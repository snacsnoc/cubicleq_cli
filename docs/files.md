# files - runtime file layout

## DESCRIPTION

`.cubicleq/` stores state and run artifacts.
`worktrees/` stores task worktrees.

## FILES

| Artifact | Path |
|---|---|
| SQLite store | `.cubicleq/state.db` |
| Config | `.cubicleq/config.json` |
| Policy | `.cubicleq/policy.json` |
| Task prompt | `.cubicleq/runs/<task-id>/prompt.md` |
| Task context | `.cubicleq/runs/<task-id>/context.json` |
| Review summary | `.cubicleq/runs/<task-id>/review-summary.md` |
| Diff summary | `.cubicleq/runs/<task-id>/diff-summary.md` |
| Validation logs | `.cubicleq/runs/<task-id>/validation/` |
| Worker stdout | `.cubicleq/logs/<task-id>.stdout.log` |
| Worker stderr | `.cubicleq/logs/<task-id>.stderr.log` |
| Task worktree | `<repo>/worktrees/<task-id>/` |
| Orchestrator prompt | `.cubicleq/runs/orchestrator/prompt.md` |
| Orchestrator context | `.cubicleq/runs/orchestrator/context.json` |

## NOTES

- `logs/*.stdout.log` and `logs/*.stderr.log` are truncated on relaunch
- validation logs are grouped by validation command under each task run directory
