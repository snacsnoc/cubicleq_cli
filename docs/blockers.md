# blockers - blocked-state operations

## SYNOPSIS

```bash
cubicleq blockers list
cubicleq blockers resolve <task-id>
```

## DESCRIPTION

Blockers are durable state.

`blockers resolve`:
- removes blocker row
- removes review row for the same task
- removes stale runtime row
- clears assigned agent
- moves task to `todo`

If `cubicleq run` is active, the scheduler can pick the task immediately.

## WORKER PATH (MCP)

Workers set blocked state with `block_task` or `release_task`.

`block_task`:
- requires task state `running`
- writes blocker reason
- deletes active runtime row
- moves task to `blocked`

`release_task`:
- requires active runtime row
- deletes runtime row
- writes blocker reason
- moves task to `blocked`
