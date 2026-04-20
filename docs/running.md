# running - scheduler and orchestrator behavior

## SYNOPSIS

```bash
cubicleq run
cubicleq orchestrate
cubicleq status
cubicleq stop
cubicleq logs <task-id>
```

## DESCRIPTION

`cubicleq run`:
- launches ready tasks up to `max_parallel_tasks`
- reconciles runtime state
- finalizes completed tasks
- does not perform review decisions

`cubicleq orchestrate`:
- requires `policy.orchestrator.enabled = true`
- executes only actions allowed by policy
- does not schedule workers

Keep `run` and `orchestrate` separate.

`cubicleq status` derives next actions from SQLite state.

Action mapping:
- blocked task: `cubicleq logs <task-id>`
- review task: `cubicleq review accept <task-id>` or `cubicleq review reject <task-id> [--note "..."]`
- runnable work with no live workers: `cubicleq run`
- empty queue: `cubicleq tasks add --title "..."`

`completed` runtime rows are recovery artifacts, not active workers.

## WARNINGS

`Ctrl+C` during `cubicleq run`:
- interrupts workers
- requeues running tasks to `todo`
- keeps prior artifacts and state on disk

`cubicleq stop` from another terminal requests graceful stop and exits the foreground run loop.
