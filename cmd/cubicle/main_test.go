package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easto/cubicle-dev-flow-aut/internal/state"
)

func TestBuildStatusRecommendationsNoTasks(t *testing.T) {
	recommendations := buildStatusRecommendations(nil, nil, nil, nil, false)
	if len(recommendations) != 1 {
		t.Fatalf("expected one recommendation, got %d", len(recommendations))
	}
	if got := recommendations[0].Commands[0].Value; got != `cubicle tasks add --title "..." --validate "..."` {
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
	if got := recommendations[0].Commands[0].Value; got != "cubicle logs t-1" {
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
	if got := recommendations[0].Commands[0].Value; got != "cubicle run" {
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
			if command.Value == "cubicle run" {
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
			if command.Value == "cubicle run" {
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
			if command.Value == "cubicle run" {
				t.Fatalf("unexpected run recommendation without runnable work: %#v", recommendations)
			}
		}
	}
}

func TestRunReviewWithoutArgsPrintsCompactHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runReview("", nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicle review list") || !strings.Contains(out, "cubicle review accept <task-id>") {
		t.Fatalf("expected review help output, got:\n%s", out)
	}
}

func TestRunBlockersWithoutArgsPrintsCompactHelp(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runBlockers("", nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cubicle blockers list") || !strings.Contains(out, "cubicle blockers resolve <task-id>") {
		t.Fatalf("expected blockers help output, got:\n%s", out)
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

func TestRunTasksReadyRejectsUnsatisfiedDependencies(t *testing.T) {
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
	for _, task := range []state.Task{
		{ID: "dep", Title: "dep", Priority: "medium", State: state.TaskStateTodo, RoleHint: "implementer", CreatedAt: now, UpdatedAt: now},
		{ID: "child", Title: "child", Priority: "medium", State: state.TaskStateTodo, RoleHint: "implementer", Dependencies: []string{"dep"}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}

	err = runTasks(root, []string{"ready", "child"})
	if err == nil || !strings.Contains(err.Error(), "unsatisfied dependencies") {
		t.Fatalf("expected unsatisfied dependency error, got %v", err)
	}

	task, err := store.GetTask("child")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != state.TaskStateTodo {
		t.Fatalf("expected child to remain todo, got %s", task.State)
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

func testStore(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicle"), 0o755); err != nil {
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
