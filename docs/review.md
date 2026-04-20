# review - review queue operations

## SYNOPSIS

```bash
cubicleq review list
cubicleq review show <task-id>
cubicleq review accept <task-id>
cubicleq review reject <task-id> [--note "..."]
cubicleq orchestrate
```

## DESCRIPTION

Tasks enter `review` after worker completion and finalization.
Review artifacts are stored under `.cubicleq/runs/<task-id>/`.

`review accept`:
- requires task state `review`
- requires `merge_branch` in policy
- requires review-ready validation state
- snapshots worktree changes
- merges task branch into `base_branch`
- marks task `done`

On merge conflict:
- review status becomes `conflict`
- task remains in `review`

`review reject`:
- requires task state `review`
- moves task to `rejection_target_state` (default `todo`)

## ORCHESTRATOR PATH

`cubicleq orchestrate` can execute review actions only when policy allows them.

Orchestrator acceptance requires both:
- `review_accept` in `orchestrator.allowed_actions`
- `merge_branch` in `orchestrator.allowed_actions`

Default policy excludes `review_accept`.
