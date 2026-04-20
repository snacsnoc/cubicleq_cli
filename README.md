# cubicleq

`cubicleq` (*cubicle queue*) runs [qwen](https://github.com/QwenLM/qwen-code) workers in parallel git worktrees, tracks everything in SQLite, and can run a separate orchestrator agent to handle review decisions.

State flow: `todo -> ready -> running -> review -> done` with `blocked` and `failed` paths.

Impatient? [Skip to Quick start](#quick-start)

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

The user should not need to remember internal workflow.

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

### Runtime files

See [docs/files.md](./docs/files.md).


# Docs

- [docs/README.md](./docs/README.md)
- [docs/operator-guide.md](./docs/operator-guide.md)
- [docs/config.md](./docs/config.md)
- [docs/worker-mcp.md](./docs/worker-mcp.md)
- Policy schema is in [docs/config.md](./docs/config.md).  
- Policy-gated review behavior is in [docs/review.md](./docs/review.md).

