# cubicleq

`cubicleq` (*cubicle queue*) runs [qwen](https://github.com/QwenLM/qwen-code) workers in parallel git worktrees, tracks everything in SQLite, and can run a separate orchestrator agent to handle review decisions. 
A worker reports it's state by an embedded MCP server, so you know what each worker is doing without having to check in.

State flow: `todo -> ready -> running -> review -> done` with `blocked` and `failed` paths.

Impatient? [Skip to Quick start](#quick-start) or [skip to Example use](#example-use)

## Why this exists

I tried *really* hard not to build another agent tool.
I also did not want another maximalist framework that claims to do everything, prints money, and does your grocery shopping.

I wanted something more end-to-end than a previous project [swimlane-runner](https://github.com/snacsnoc/swimlane-runner), which automated a two lane qwen loop (Peter/Paul) but still left the human scheduling, inspecting, and deciding each step.

I looked at other tools and they scratched one itch but caused another:

| Tool | Why it did not fit |
|---|---|
| `mngr` | Session manager, not a durable task/blocker/dependency control plane. |
| `agtx` | Good model, but heavier than needed and not the backend/workflow fit here. |
| `Maestro` | Ops console style; this project is a thin local CLI. |
| `vibe-kanban` | GUI/workspace-first; this is CLI-first. |
| `kaban` | Install/runtime issues in testing; also more board than orchestrator. |
| `Task Master AI` | Good task planning, but still needs separate runtime/worktree orchestration. |
| `AgentPipe` | Conversation orchestration, not independent worker orchestration with durable state. |
| `Ralph / Ralph Loop` | Sequential loop model; needed parallel lanes with blocker/review ownership. |
| `Hive` | Good workspace/session handling, weaker task/blocker control-plane focus. |
| `multi-agent-shogun` | Closest shape, still not a drop-in fit for this backend/workflow. |

If one of these fit the full loop, `cubicleq` would not exist.

## Ethos and mental model of this project
As a committment to myself and whichever random users of this project, I wanted to stick a 'do one thing and do it well' approach, and be kind to users. The user should not need to remember internal workflow.

At any point, `cubicleq` should answer:
- what is stuck?
- what needs action?
- what is the next command?

If it cannot answer those directly, the UX is wrong.

`cubicleq` stays the control plane.  
`cubicleq run` schedules task execution.  
`cubicleq orchestrate` runs policy-gated review actions.

If a feature makes `cubicleq` less state-driven and more prompt-driven, it is off-pitch.

Think office floor, not a wall of tmux panes:
- each worker gets a bounded cubicle 
- you do not need to hover every terminal
- output matters more than live chatter
- peeking into a worker terminal is optional diagnostics, not the main workflow


## Quick start

Build:

```bash
go build -o ./bin/cubicleq ./cmd/cubicleq
go test ./...
go vet ./...
```

Cleanup:

```bash
rm -f ./bin/cubicleq
rm -rf ./.cache/go-build ./.cache/go-mod ./.cache/go-tmp
```

Initialize:

```bash
./bin/cubicleq init
```

Another repo:

```bash
./bin/cubicleq --root /path/to/repo init --bootstrap-git
```

Check that qwen is already authorized:

```
qwen auth
```

Day-to-day (see [docs/operator-guide.md](./docs/operator-guide.md), [docs/running.md](./docs/running.md), [docs/tasks.md](./docs/tasks.md)):

```bash
./bin/cubicleq tasks add --title "fix auth" --validate "pytest tests/auth/" # validate is optional
./bin/cubicleq tasks add --title "add tests" --depends-on "<task-id>"
./bin/cubicleq run
./bin/cubicleq status
./bin/cubicleq logs <task-id>
./bin/cubicleq review accept <task-id>
./bin/cubicleq orchestrate
```

## Config

Reference: [docs/config.md](./docs/config.md)

On `cubicleq init`, `.cubicleq/` is created in the project root.

`.cubicleq/config.json` controls scheduler concurrency, worktree location, and worker backend command.

`.cubicleq/config.json`:

```json
{
  "max_parallel_tasks": 2,
  "worktree_dir": "/path/to/repo/worktrees",
  "backend": { "command": "qwen", "args": [] }
}
```

Main settings:
- `max_parallel_tasks`: maximum number of tasks `cubicleq run` launches concurrently.
- `worktree_dir`: path for per-task git worktrees. Must be `<repo>/worktrees`.
- `backend.command` and `backend.args`: executable and arguments used to start worker runs.


### Policy - what agents can and cannot do

`.cubicleq/policy.json` controls merge target branch, post-accept cleanup, rejection target state, and orchestrator action permissions.

`.cubicleq/policy.json`:

```json
{
  "base_branch": "main",
  "cleanup_worktree_on_accept": false,
  "rejection_target_state": "todo",
  "orchestrator": {
    "enabled": true,
    "allowed_actions": ["retry_task", "resolve_blocker", "merge_branch"]
  }
}
```

Main settings:
- `base_branch`: branch used as the merge target for accepted review tasks.
- `cleanup_worktree_on_accept`: removes task worktree after successful review acceptance when `true`.
- `rejection_target_state`: state assigned by `review reject`.
- `orchestrator.enabled`: enables `cubicleq orchestrate`.
- `orchestrator.allowed_actions`: allowlist for orchestrator actions.

`review accept` enforces `merge_branch` policy.
For orchestrator-driven accepts, add `review_accept` to `allowed_actions`.

## Example use

Suppose your project has a broken login redirect. You want to fix it, add a test, and update the README in that order, with the test guarded by the fix.

### Initialize the project

```bash
cd /my-not-boring/project
./bin/cubicleq init
# creates .cubicleq/state.db, .cubicleq/config.json, .cubicleq/policy.json
```


### Create tasks

```bash
# t-1: fix the redirect — validated with a targeted test
./bin/cubicleq tasks add \
  --title "fix login redirect" \
  --description "Redirect to /dashboard after login instead of /" \
  --validate "python -m pytest tests/auth/test_login.py -v"

# t-2: add regression tests — depends on t-1 being merged
./bin/cubicleq tasks add \
  --title "add auth regression tests" \
  --description "Cover redirect, session expiry, and CSRF token cases" \
  --validate "python -m pytest tests/auth/ -v" \
  --depends-on "t-1"

# t-3: update README — no validation needed
./bin/cubicleq tasks add \
  --title "update auth docs in README" \
  --description "Document the new /dashboard redirect and session expiry"
```

List to confirm:

```bash
./bin/cubicleq tasks list
# ID       TITLE                     STATE  DEPENDENCIES
# t-1      fix login redirect        todo   —
# t-2      add auth regression tests todo   t-1
# t-3      update auth docs in README todo  —
```

### Start the scheduler

```bash
./bin/cubicleq run
```

The scheduler picks `t-1` and `t-3` (both `todo` with all dependencies satisfied) and launches workers in parallel, up to `max_parallel_tasks` (default 2).
 `t-2` stays `todo` because its dependency `t-1` is not yet `review` or `done`.

```
# sample status output while running
./bin/cubicleq status
# RUNNING
#   t-1  fix login redirect         (agent: qwen, pid 1234)
#   t-3  update auth docs in README (agent: qwen, pid 5678)
# READY
#   —
# REVIEW
#   —
# BLOCKED
#   —
# DONE
#   —
```

Workers report heartbeats and completion via the embedded MCP server. When a worker calls `complete_task`, the scheduler runs the configured validation commands inside the task worktree. On success the task moves to `review`, on failure it moves to `failed`.

### 4. Inspect logs and blockers

```bash
./bin/cubicleq logs t-1
# shows worker stdout/stderr from .cubicleq/logs/t-1.stdout.log
```

If something breaks:

```bash
./bin/cubicleq status
# FAILED
#   t-1  fix login redirect  (exited, exit code 1)
```

Retry after pushing a fix:

```bash
./bin/cubicleq retry t-1
# clears runtime, review, validation, artifacts; keeps dependencies
# task returns to todo; scheduler will relaunch on next `run`
```

### 5. Accept review and advance the chain

When `t-1` enters `review` with passing validation:

```bash
./bin/cubicleq review list
# t-1  fix login redirect  → review

./bin/cubicleq review accept t-1
# merges task/t-1-fix-login-redirect into main
# marks t-1 done
```

Now `t-2` dependencies are satisfied. On the next `run` the scheduler picks it up.

Repeat for `t-2` and `t-3`.

### Optional: let the orchestrator agent handle review
This is one of the core features I built into cubicleq. The orchestrator agent can accept or reject reviews on your behalf when policy allows it. 
Enable it by adding `review_accept` to the policy allowlist:

```bash
# in .cubicleq/policy.json:
# "orchestrator": { "enabled": true, "allowed_actions": ["review_accept", "retry_task", "resolve_blocker", "merge_branch"] }
```

Then run the agent:

```bash
./bin/cubicleq orchestrate
# agent reads review queue, applies policy, accepts/rejects/retries as allowed
```

This was the missing step in other CLI tools I surveyed. I gave the agents a *thing* to do, they executed the *thing*, now have another higher-level agent review the *thing* for me.


After the run completes you have:
- merged changes on `main`
- passing validation records in `.cubicleq/runs/t-*/validation/`
- review summaries and diffs in `.cubicleq/runs/t-*/`

No transcript reading required, go make some coffee! You deserve it!

### Runtime files

See [docs/files.md](./docs/files.md).


# Docs

- [docs/README.md](./docs/README.md)
- [docs/operator-guide.md](./docs/operator-guide.md)
- [docs/config.md](./docs/config.md)
- [docs/worker-mcp.md](./docs/worker-mcp.md)
- Policy schema is in [docs/config.md](./docs/config.md).  
- Policy-gated review behavior is in [docs/review.md](./docs/review.md).
