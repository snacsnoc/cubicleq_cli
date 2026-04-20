package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestBuildStatusRecommendationsNoTasks(t *testing.T) {
	recommendations := buildStatusRecommendations(nil, nil, nil, nil, false)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if got := recommendations[0].Commands[0].Value; got != `cubicleq tasks add --title "..."` {
		t.Fatalf("unexpected add-task recommendation: %s", got)
	}
}

func TestBuildStatusRecommendationsGenericBlockedTaskInspectsLogs(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-1",
		Title: "blocked",
		State: state.TaskStateBlocked,
	}}
	blockers := []state.Blocker{{
		TaskID: "t-1",
		Reason: "needs input",
	}}

	recommendations := buildStatusRecommendations(tasks, blockers, nil, nil, false)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if got := recommendations[0].Commands[0].Value; got != "cubicleq logs t-1" {
		t.Fatalf("unexpected inspect command: %s", got)
	}
}

func TestBuildStatusRecommendationsPrioritizesBlockedBeforeReview(t *testing.T) {
	tasks := []state.Task{
		{ID: "t-1", Title: "blocked", State: state.TaskStateBlocked},
		{ID: "t-2", Title: "review", State: state.TaskStateReview},
	}
	blockers := []state.Blocker{{
		TaskID: "t-1",
		Reason: "needs input",
	}}
	reviews := []state.Review{{
		TaskID: "t-2",
	}}

	recommendations := buildStatusRecommendations(tasks, blockers, reviews, nil, false)
	if len(recommendations) != 2 {
		t.Fatalf("expected two recommendations, got %d", len(recommendations))
	}
	if recommendations[0].Kind != "blocked" || recommendations[0].TaskID != "t-1" {
		t.Fatalf("expected blocked recommendation first, got %#v", recommendations)
	}
	if recommendations[1].Kind != "review" || recommendations[1].TaskID != "t-2" {
		t.Fatalf("expected review recommendation second, got %#v", recommendations)
	}
}

func TestBuildStatusRecommendationsSuggestsRunForRunnableTodoWithoutRuntime(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-1",
		Title: "todo",
		State: state.TaskStateTodo,
	}}

	recommendations := buildStatusRecommendations(tasks, nil, nil, nil, true)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if got := recommendations[0].Commands[0].Value; got != "cubicleq run" {
		t.Fatalf("unexpected run command: %s", got)
	}
}

func TestBuildStatusRecommendationsSuppressesRunWhenRuntimeIsActive(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-1",
		Title: "ready",
		State: state.TaskStateReady,
	}}
	runtimes := []state.Runtime{{
		TaskID:        "t-1",
		Status:        "running",
		LastHeartbeat: time.Now().UTC(),
	}}

	recommendations := buildStatusRecommendations(tasks, nil, nil, runtimes, true)
	for _, recommendation := range recommendations {
		for _, command := range recommendation.Commands {
			if command.Value == "cubicleq run" {
				t.Fatalf("unexpected run recommendation while runtime is active: %#v", recommendations)
			}
		}
	}
}

func TestBuildStatusRecommendationsSuggestsRunWhenRuntimeIsCompleted(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-1",
		Title: "ready",
		State: state.TaskStateReady,
	}}
	runtimes := []state.Runtime{{
		TaskID:        "t-1",
		Status:        "completed",
		LastHeartbeat: time.Now().UTC(),
	}}

	recommendations := buildStatusRecommendations(tasks, nil, nil, runtimes, true)
	foundRun := false
	for _, recommendation := range recommendations {
		for _, command := range recommendation.Commands {
			if command.Value == "cubicleq run" {
				foundRun = true
			}
		}
	}
	if !foundRun {
		t.Fatalf("expected run recommendation with completed runtime: %#v", recommendations)
	}
}

func TestBuildStatusRecommendationsSuppressesRunWhenNoRunnableWork(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-1",
		Title: "todo",
		State: state.TaskStateTodo,
	}}

	recommendations := buildStatusRecommendations(tasks, nil, nil, nil, false)
	for _, recommendation := range recommendations {
		for _, command := range recommendation.Commands {
			if command.Value == "cubicleq run" {
				t.Fatalf("unexpected run recommendation without runnable work: %#v", recommendations)
			}
		}
	}
}

func TestBuildStatusRecommendationsAddsFailedTaskActions(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-fail-1",
		Title: "failed task",
		State: state.TaskStateFailed,
	}}

	recommendations := buildStatusRecommendations(tasks, nil, nil, nil, false)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if recommendations[0].Kind != "failed" || recommendations[0].TaskID != "t-fail-1" {
		t.Fatalf("expected failed recommendation for t-fail-1, got %#v", recommendations[0])
	}
	if len(recommendations[0].Commands) != 2 {
		t.Fatalf("expected failed recommendation commands, got %#v", recommendations[0].Commands)
	}
	if recommendations[0].Commands[0].Value != "cubicleq logs t-fail-1" {
		t.Fatalf("unexpected failed inspect command: %#v", recommendations[0].Commands[0])
	}
	if recommendations[0].Commands[1].Value != "cubicleq retry t-fail-1" {
		t.Fatalf("unexpected failed retry command: %#v", recommendations[0].Commands[1])
	}
}

func TestRunReviewWithoutArgsPrintsCompactHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runReview("", nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicleq review list") || !strings.Contains(out, "cubicleq review accept <task-id>") {
		t.Fatalf("expected review help output, got:\n%s", out)
	}
}

func TestRunReviewHelpFlagPrintsCompactHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runReview("", []string{"--help"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicleq review list") || !strings.Contains(out, "cubicleq review reject <task-id> [--note \"...\"]") {
		t.Fatalf("expected review help output, got:\n%s", out)
	}
}

func TestRunRetryHelpFlagPrintsUsage(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runRetry("", []string{"--help"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicleq retry <task-id>") {
		t.Fatalf("expected retry usage output, got:\n%s", out)
	}
}

func TestRunReviewRejectMissingTaskUsesOptionalNoteSyntax(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitSchema(); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_ = store.Close()

	err = runReview(root, []string{"reject"})
	if err == nil {
		t.Fatal("expected reject usage error")
	}
	if !strings.Contains(err.Error(), `review reject <task-id> [--note "..."]`) {
		t.Fatalf("expected optional-note reject usage, got %v", err)
	}
}

func TestRunBlockersWithoutArgsPrintsCompactHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runBlockers("", nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicleq blockers list") || !strings.Contains(out, "cubicleq blockers resolve <task-id>") {
		t.Fatalf("expected blockers help output, got:\n%s", out)
	}
}

func TestBuildStatusRecommendationsReviewRejectSuggestionUsesOptionalNote(t *testing.T) {
	tasks := []state.Task{{
		ID:    "t-2",
		Title: "review",
		State: state.TaskStateReview,
	}}
	reviews := []state.Review{{
		TaskID: "t-2",
	}}

	recommendations := buildStatusRecommendations(tasks, nil, reviews, nil, false)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if len(recommendations[0].Commands) != 2 {
		t.Fatalf("expected review recommendation commands, got %#v", recommendations[0].Commands)
	}
	got := recommendations[0].Commands[1].Value
	want := `cubicleq review reject t-2 [--note "..."]`
	if got != want {
		t.Fatalf("expected reject recommendation %q, got %q", want, got)
	}
}

func TestResolveTaskIDArgPrefersExactMatch(t *testing.T) {
	store := testStore(t)
	addTestTask(t, store, "t-1")
	addTestTask(t, store, "t-123")

	got, err := resolveTaskIDArg(store, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "t-1" {
		t.Fatalf("expected exact match t-1, got %s", got)
	}
}

func TestResolveTaskIDArgResolvesUniquePrefix(t *testing.T) {
	store := testStore(t)
	addTestTask(t, store, "t-123456")

	got, err := resolveTaskIDArg(store, "t-123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "t-123456" {
		t.Fatalf("expected prefix to resolve to t-123456, got %s", got)
	}
}

func TestResolveTaskIDArgRejectsUnknownPrefix(t *testing.T) {
	store := testStore(t)
	addTestTask(t, store, "t-123456")

	_, err := resolveTaskIDArg(store, "t-999")
	if err == nil || !strings.Contains(err.Error(), "unknown task id or prefix: t-999") {
		t.Fatalf("expected unknown prefix error, got %v", err)
	}
}

func TestResolveTaskIDArgRejectsAmbiguousPrefix(t *testing.T) {
	store := testStore(t)
	addTestTask(t, store, "t-123456")
	addTestTask(t, store, "t-123999")

	_, err := resolveTaskIDArg(store, "t-123")
	if err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if !strings.Contains(err.Error(), `ambiguous task id prefix "t-123"`) {
		t.Fatalf("expected ambiguous prefix error, got %v", err)
	}
	if !strings.Contains(err.Error(), "t-123456") || !strings.Contains(err.Error(), "t-123999") {
		t.Fatalf("expected matching task ids in ambiguity error, got %v", err)
	}
}

func TestParseDependencyCSVRejectsEmptyEntries(t *testing.T) {
	if _, err := parseDependencyCSV("t-1, ,t-2"); err == nil || !strings.Contains(err.Error(), "dependency ids must not be empty") {
		t.Fatalf("expected empty dependency parse failure, got %v", err)
	}
}

func TestParseDependencyCSVAllowsExplicitClear(t *testing.T) {
	got, err := parseDependencyCSV("   ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty dependency list for explicit clear, got %v", got)
	}
}

func TestWorkingRootUsesCurrentDirectoryByDefault(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	got, err := workingRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("expected working directory root %q, got %q", root, got)
	}
}

func TestWorkingRootIgnoresCubicleRootEnv(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	other := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	oldRootEnv := os.Getenv("CUBICLE_ROOT")
	if err := os.Setenv("CUBICLE_ROOT", other); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if oldRootEnv == "" {
			_ = os.Unsetenv("CUBICLE_ROOT")
			return
		}
		_ = os.Setenv("CUBICLE_ROOT", oldRootEnv)
	}()

	got, err := workingRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("expected CUBICLE_ROOT to be ignored and root %q to win, got %q", root, got)
	}
}

func TestWorkingRootUsesRootOverride(t *testing.T) {
	root := t.TempDir()

	got, err := workingRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("expected explicit root override %q, got %q", root, got)
	}
}

func TestRunStopIgnoresCompletedRuntimes(t *testing.T) {
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
	if err := store.AddTask(state.Task{
		ID:        "t-1",
		Title:     "task",
		Priority:  "medium",
		State:     state.TaskStateRunning,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        "t-1",
		BranchName:    "task/t-1",
		WorktreePath:  filepath.Join(root, "worktrees", "t-1"),
		SessionID:     "t-1-session",
		Status:        "completed",
		PID:           0,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runStop(root); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no active workers") {
		t.Fatalf("expected completed-only stop to report no active workers, got:\n%s", out)
	}

	got, err := store.GetTask("t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateRunning {
		t.Fatalf("expected task to remain running, got %s", got.State)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].Status != "completed" {
		t.Fatalf("expected completed runtime to remain, got %#v", runtimes)
	}
}

func TestRunTasksSetValidationParsesTaskIDBeforeFlags(t *testing.T) {
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

	taskID := "t-1"
	now := time.Now().UTC()
	if err := store.AddTask(state.Task{
		ID:        taskID,
		Title:     "task",
		Priority:  "medium",
		State:     state.TaskStateTodo,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := runTasks(root, []string{"set-validation", taskID, "--validate", "python3 -m py_compile dns-benchmark.py"}); err != nil {
		t.Fatalf("expected set-validation to accept task-id before flags, got %v", err)
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.ValidationCommands) != 1 || task.ValidationCommands[0] != "python3 -m py_compile dns-benchmark.py" {
		t.Fatalf("unexpected validation commands: %#v", task.ValidationCommands)
	}
}

func TestRunCleanupPrintsSuccessMessage(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Default(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteDefault(root, cfg); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runCleanup(root); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cleanup complete") {
		t.Fatalf("expected cleanup confirmation output, got:\n%s", out)
	}
}

func testStore(t *testing.T) *state.Store {
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
	return store
}

func addTestTask(t *testing.T, store *state.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.AddTask(state.Task{
		ID:        id,
		Title:     id,
		Priority:  "medium",
		State:     state.TaskStateTodo,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
