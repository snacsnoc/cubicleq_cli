package orchestrator

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
	"github.com/snacsnoc/cubicleq_cli/internal/validation"
)

func TestReconcileLaunchingAliveButSilentBlocksTask(t *testing.T) {
	root, store := testStore(t)
	restore := setRuntimeTimeouts(50*time.Millisecond, 100*time.Millisecond)
	defer restore()

	task := testTask("t-launch-silent", state.TaskStateReady)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-lc", "exec sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killUnixProcessTree(cmd.Process.Pid) })

	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "launching",
		PID:           cmd.Process.Pid,
		LastHeartbeat: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.reconcile(); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateBlocked || got.BlockedReason != "worker failed to report after launch" {
		t.Fatalf("expected blocked launch silence, got state=%s reason=%q", got.State, got.BlockedReason)
	}
	assertNoActiveRuntime(t, store, task.ID)
	assertLatestEvent(t, store, task.ID, "worker_launch_silence")
}

func TestReconcileLaunchingExitedFailsTask(t *testing.T) {
	root, store := testStore(t)
	restore := setRuntimeTimeouts(50*time.Millisecond, 100*time.Millisecond)
	defer restore()

	task := testTask("t-launch-exit", state.TaskStateReady)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-lc", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "launching",
		PID:           cmd.Process.Pid,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.reconcile(); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateFailed || got.BlockedReason != "worker exited before claiming task" {
		t.Fatalf("expected failed exited-before-claim, got state=%s reason=%q", got.State, got.BlockedReason)
	}
	assertNoActiveRuntime(t, store, task.ID)
	assertLatestEvent(t, store, task.ID, "worker_exit_before_claim")
}

func TestReconcileRunningAliveWithStaleHeartbeatBlocksTask(t *testing.T) {
	root, store := testStore(t)
	restore := setRuntimeTimeouts(50*time.Millisecond, 100*time.Millisecond)
	defer restore()

	task := testTask("t-stale-heartbeat", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-lc", "exec sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killUnixProcessTree(cmd.Process.Pid) })

	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           cmd.Process.Pid,
		LastHeartbeat: time.Now().UTC().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.reconcile(); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateBlocked || got.BlockedReason != "stale heartbeat" {
		t.Fatalf("expected blocked stale heartbeat, got state=%s reason=%q", got.State, got.BlockedReason)
	}
	assertNoActiveRuntime(t, store, task.ID)
	assertLatestEvent(t, store, task.ID, "stale_heartbeat")
}

func TestReconcileRunningExitedFailsTask(t *testing.T) {
	root, store := testStore(t)
	restore := setRuntimeTimeouts(50*time.Millisecond, 100*time.Millisecond)
	defer restore()

	task := testTask("t-running-exit", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-lc", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           cmd.Process.Pid,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.reconcile(); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateFailed || got.BlockedReason != "worker exited without reporting completion" {
		t.Fatalf("expected failed exited-after-claim, got state=%s reason=%q", got.State, got.BlockedReason)
	}
	assertNoActiveRuntime(t, store, task.ID)
	assertLatestEvent(t, store, task.ID, "worker_exit_after_claim")
}

func TestTrackProcessReapsExitedChild(t *testing.T) {
	root, store := testStore(t)
	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)

	cmd := exec.Command("bash", "-lc", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	orch.trackProcess("t-reap", cmd)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exited, err := orch.processExited("t-reap")
		if err != nil {
			t.Fatal(err)
		}
		if exited {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected exited child to be observed through tracked wait")
}

func TestFinalizeForgetsTrackedProcessOnSuccess(t *testing.T) {
	root, store := testStore(t)
	initGitRepo(t, root)

	task := testTask("t-finalize-success", state.TaskStateRunning)
	task.WorktreePath = root
	task.BranchName = currentBranch(t, root)
	task.ValidationCommands = []string{"true"}
	task.CompletionSummary = "done"
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    task.BranchName,
		WorktreePath:  task.WorktreePath,
		SessionID:     task.ID + "-session",
		Status:        "completed",
		PID:           1,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	cmd := exec.Command("bash", "-lc", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	orch.trackProcess(task.ID, cmd)

	if err := orch.finalize(task.ID); err != nil {
		t.Fatal(err)
	}

	orch.mu.Lock()
	_, exists := orch.procs[task.ID]
	orch.mu.Unlock()
	if exists {
		t.Fatalf("expected tracked process for %s to be forgotten after successful finalize", task.ID)
	}
}

func TestFinalizeWithoutValidationCommandsSkipsAndMovesToReview(t *testing.T) {
	root, store := testStore(t)
	initGitRepo(t, root)

	task := testTask("t-finalize-skip-validation", state.TaskStateRunning)
	task.WorktreePath = root
	task.BranchName = currentBranch(t, root)
	task.CompletionSummary = "done"
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    task.BranchName,
		WorktreePath:  task.WorktreePath,
		SessionID:     task.ID + "-session",
		Status:        "completed",
		PID:           1,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.finalize(task.ID); err != nil {
		t.Fatal(err)
	}

	gotTask, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != state.TaskStateReview {
		t.Fatalf("expected task to move to review, got %s", gotTask.State)
	}

	runs, err := store.ListValidationRuns(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one skipped validation run, got %d", len(runs))
	}
	if runs[0].Command != "validation:not-configured" || runs[0].Status != "skipped" || runs[0].ExitCode != 0 {
		t.Fatalf("unexpected skipped validation run: %#v", runs[0])
	}

	assertLatestEvent(t, store, task.ID, "validation_skipped")
}

func TestFinalizeTimedOutValidationFailsTaskWithoutReviewArtifacts(t *testing.T) {
	root, store := testStore(t)

	worktree := t.TempDir()
	task := testTask("t-finalize-timeout", state.TaskStateRunning)
	task.WorktreePath = worktree
	task.BranchName = "task/" + task.ID
	task.ValidationCommands = []string{"printf before-out; printf before-err >&2; sleep 5"}
	task.CompletionSummary = "done"
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    task.BranchName,
		WorktreePath:  task.WorktreePath,
		SessionID:     task.ID + "-session",
		Status:        "completed",
		PID:           1,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	restoreValidationTimeout := validation.SetCommandTimeoutForTest(50 * time.Millisecond)
	defer restoreValidationTimeout()

	orch := New(root, "", config.Config{MaxParallelTasks: 2, WorktreeDir: filepath.Join(root, "worktrees")}, store)
	if err := orch.finalize(task.ID); err != nil {
		t.Fatal(err)
	}

	gotTask, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotTask.State != state.TaskStateFailed {
		t.Fatalf("expected task to fail after validation timeout, got %s", gotTask.State)
	}
	if gotTask.BlockedReason != "validation failed: printf before-out; printf before-err >&2; sleep 5" {
		t.Fatalf("unexpected failure reason: %q", gotTask.BlockedReason)
	}
	assertNoActiveRuntime(t, store, task.ID)

	runs, err := store.ListValidationRuns(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one validation run, got %d", len(runs))
	}
	if runs[0].Status != "failed" || runs[0].ExitCode != 124 {
		t.Fatalf("unexpected validation timeout run: %#v", runs[0])
	}
	if runs[0].Summary != "validation timed out after 50ms" {
		t.Fatalf("unexpected validation timeout summary: %q", runs[0].Summary)
	}

	reviews, err := store.ListReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no review rows after validation timeout, got %#v", reviews)
	}

	artifacts, err := store.ListTaskArtifacts(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no review artifacts after validation timeout, got %#v", artifacts)
	}
}

func TestStopForcefulRequeuesRunningAndReadyTasks(t *testing.T) {
	root, store := testStore(t)

	running := testTask("t-stop-force-running", state.TaskStateRunning)
	ready := testTask("t-stop-force-ready", state.TaskStateReady)
	if err := store.AddTask(running); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTask(ready); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        running.ID,
		BranchName:    "task/" + running.ID,
		WorktreePath:  filepath.Join(root, "worktrees", running.ID),
		SessionID:     running.ID + "-session",
		Status:        "running",
		PID:           0,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        ready.ID,
		BranchName:    "task/" + ready.ID,
		WorktreePath:  filepath.Join(root, "worktrees", ready.ID),
		SessionID:     ready.ID + "-session",
		Status:        "launching",
		PID:           0,
		LastHeartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := Stop(store, false); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{running.ID, ready.ID} {
		task, err := store.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if task.State != state.TaskStateTodo {
			t.Fatalf("expected %s to be todo after force stop, got %s", id, task.State)
		}
		assertNoActiveRuntime(t, store, id)
		assertLatestEvent(t, store, id, "interrupted")
	}
}

func TestStopGracefulRecordsStoppedAndMarksStopRequested(t *testing.T) {
	root, store := testStore(t)

	task := testTask("t-stop-graceful", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           0,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := Stop(store, true); err != nil {
		t.Fatal(err)
	}

	task, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != state.TaskStateTodo {
		t.Fatalf("expected task to be todo after graceful stop, got %s", task.State)
	}
	assertNoActiveRuntime(t, store, task.ID)
	assertLatestEvent(t, store, task.ID, "stopped")
	value, err := store.GetSetting(stopRequestedKey)
	if err != nil {
		t.Fatal(err)
	}
	if value != "graceful" {
		t.Fatalf("expected %q setting to be graceful, got %q", stopRequestedKey, value)
	}
}

func TestStopGracefulIgnoresCompletedRuntimes(t *testing.T) {
	root, store := testStore(t)

	task := testTask("t-stop-completed", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "completed",
		PID:           0,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := Stop(store, true); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateRunning {
		t.Fatalf("expected task to remain running with completed runtime, got %s", got.State)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].Status != "completed" {
		t.Fatalf("expected completed runtime to remain untouched, got %#v", runtimes)
	}
	value, err := store.GetSetting(stopRequestedKey)
	if err != nil {
		t.Fatal(err)
	}
	if value != "" {
		t.Fatalf("expected no graceful stop request when only completed runtimes exist, got %q", value)
	}
}

func TestStopReturnsRetryTaskError(t *testing.T) {
	root, store := testStore(t)

	task := testTask("t-stop-retry-error", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           0,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	triggerDB := openRawDB(t, root)
	defer triggerDB.Close()
	createFailTrigger(t, triggerDB, "retry_fail_trigger", fmt.Sprintf(`
		CREATE TRIGGER retry_fail_trigger
		BEFORE UPDATE OF state ON tasks
		WHEN NEW.id = %q AND NEW.state = 'todo'
		BEGIN
			SELECT RAISE(FAIL, 'forced retry failure');
		END;
	`, task.ID))

	err := Stop(store, false)
	if err == nil || !strings.Contains(err.Error(), "forced retry failure") {
		t.Fatalf("expected forced retry failure, got %v", err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateRunning {
		t.Fatalf("expected task state to remain running after retry failure, got %s", got.State)
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].TaskID != task.ID {
		t.Fatalf("expected runtime row for %s to remain after retry failure, got %#v", task.ID, runtimes)
	}
}

func TestStopReturnsRecordEventError(t *testing.T) {
	root, store := testStore(t)

	task := testTask("t-stop-event-error", state.TaskStateRunning)
	if err := store.AddTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertRuntime(state.Runtime{
		TaskID:        task.ID,
		BranchName:    "task/" + task.ID,
		WorktreePath:  filepath.Join(root, "worktrees", task.ID),
		SessionID:     task.ID + "-session",
		Status:        "running",
		PID:           0,
		LastHeartbeat: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	triggerDB := openRawDB(t, root)
	defer triggerDB.Close()
	createFailTrigger(t, triggerDB, "record_event_fail_trigger", fmt.Sprintf(`
		CREATE TRIGGER record_event_fail_trigger
		BEFORE INSERT ON events
		WHEN NEW.task_id = %q
		BEGIN
			SELECT RAISE(FAIL, 'forced event failure');
		END;
	`, task.ID))

	err := Stop(store, false)
	if err == nil || !strings.Contains(err.Error(), "forced event failure") {
		t.Fatalf("expected forced event failure, got %v", err)
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.TaskStateTodo {
		t.Fatalf("expected task to be retried before event failure, got %s", got.State)
	}
	assertNoActiveRuntime(t, store, task.ID)
	events, err := store.ListEvents(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events when RecordEvent fails, got %#v", events)
	}
}

func testStore(t *testing.T) (string, *state.Store) {
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

func testTask(id, taskState string) state.Task {
	now := time.Now().UTC()
	return state.Task{
		ID:        id,
		Title:     id,
		Priority:  "medium",
		State:     taskState,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func setRuntimeTimeouts(launch, stale time.Duration) func() {
	oldLaunch := launchSilenceTimeout
	oldStale := staleHeartbeatTimeout
	launchSilenceTimeout = launch
	staleHeartbeatTimeout = stale
	return func() {
		launchSilenceTimeout = oldLaunch
		staleHeartbeatTimeout = oldStale
	}
}

func assertNoActiveRuntime(t *testing.T, store *state.Store, taskID string) {
	t.Helper()
	runtimes, err := store.ListRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range runtimes {
		if runtime.TaskID == taskID {
			t.Fatalf("expected no runtime row for %s, got %#v", taskID, runtime)
		}
	}
}

func assertLatestEvent(t *testing.T, store *state.Store, taskID, eventType string) {
	t.Helper()
	events, err := store.ListEvents(taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("expected events for %s", taskID)
	}
	if events[0].Type != eventType {
		t.Fatalf("expected latest event %s, got %s", eventType, events[0].Type)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGitTest(t, dir, "init")
	runGitTest(t, dir, "config", "user.name", "Test User")
	runGitTest(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "README.md")
	runGitTest(t, dir, "commit", "-m", "initial")
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v\n%s", err, string(out))
	}
	return string(bytesTrimSpace(out))
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func openRawDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", config.DBPath(root)))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func createFailTrigger(t *testing.T, db *sql.DB, triggerName, ddl string) {
	t.Helper()
	if _, err := db.Exec("DROP TRIGGER IF EXISTS " + triggerName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
}

func bytesTrimSpace(v []byte) []byte {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\n' || v[0] == '\t' || v[0] == '\r') {
		v = v[1:]
	}
	for len(v) > 0 {
		last := v[len(v)-1]
		if last != ' ' && last != '\n' && last != '\t' && last != '\r' {
			break
		}
		v = v[:len(v)-1]
	}
	return v
}
