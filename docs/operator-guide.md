# operator-guide - operator workflow

## SYNOPSIS

```bash
go build -o ./bin/cubicleq ./cmd/cubicleq
./bin/cubicleq init
./bin/cubicleq tasks add --title "task 1" --validate "pytest"
./bin/cubicleq tasks add --title "task 2" --depends-on "<task-1-id>"
./bin/cubicleq run
./bin/cubicleq status
./bin/cubicleq review accept <task-id>
```

## DESCRIPTION

Operate against durable state.

Procedure:
1. Create tasks.
2. Start workers.
3. Inspect `status`, logs, blockers, and review queue.
4. Accept or reject review items.
5. Retry failed tasks or resolve blockers.

Use `cubicleq run` for scheduling.
Use `cubicleq orchestrate` for policy-gated review actions.

## EXAMPLES

```bash
./bin/cubicleq --root /Users/easto/test/gnu-nano-clone init --bootstrap-git
```

```bash
./bin/cubicleq tasks add --title "task 1" --description "do the thing" --validate "pytest"
./bin/cubicleq tasks add --title "task 2" --description "do the next thing" --validate "pytest tests/test_cli.py"
./bin/cubicleq tasks set-deps <task-2-id> --depends-on "<task-1-id>"
```

```bash
./bin/cubicleq status
./bin/cubicleq logs <task-id>
./bin/cubicleq blockers list
./bin/cubicleq review list
./bin/cubicleq stop
```

```bash
./bin/cubicleq orchestrate
```

## WARNINGS

- If `--root` is omitted, current directory is used.
- `Ctrl+C` during `run` interrupts workers and requeues running tasks.
- `retry` clears runtime, review, validation, and artifacts; task metadata and dependencies remain.
- Default policy does not allow orchestrator-driven review acceptance; add `review_accept` when needed.
