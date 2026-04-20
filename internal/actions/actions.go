package actions

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/review"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

type Executor struct {
	Root   string
	Store  *state.Store
	Policy config.Policy
}

func (e Executor) AcceptReview(taskID, actor string) (string, error) {
	task, err := e.Store.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if task.State != state.TaskStateReview {
		return "", fmt.Errorf("task %s is not in review", task.ID)
	}
	if !config.AllowsAction(e.Policy, "merge_branch") {
		return "", fmt.Errorf("policy denied action %q", "merge_branch")
	}
	if err := EnsureReviewReady(e.Store, task); err != nil {
		return "", err
	}
	if err := review.Accept(e.Root, task, review.AcceptOptions{
		BaseBranch:         e.Policy.BaseBranch,
		CleanupWorktree:    e.Policy.CleanupWorktreeOnAccept,
		MergeCommitMessage: fmt.Sprintf("cubicleq: accept %s", task.ID),
	}); err != nil {
		if errors.Is(err, review.ErrMergeConflict) {
			bookkeepingErr := joinBookkeepingErrors(
				e.Store.SetReviewStatus(task.ID, "conflict"),
				e.Store.RecordEvent(task.ID, "review_accept_conflict", map[string]any{"error": err.Error(), "actor": actor}),
			)
			return "", mergeBookkeepingError(err, "review conflict state", bookkeepingErr)
		}
		if errors.Is(err, review.ErrNoMergeChanges) {
			bookkeepingErr := joinBookkeepingErrors(
				e.Store.RecordEvent(task.ID, "review_accept_noop", map[string]any{"error": err.Error(), "actor": actor}),
			)
			return "", mergeBookkeepingError(err, "review no-op event", bookkeepingErr)
		}
		return "", err
	}
	if err := e.Store.AcceptReview(task.ID, e.Policy.CleanupWorktreeOnAccept); err != nil {
		return "", err
	}
	if err := e.Store.RecordEvent(task.ID, "review_accept", map[string]any{"base_branch": e.Policy.BaseBranch, "actor": actor}); err != nil {
		return "", err
	}
	return fmt.Sprintf("accepted %s into %s", task.ID, e.Policy.BaseBranch), nil
}

func (e Executor) RejectReview(taskID, note, actor string) (string, error) {
	task, err := e.Store.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if task.State != state.TaskStateReview {
		return "", fmt.Errorf("task %s is not in review", task.ID)
	}
	target := state.TaskStateTodo
	if e.Policy.RejectionTargetState == state.TaskStateFailed {
		target = state.TaskStateFailed
	}
	if err := e.Store.RejectReview(taskID, target); err != nil {
		return "", err
	}
	if err := e.Store.RecordEvent(taskID, "review_reject", map[string]any{"note": note, "target_state": target, "actor": actor}); err != nil {
		return "", err
	}
	return fmt.Sprintf("rejected %s -> %s", taskID, target), nil
}

func (e Executor) RetryTask(taskID, actor string) (string, error) {
	if err := e.Store.RetryTask(taskID); err != nil {
		return "", err
	}
	if err := e.Store.RecordEvent(taskID, "retry_task", map[string]any{"actor": actor}); err != nil {
		return "", err
	}
	return fmt.Sprintf("retried %s", taskID), nil
}

func (e Executor) ResolveBlocker(taskID, actor string) (string, error) {
	if err := e.Store.ResolveBlocker(taskID); err != nil {
		return "", err
	}
	if err := e.Store.RecordEvent(taskID, "resolve_blocker", map[string]any{"actor": actor}); err != nil {
		return "", err
	}
	return fmt.Sprintf("resolved blocker for %s -> %s", taskID, state.TaskStateTodo), nil
}

func (e Executor) CreateFollowupTask(title, description, role string, dependsOn []string, validationCommands []string, actor string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", errors.New("create_followup_task requires title")
	}
	validationCommands = trimNonEmpty(validationCommands)
	if len(validationCommands) == 0 {
		return "", errors.New("create_followup_task requires at least one validation command")
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = "implementer"
	}
	task := state.Task{
		ID:                 state.NewTaskID(),
		Title:              title,
		Description:        strings.TrimSpace(description),
		Priority:           "medium",
		State:              state.TaskStateTodo,
		RoleHint:           role,
		Dependencies:       dependsOn,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		ValidationCommands: validationCommands,
	}
	if err := e.Store.AddTask(task); err != nil {
		return "", err
	}
	if err := e.Store.RecordEvent(task.ID, "create_followup_task", map[string]any{"actor": actor, "validation_commands": validationCommands}); err != nil {
		return "", err
	}
	return fmt.Sprintf("created follow-up task %s", task.ID), nil
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func joinBookkeepingErrors(errs ...error) error {
	var failures []string
	for _, err := range errs {
		if err == nil {
			continue
		}
		failures = append(failures, err.Error())
	}
	if len(failures) == 0 {
		return nil
	}
	return errors.New(strings.Join(failures, "; "))
}

func mergeBookkeepingError(primary error, scope string, bookkeepingErr error) error {
	if bookkeepingErr == nil {
		return primary
	}
	return fmt.Errorf("%w; additionally failed to record %s: %s", primary, scope, bookkeepingErr)
}

func EnsureReviewReady(store *state.Store, task state.Task) error {
	if len(task.ValidationCommands) == 0 {
		return nil
	}
	runs, err := store.ListValidationRuns(task.ID)
	if err != nil {
		return err
	}
	filtered := make([]state.ValidationRun, 0, len(runs))
	for _, run := range runs {
		// Legacy synthetic marker from the old "missing validation blocks task" path.
		// Real configured validation should ignore this row.
		if run.Command == "validation:not-configured" {
			continue
		}
		filtered = append(filtered, run)
	}
	if len(filtered) == 0 {
		return errors.New("review has no validation record")
	}
	for _, run := range filtered {
		if run.Status != "passed" {
			return fmt.Errorf("validation failed for %s", run.Command)
		}
	}
	return nil
}
