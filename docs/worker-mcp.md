# worker-mcp - worker progress reporting

## SYNOPSIS

Workers launched by `cubicleq run` report progress back to cubicleq through a local MCP server.

You, as the squishy human, usually do not call these tools directly.
You see their effects through:

```bash
cubicleq status
cubicleq logs <task-id>
cubicleq blockers list
cubicleq review list
```

## DESCRIPTION

cubicleq starts a local MCP server for worker reporting while `cubicleq run` is active.
Workers use it to move tasks forward, surface blockers, and attach evidence.

Worker reporting actions:

- `claim_task`: worker starts the task and moves it into active execution
- `heartbeat`: worker reports that the task is still in progress
- `block_task`: worker stops and records a blocker reason
- `complete_task`: worker reports completion and hands the task to finalization
- `attach_artifact`: worker attaches a file path to the task record
- `release_task`: worker gives up the task without marking it complete

User-facing outcomes:

- if a worker claims a task, `status` shows it as running
- if a worker sends heartbeats, Cubicleq can tell the runtime is still alive
- if a worker blocks or releases a task, the task moves to `blocked`
- if a worker completes a task, Cubicleq runs validation and may move the task to `review`
- if a worker attaches artifacts, they appear in `logs` and related task output

These tools are part of the worker reporting contract and should be treated as stable.

## NOTES

- This MCP server is local-only and is meant for same-host execution.
- Do not expose it outside the machine running `cubicleq run`.
- If worker reporting stops, inspect `cubicleq logs <task-id>` first.
