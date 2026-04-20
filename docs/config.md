# config - runtime and policy configuration

## CONFIGURATION

### `.cubicleq/config.json`

```json
{
  "max_parallel_tasks": 2,
  "worktree_dir": "/path/to/repo/worktrees",
  "backend": {
    "command": "qwen",
    "args": []
  }
}
```

Rules:
- `worktree_dir` must be repo-local at `<repo>/worktrees`
- `backend.command` must resolve on `PATH` (`doctor` checks this)

### `.cubicleq/policy.json`

```json
{
  "base_branch": "main",
  "cleanup_worktree_on_accept": false,
  "rejection_target_state": "todo",
  "orchestrator": {
    "enabled": true,
    "allowed_actions": [
      "review_accept",
      "review_reject",
      "retry_task",
      "resolve_blocker",
      "merge_branch"
    ]
  }
}
```

Rules:
- `rejection_target_state` is required
- `orchestrator.allowed_actions` is required and must be non-empty
- `orchestrator.enabled` and `orchestrator.allowed_actions` are separate gates

## OPTIONS

Policy action names used by runtime:
- `retry_task`
- `resolve_blocker`
- `merge_branch`
- `review_accept`
- `review_reject`
- `create_followup_task`

Notes:
- `merge_branch` is checked at execution time for all review-accept paths
- orchestrator `review_accept` requires `review_accept` and `merge_branch` in `allowed_actions`
