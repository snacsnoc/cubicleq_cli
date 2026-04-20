# tasks - task lifecycle, dependencies, validation, retry

## SYNOPSIS

```bash
cubicleq tasks add --title "..." [--description "..."] [--validate "cmd1,cmd2"] [--depends-on "t-1,t-2"]
cubicleq tasks list
cubicleq tasks show <task-id>
cubicleq tasks set-deps <task-id> --depends-on "t-1,t-2"
cubicleq tasks set-validation <task-id> --validate "cmd1,cmd2"
cubicleq tasks ready <task-id>
cubicleq retry <task-id>
```

## DESCRIPTION

Lifecycle:

`todo -> ready -> running -> review -> done`

Additional states: `blocked`, `failed`.

Tasks move to `review` after completion and finalization.

Validation:
- configured commands: all must pass or task fails
- no commands: skipped validation record is written; task can enter `review`

Dependencies are durable.
`tasks set-deps` replaces the full dependency list.
`--depends-on ""` clears dependencies.

A dependency is satisfied when upstream task state is `review` or `done`.

Dependency checks reject unknown IDs, self-dependency, duplicates, and cycles.

## WARNINGS

`retry` is a strong reset.

Cleared:
- blockers, reviews, validation runs, artifacts, and runtimes
- branch and worktree binding
- completion metadata (`files_changed`, `test_results`, `completion_summary`, assigned agent)

Kept:
- task identity and metadata (`id`, title, description, role, priority)
- dependency metadata
