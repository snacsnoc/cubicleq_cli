package actions

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/review"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestCreateFollowupTaskRequiresValidationCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	executor := Executor{
		Root:   root,
		Store:  store,
		Policy: config.DefaultPolicy("main"),
	}
	if _, err := executor.CreateFollowupTask("follow-up", "desc", "implementer", nil, nil, "test"); err == nil {
		t.Fatal("expected missing validation commands to fail")
	}
}

func TestEnsureReviewReadyAllowsMissingValidationCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := state.Task{
		ID:        "t-review-no-validation",
		Title:     "review task",
		Priority:  "medium",
		State:     state.TaskStateReview,
		RoleHint:  "implementer",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	if err := EnsureReviewReady(store, task); err != nil {
		t.Fatalf("expected missing validation commands to be allowed, got %v", err)
	}
}

func TestEnsureReviewReadyIgnoresLegacySyntheticMissingValidationRun(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := state.Task{
		ID:                 "t-review-legacy-validation-row",
		Title:              "review task",
		Priority:           "medium",
		State:              state.TaskStateReview,
		RoleHint:           "implementer",
		ValidationCommands: []string{"python3 -m py_compile dns-benchmark.py"},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	if err := store.InsertValidationRun(state.ValidationRun{
		TaskID:     task.ID,
		Command:    "validation:not-configured",
		ExitCode:   1,
		Status:     "failed",
		StdoutPath: "legacy.stdout.log",
		StderrPath: "legacy.stderr.log",
		Summary:    "task has no validation commands configured",
		CreatedAt:  time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.InsertValidationRun(state.ValidationRun{
		TaskID:     task.ID,
		Command:    "python3 -m py_compile dns-benchmark.py",
		ExitCode:   0,
		Status:     "passed",
		StdoutPath: "validate.stdout.log",
		StderrPath: "validate.stderr.log",
		Summary:    "validation passed",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := EnsureReviewReady(store, task); err != nil {
		t.Fatalf("expected review readiness to ignore legacy synthetic row, got %v", err)
	}
}

func TestEnsureReviewReadyRejectsWhenOnlySyntheticMissingValidationRunExists(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	task := state.Task{
		ID:                 "t-review-only-synthetic",
		Title:              "review task",
		Priority:           "medium",
		State:              state.TaskStateReview,
		RoleHint:           "implementer",
		ValidationCommands: []string{"python3 -m py_compile dns-benchmark.py"},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	if err := store.InsertValidationRun(state.ValidationRun{
		TaskID:     task.ID,
		Command:    "validation:not-configured",
		ExitCode:   1,
		Status:     "failed",
		StdoutPath: "legacy.stdout.log",
		StderrPath: "legacy.stderr.log",
		Summary:    "task has no validation commands configured",
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	err = EnsureReviewReady(store, task)
	if err == nil || !strings.Contains(err.Error(), "review has no validation record") {
		t.Fatalf("expected missing real validation record error, got %v", err)
	}
}

func TestAcceptReviewRequiresMergeBranchPermission(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	task := state.Task{
		ID:        "t-review",
		Title:     "review task",
		Priority:  "high",
		State:     state.TaskStateReview,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}

	executor := Executor{
		Root:   root,
		Store:  store,
		Policy: config.DefaultPolicy("main"),
	}
	executor.Policy.Orchestrator.AllowedActions = []string{"retry_task", "resolve_blocker"}
	_, err = executor.AcceptReview(task.ID, "operator")
	if err == nil || !strings.Contains(err.Error(), "policy denied action \"merge_branch\"") {
		t.Fatalf("expected merge_branch policy denial, got %v", err)
	}
}

func TestAcceptReviewNoopSurfacesBookkeepingFailure(t *testing.T) {
	root, store := testExecutorStore(t)
	initActionGitRepo(t, root)
	writeActionFile(t, filepath.Join(root, "README.md"), "# fixture\n")
	runGitActionTest(t, root, "add", "README.md")
	runGitActionTest(t, root, "commit", "-m", "initial")
	branch := strings.TrimSpace(runGitActionOutput(t, root, "branch", "--show-current"))

	task := reviewReadyTask("t-review-noop", time.Now().UTC())
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, branch, root); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertValidationRun(passedValidationRun(task.ID)); err != nil {
		t.Fatal(err)
	}

	raw := openRawDB(t, root)
	if _, err := raw.Exec(`DROP TABLE events`); err != nil {
		t.Fatal(err)
	}

	executor := Executor{
		Root:   root,
		Store:  store,
		Policy: config.DefaultPolicy(branch),
	}
	_, err := executor.AcceptReview(task.ID, "operator")
	if err == nil {
		t.Fatal("expected no-op review accept to fail")
	}
	if !errors.Is(err, review.ErrNoMergeChanges) {
		t.Fatalf("expected no-op merge error, got %v", err)
	}
	if !strings.Contains(err.Error(), "additionally failed to record review no-op event") {
		t.Fatalf("expected bookkeeping failure in error, got %v", err)
	}
}

func TestResolveBlockerReturnsTargetState(t *testing.T) {
	root, store := testExecutorStore(t)
	now := time.Now().UTC()
	taskID := "t-blocked"
	if err := store.AddTask(state.Task{
		ID:        taskID,
		Title:     "blocked task",
		Priority:  "medium",
		State:     state.TaskStateBlocked,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	raw := openRawDB(t, root)
	if _, err := raw.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?)`, taskID, "needs input", now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	executor := Executor{
		Root:   root,
		Store:  store,
		Policy: config.DefaultPolicy("main"),
	}
	line, err := executor.ResolveBlocker(taskID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if line != "resolved blocker for t-blocked -> todo" {
		t.Fatalf("unexpected resolve output: %q", line)
	}
}

func TestAcceptReviewConflictSurfacesBookkeepingFailure(t *testing.T) {
	root, store := testExecutorStore(t)
	initActionGitRepo(t, root)
	baseBranch := strings.TrimSpace(runGitActionOutput(t, root, "branch", "--show-current"))
	writeActionFile(t, filepath.Join(root, "README.md"), "base\n")
	runGitActionTest(t, root, "add", "README.md")
	runGitActionTest(t, root, "commit", "-m", "base")

	worktreePath := filepath.Join(root, "wt")
	runGitActionTest(t, root, "worktree", "add", "-b", "task/t-review-conflict", worktreePath, "HEAD")
	writeActionFile(t, filepath.Join(worktreePath, "README.md"), "task change\n")
	runGitActionTest(t, worktreePath, "add", "README.md")
	runGitActionTest(t, worktreePath, "commit", "-m", "task change")
	writeActionFile(t, filepath.Join(root, "README.md"), "main change\n")
	runGitActionTest(t, root, "add", "README.md")
	runGitActionTest(t, root, "commit", "-m", "main change")

	task := reviewReadyTask("t-review-conflict", time.Now().UTC())
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTaskRuntime(task.ID, "task/t-review-conflict", worktreePath); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertValidationRun(passedValidationRun(task.ID)); err != nil {
		t.Fatal(err)
	}

	raw := openRawDB(t, root)
	if _, err := raw.Exec(`DROP TABLE reviews`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE events`); err != nil {
		t.Fatal(err)
	}

	executor := Executor{
		Root:   root,
		Store:  store,
		Policy: config.DefaultPolicy(baseBranch),
	}
	_, err := executor.AcceptReview(task.ID, "operator")
	if err == nil {
		t.Fatal("expected merge conflict")
	}
	if !errors.Is(err, review.ErrMergeConflict) {
		t.Fatalf("expected merge conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "additionally failed to record review conflict state") {
		t.Fatalf("expected conflict bookkeeping failure in error, got %v", err)
	}
}

func testExecutorStore(t *testing.T) (string, *state.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}
	return root, store
}

func reviewReadyTask(id string, now time.Time) state.Task {
	return state.Task{
		ID:                 id,
		Title:              "review task",
		Priority:           "high",
		State:              state.TaskStateReview,
		RoleHint:           "implementer",
		ValidationCommands: []string{"true"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func passedValidationRun(taskID string) state.ValidationRun {
	return state.ValidationRun{
		TaskID:     taskID,
		Command:    "true",
		ExitCode:   0,
		Status:     "passed",
		StdoutPath: "stdout.log",
		StderrPath: "stderr.log",
		Summary:    "validation passed",
		CreatedAt:  time.Now().UTC(),
	}
}

func openRawDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", config.DBPath(root))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func initActionGitRepo(t *testing.T, root string) {
	t.Helper()
	runGitActionTest(t, root, "init")
	runGitActionTest(t, root, "config", "user.name", "Test User")
	runGitActionTest(t, root, "config", "user.email", "test@example.com")
}

func runGitActionTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func runGitActionOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeActionFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
