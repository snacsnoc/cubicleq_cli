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
- `.cubicleq/config.json` is process-launch config only, it does not carry Qwen auth/provider/model selection

### Repo Qwen config

Use the repo `.qwen` directory for Qwen auth and model selection:

- `.qwen/settings.json`: project-scoped Qwen provider, auth type, and model selection
- `.qwen/.env`: preferred place for project-local Qwen API keys

For OpenAI-compatible providers, use Qwen’s native project config path:

```json
{
  "modelProviders": {
    "openai": [
      {
        "id": "MiniMax-M2.7",
        "envKey": "MINI_MAX_TOKEN_API_KEY",
        "baseUrl": "https://api.minimax.io/v1"
      }
    ]
  },
  "security": {
    "auth": {
      "selectedType": "openai"
    }
  },
  "model": {
    "name": "MiniMax-M2.7"
  }
}
```

Runtime behavior:
- workers derive worktree-local `.qwen/settings.json` from the repo file and copy `.qwen/.env` when present
- `cubicleq orchestrate` derives runtime settings under `.cubicleq/runs/orchestrator/.qwen/settings.json`

### `.cubicleq/policy.json`

```json
{
  "base_branch": "main",
  "cleanup_worktree_on_accept": false,
  "rejection_target_state": "todo",
  "orchestrator": {
    "enabled": true,
    "allowed_actions": [
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
- `review_accept` and `review_reject` are **not** in the default policy. Add them explicitly to enable orchestrator driven review actions

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
