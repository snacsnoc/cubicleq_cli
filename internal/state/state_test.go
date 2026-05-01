package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveBlockerClearsRuntimeAndAssignment(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := Task{
		ID:          "t-1",
		Title:       "blocked task",
		Description: "",
		Priority:    "medium",
		State:       TaskStateTodo,
		RoleHint:    "implementer",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, "task/t-1", "/tmp/worktree-t-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateReady, ""); err != nil {
		t.Fatal(err)
	}
	rt := Runtime{
		TaskID:        task.ID,
		BranchName:    "task/t-1",
		WorktreePath:  "/tmp/worktree-t-1",
		SessionID:     "s-1",
		Status:        "launching",
		PID:           123,
		LastHeartbeat: time.Now().UTC(),
	}
	if err := store.UpsertRuntime(rt); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimTask(task.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := store.BlockTask(task.ID, "needs input"); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveBlocker(task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateTodo {
		t.Fatalf("state = %s", got.State)
	}
	if got.AssignedAgent != "" {
		t.Fatalf("assigned agent not cleared: %q", got.AssignedAgent)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 0 {
		t.Fatalf("expected no runtimes after resolving blocker, got %#v", runtimes)
	}
	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no reviews after resolving blocker, got %#v", reviews)
	}
}

func TestSetTaskDependenciesReplacesCurrentList(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "c", Title: "c", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetTaskDependencies("c", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Dependencies, ",") != "a,b" {
		t.Fatalf("unexpected dependencies after set: %v", got.Dependencies)
	}

	if err := store.SetTaskDependencies("c", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Dependencies, ",") != "b" {
		t.Fatalf("expected replacement semantics, got %v", got.Dependencies)
	}

	if err := store.SetTaskDependencies("c", nil); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dependencies) != 0 {
		t.Fatalf("expected dependencies to clear, got %v", got.Dependencies)
	}
}

func TestSetTaskDependenciesCanonicalizesWhitespaceAndPreservesOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "c", Title: "c", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetTaskDependencies("c", []string{" b ", " a "}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Dependencies, ",") != "b,a" {
		t.Fatalf("expected canonicalized dependency order to be preserved, got %v", got.Dependencies)
	}
}

func TestAddTaskCanonicalizesDependenciesBeforePersisting(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	task := Task{
		ID:           "c",
		Title:        "c",
		Priority:     "medium",
		State:        TaskStateTodo,
		RoleHint:     "implementer",
		Dependencies: []string{" b ", " a "},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask("c")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Dependencies, ",") != "b,a" {
		t.Fatalf("expected add task to persist canonical dependencies, got %v", got.Dependencies)
	}
}

func TestDependencyValidationRejectsUnknownSelfDuplicateAndEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetTaskDependencies("a", []string{"missing"}); err == nil {
		t.Fatal("expected unknown dependency to fail")
	}
	if err := store.SetTaskDependencies("a", []string{"a"}); err == nil {
		t.Fatal("expected self dependency to fail")
	}
	if err := store.SetTaskDependencies("a", []string{"b", "b"}); err == nil {
		t.Fatal("expected duplicate dependency to fail")
	}
	if err := store.SetTaskDependencies("a", []string{" "}); err == nil {
		t.Fatal("expected empty dependency to fail")
	}
	if err := store.SetTaskDependencies("a", []string{" b ", "b"}); err == nil {
		t.Fatal("expected duplicate dependency after trimming to fail")
	}
}

func TestSetTaskDependenciesRejectsDirectCycle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetTaskDependencies("a", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	err = store.SetTaskDependencies("b", []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle detected involving b") {
		t.Fatalf("expected direct cycle rejection, got %v", err)
	}
}

func TestSetTaskDependenciesRejectsIndirectCycle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "b", Title: "b", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "c", Title: "c", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetTaskDependencies("a", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskDependencies("b", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	err = store.SetTaskDependencies("c", []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle detected involving c") {
		t.Fatalf("expected indirect cycle rejection, got %v", err)
	}
}

func TestAddTaskRejectsCycleAgainstExistingDependencyRows(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := store.AddTask(Task{ID: "a", Title: "a", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO task_dependencies (task_id, depends_on, position) VALUES (?, ?, ?)`, "a", "b", 0); err != nil {
		t.Fatal(err)
	}

	err = store.AddTask(Task{
		ID:           "b",
		Title:        "b",
		Priority:     "medium",
		State:        TaskStateTodo,
		RoleHint:     "implementer",
		Dependencies: []string{"a"},
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle detected involving b") {
		t.Fatalf("expected add task to reject cycle, got %v", err)
	}
}

func TestPromoteReadyTasksTreatsReviewAsSatisfiedDependency(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dep := Task{ID: "dep", Title: "dep", Priority: "medium", State: TaskStateReview, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	task := Task{ID: "task", Title: "task", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", Dependencies: []string{"dep"}, CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(dep); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteReadyTasks(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTask("task")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateReady {
		t.Fatalf("expected dependent task to become ready when dependency is in review, got %s", got.State)
	}
}

func TestPromoteReadyTasksErrorsWhenDependencyRowTargetsMissingTask(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dep := Task{ID: "dep", Title: "dep", Priority: "medium", State: TaskStateDone, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	task := Task{ID: "task", Title: "task", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", Dependencies: []string{"dep"}, CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(dep); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM tasks WHERE id = ?`, dep.ID); err != nil {
		t.Fatal(err)
	}

	err = store.PromoteReadyTasks()
	if err == nil || !strings.Contains(err.Error(), "dependency dep does not exist") {
		t.Fatalf("expected missing dependency error, got %v", err)
	}
}

func TestHasRunnableTasksUsesStoreDependencyTruth(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dep := Task{ID: "dep", Title: "dep", Priority: "medium", State: TaskStateBlocked, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	task := Task{ID: "task", Title: "task", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", Dependencies: []string{"dep"}, CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(dep); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	runnable, err := store.HasRunnableTasks()
	if err != nil {
		t.Fatal(err)
	}
	if runnable {
		t.Fatal("expected no runnable tasks while dependency is unsatisfied")
	}

	if err := store.SetTaskState("dep", TaskStateReview, ""); err != nil {
		t.Fatal(err)
	}
	runnable, err = store.HasRunnableTasks()
	if err != nil {
		t.Fatal(err)
	}
	if !runnable {
		t.Fatal("expected runnable task once dependency reaches review")
	}
}

func TestRetryTaskClearsStaleOutputMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := Task{
		ID:                 "t-2",
		Title:              "retry task",
		Description:        "",
		Priority:           "medium",
		State:              TaskStateReview,
		RoleHint:           "implementer",
		BranchName:         "task/t-2",
		WorktreePath:       "/tmp/worktree-t-2",
		FilesChanged:       []string{"a.txt"},
		TestResults:        []string{"ok"},
		CompletionSummary:  "done",
		AssignedAgent:      "worker",
		ValidationCommands: []string{"true"},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := seedTaskOutput(store, task.ID, "done", []string{"a.txt"}, []string{"ok"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, "task/t-2", "/tmp/worktree-t-2"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTaskArtifact(task.ID, "review_summary", "/tmp/review.md"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeReview(task.ID, "/tmp/review.md", "/tmp/diff.md"); err != nil {
		t.Fatal(err)
	}
	if err := store.RetryTask(task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BranchName != "" || got.WorktreePath != "" {
		t.Fatalf("expected branch/worktree cleared, got branch=%q worktree=%q", got.BranchName, got.WorktreePath)
	}
	if len(got.FilesChanged) != 0 || len(got.TestResults) != 0 {
		t.Fatalf("expected stale outputs cleared, got files=%v tests=%v", got.FilesChanged, got.TestResults)
	}
	if got.AssignedAgent != "" {
		t.Fatalf("expected assigned agent cleared, got %q", got.AssignedAgent)
	}
	if got.CompletionSummary != "" {
		t.Fatalf("expected completion summary cleared, got %q", got.CompletionSummary)
	}
}

func seedTaskOutput(store *Store, taskID, summary string, filesChanged, testResults []string) error {
	_, err := store.db.Exec(
		`UPDATE tasks SET completion_summary = ?, files_changed = ?, test_results = ?, updated_at = ? WHERE id = ?`,
		summary,
		toJSON(filesChanged),
		toJSON(testResults),
		time.Now().UTC().Format(time.RFC3339),
		taskID,
	)
	return err
}

func TestResolveBlockerAllowsMissingValidationConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := Task{
		ID:            "t-3",
		Title:         "missing validation",
		Priority:      "medium",
		State:         TaskStateBlocked,
		RoleHint:      "implementer",
		BlockedReason: "no validation configured",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "no validation configured", time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateBlocked, "no validation configured"); err != nil {
		t.Fatal(err)
	}

	if err := store.ResolveBlocker(task.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateTodo {
		t.Fatalf("expected todo after resolving blocker, got %s", got.State)
	}
}

func TestPromoteReadyTasksLeavesMissingValidationBlockerUntouched(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{
		ID:            "t-auto-resolve",
		Title:         "auto resolve",
		Priority:      "medium",
		State:         TaskStateBlocked,
		RoleHint:      "implementer",
		BlockedReason: "no validation configured",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "no validation configured", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateBlocked, "no validation configured"); err != nil {
		t.Fatal(err)
	}

	if err := store.PromoteReadyTasks(); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateBlocked {
		t.Fatalf("expected blocked task to remain blocked, got %s", got.State)
	}

	blockers, err := store.ListBlockers()
	if err != nil {
		t.Fatal(err)
	}
	foundBlocker := false
	for _, blocker := range blockers {
		if blocker.TaskID == task.ID {
			foundBlocker = true
		}
	}
	if !foundBlocker {
		t.Fatalf("expected blocker row for %s to remain", task.ID)
	}

	events, err := store.ListEvents(task.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "blocker_auto_resolved_validation_optional" {
			t.Fatalf("did not expect auto-resolve event for %s, got %#v", task.ID, events)
		}
	}
}

func TestMarkTaskReadyRequiresSatisfiedDependencies(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for _, task := range []Task{
		{ID: "dep", Title: "dep", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "child", Title: "child", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", Dependencies: []string{"dep"}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	err = store.MarkTaskReady("child")
	if err == nil || !strings.Contains(err.Error(), "unsatisfied dependencies") {
		t.Fatalf("expected unsatisfied dependency error, got %v", err)
	}

	got, err := store.GetTask("child")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateTodo {
		t.Fatalf("expected child to remain todo, got %s", got.State)
	}

	if err := store.SetTaskState("dep", TaskStateReview, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskReady("child"); err != nil {
		t.Fatalf("expected child to become ready, got %v", err)
	}
	got, err = store.GetTask("child")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateReady {
		t.Fatalf("expected child to be ready, got %s", got.State)
	}
}

func TestFinalizeReviewClearsBlockerAndRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-review", Title: "review", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, "task/t-review", "/tmp/worktree-review"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(Runtime{
		TaskID:        task.ID,
		BranchName:    "task/t-review",
		WorktreePath:  "/tmp/worktree-review",
		SessionID:     "s-review",
		Status:        "running",
		PID:           1,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "needs input", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateRunning, "needs input"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeReview(task.ID, "/tmp/review.md", "/tmp/diff.md"); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateReview {
		t.Fatalf("expected review state, got %s", got.State)
	}
	blockers, err := store.ListBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("expected no blockers after finalize review, got %#v", blockers)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 0 {
		t.Fatalf("expected no runtimes after finalize review, got %#v", runtimes)
	}
	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].TaskID != task.ID {
		t.Fatalf("expected one review row for task, got %#v", reviews)
	}
}

func TestFailTaskClearsBlockerReviewAndRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-fail", Title: "fail", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, "task/t-fail", "/tmp/worktree-fail"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(Runtime{
		TaskID:        task.ID,
		BranchName:    "task/t-fail",
		WorktreePath:  "/tmp/worktree-fail",
		SessionID:     "s-fail",
		Status:        "running",
		PID:           1,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeReview(task.ID, "/tmp/review.md", "/tmp/diff.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "blocked", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTask(task.ID, "boom"); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateFailed {
		t.Fatalf("expected failed state, got %s", got.State)
	}
	blockers, err := store.ListBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("expected no blockers after fail, got %#v", blockers)
	}
	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no reviews after fail, got %#v", reviews)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 0 {
		t.Fatalf("expected no runtimes after fail, got %#v", runtimes)
	}
}

func TestRejectReviewClearsAuxiliaryRowsAndUsesTargetState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-reject", Title: "reject", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeReview(task.ID, "/tmp/review.md", "/tmp/diff.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "stale blocker", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateReview, "stale blocker"); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectReview(task.ID, TaskStateTodo); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateTodo {
		t.Fatalf("expected todo after reject, got %s", got.State)
	}
	blockers, err := store.ListBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("expected no blockers after reject, got %#v", blockers)
	}
	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no reviews after reject, got %#v", reviews)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 0 {
		t.Fatalf("expected no runtimes after reject, got %#v", runtimes)
	}
}

func TestAcceptReviewClearsBlockerReviewAndRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-accept", Title: "accept", Priority: "medium", State: TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeReview(task.ID, "/tmp/review.md", "/tmp/diff.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, task.ID, "stale blocker", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskState(task.ID, TaskStateReview, "stale blocker"); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptReview(task.ID, false); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateDone {
		t.Fatalf("expected done after accept, got %s", got.State)
	}
	blockers, err := store.ListBlockers()
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("expected no blockers after accept, got %#v", blockers)
	}
	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no reviews after accept, got %#v", reviews)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 0 {
		t.Fatalf("expected no runtimes after accept, got %#v", runtimes)
	}
}

func TestLateClaimAfterReleaseTaskIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-late-claim", Title: "late-claim", Priority: "medium", State: TaskStateReady, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  "/tmp/worktree-late-claim",
		SessionID:     task.ID + "-session",
		Status:        "launching",
		PID:           123,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseTask(task.ID, "worker failed to report after launch"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimTask(task.ID, "late-worker"); err == nil {
		t.Fatal("expected late claim to be rejected")
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateBlocked {
		t.Fatalf("expected blocked state to remain after rejected late claim, got %s", got.State)
	}
}

func TestLateHeartbeatAfterReleaseTaskIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := Task{ID: "t-late-heartbeat", Title: "late-heartbeat", Priority: "medium", State: TaskStateRunning, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  "/tmp/worktree-late-heartbeat",
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           123,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseTask(task.ID, "stale heartbeat"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordHeartbeat(task.ID); err == nil {
		t.Fatal("expected late heartbeat to be rejected")
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskStateBlocked {
		t.Fatalf("expected blocked state to remain after rejected late heartbeat, got %s", got.State)
	}
}
