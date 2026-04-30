package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/agents"
	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/prompting"
	"github.com/snacsnoc/cubicleq_cli/internal/reporting"
	"github.com/snacsnoc/cubicleq_cli/internal/review"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
	"github.com/snacsnoc/cubicleq_cli/internal/validation"
	"github.com/snacsnoc/cubicleq_cli/internal/worktree"
)

type Orchestrator struct {
	root    string
	binPath string
	cfg     config.Config
	store   *state.Store
	report  *reporting.Server
	adapter agents.Adapter
	mu      sync.Mutex
	procs   map[string]chan error
}

const stopRequestedKey = "orchestrator.stop_requested"

const (
	DefaultLaunchSilenceTimeout  = 60 * time.Second
	DefaultStaleHeartbeatTimeout = 2 * time.Minute
)

var (
	launchSilenceTimeout  = DefaultLaunchSilenceTimeout
	staleHeartbeatTimeout = DefaultStaleHeartbeatTimeout
)

func New(root, binPath string, cfg config.Config, store *state.Store) *Orchestrator {
	return &Orchestrator{
		root:    root,
		binPath: binPath,
		cfg:     cfg,
		store:   store,
		report:  reporting.NewServer(store),
		adapter: agents.New(cfg.Backend),
		procs:   make(map[string]chan error),
	}
}

func ts() string {
	return time.Now().UTC().Format("[15:04:05]")
}

func (o *Orchestrator) Run(ctx context.Context) error {
	fmt.Printf("%s starting cubicleq orchestrator in %s\n", ts(), o.root)
	if err := o.store.DeleteSetting(stopRequestedKey); err != nil {
		return err
	}
	if err := o.report.Start(); err != nil {
		return err
	}
	fmt.Printf("%s mcp server listening on %s\n", ts(), o.report.URL())
	fmt.Printf("%s run is attached to this terminal; use another terminal with 'cubicleq status' to inspect progress\n", ts())
	fmt.Printf("%s inspect worker output with 'cubicleq logs <task-id>'\n", ts())
	defer o.report.Shutdown(context.Background())

	if err := o.reconcile(); err != nil {
		return err
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		stopRequested, err := o.consumeStopRequest()
		if err != nil {
			return err
		}
		if stopRequested {
			fmt.Printf("%s stop requested, shutting down orchestrator\n", ts())
			return nil
		}
		if err := o.tick(); err != nil {
			return err
		}
		done, err := o.idle()
		if err != nil {
			return err
		}
		if done {
			if err := o.printRunHandoff(); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return o.handleInterrupt()
		case <-ticker.C:
		}
	}
}

func (o *Orchestrator) tick() error {
	if err := o.store.PromoteReadyTasks(); err != nil {
		return err
	}
	if err := o.reconcile(); err != nil {
		return err
	}
	active, err := o.store.ListActiveRuntimes()
	if err != nil {
		return err
	}
	runningCount := 0
	for _, runtime := range active {
		if runtime.Status == "launching" || runtime.Status == "running" {
			runningCount++
		} else if runtime.Status == "completed" {
			if err := o.finalize(runtime.TaskID); err != nil {
				return err
			}
		}
	}

	slots := o.cfg.MaxParallelTasks - runningCount
	if slots <= 0 {
		return nil
	}
	ready, err := o.store.ListReadyTasks(slots)
	if err != nil {
		return err
	}
	for _, task := range ready {
		if err := o.launch(task); err != nil {
			return err
		}
	}
	return nil
}

func (o *Orchestrator) launch(task state.Task) error {
	branch := worktree.BranchName(task.ID, task.Title)
	wt, err := worktree.Ensure(o.root, o.cfg.WorktreeDir, branch, task.ID)
	if err != nil {
		return err
	}
	if err := o.store.MarkTaskRuntime(task.ID, branch, wt); err != nil {
		return err
	}
	task, err = o.store.GetTask(task.ID)
	if err != nil {
		return err
	}
	bundle, err := prompting.WriteBundle(o.root, task)
	if err != nil {
		return err
	}
	runtime := agents.NewRuntime(task, branch, wt)
	if err := o.store.UpsertRuntime(runtime); err != nil {
		return err
	}
	cmd, err := o.adapter.Launch(agents.LaunchSpec{
		Root:       o.root,
		BinPath:    o.binPath,
		Task:       task,
		Runtime:    runtime,
		PromptPath: bundle.PromptPath,
		MCPURL:     o.report.URL(),
	})
	if err != nil {
		_ = o.store.DeleteRuntime(task.ID)
		return err
	}
	runtime.PID = cmd.Process.Pid
	runtime.LastHeartbeat = time.Now().UTC()
	fmt.Printf("%s launched %s pid=%d in %s on %s\n", ts(), task.ID, runtime.PID, wt, branch)
	o.trackProcess(task.ID, cmd)
	if err := o.store.UpsertRuntime(runtime); err != nil {
		return err
	}
	if err := o.store.RecordEvent(task.ID, "launch", map[string]any{"pid": runtime.PID, "branch": branch, "worktree": wt}); err != nil {
		return err
	}
	return nil
}

func (o *Orchestrator) finalize(taskID string) error {
	fmt.Printf("%s finalizing %s\n", ts(), taskID)
	defer o.forgetProcess(taskID)
	task, err := o.store.GetTask(taskID)
	if err != nil {
		return err
	}
	if len(task.ValidationCommands) == 0 {
		run, err := validation.MissingConfigRun(o.root, task)
		if err != nil {
			return err
		}
		if err := o.store.InsertValidationRun(run); err != nil {
			return err
		}
		if err := o.store.RecordEvent(taskID, "validation_skipped", map[string]any{
			"command": run.Command,
			"status":  run.Status,
			"reason":  run.Summary,
		}); err != nil {
			return err
		}
		fmt.Printf("%s validation skipped for %s: %s\n", ts(), taskID, run.Summary)
		task, err = o.store.GetTask(taskID)
		if err != nil {
			return err
		}
		artifacts, err := review.Write(o.root, task, []state.ValidationRun{run})
		if err != nil {
			return err
		}
		fmt.Printf("%s task %s is review-ready\n", ts(), taskID)
		o.printReviewNext(taskID)
		return o.store.FinalizeReview(taskID, artifacts.SummaryPath, artifacts.DiffPath)
	}
	runs, err := validation.Run(o.root, task)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if err := o.store.InsertValidationRun(run); err != nil {
			return err
		}
		if run.Status != "passed" {
			fmt.Printf("%s validation failed for %s: %s\n", ts(), taskID, run.Command)
			if err := o.store.FailTask(taskID, fmt.Sprintf("validation failed: %s", run.Command)); err != nil {
				return err
			}
			o.printFailedNext(taskID)
			return nil
		}
	}
	task, err = o.store.GetTask(taskID)
	if err != nil {
		return err
	}
	artifacts, err := review.Write(o.root, task, runs)
	if err != nil {
		return err
	}
	fmt.Printf("%s task %s is review-ready\n", ts(), taskID)
	o.printReviewNext(taskID)
	return o.store.FinalizeReview(taskID, artifacts.SummaryPath, artifacts.DiffPath)
}

func (o *Orchestrator) reconcile() error {
	runtimes, err := o.store.ListActiveRuntimes()
	if err != nil {
		return err
	}
	for _, runtime := range runtimes {
		if err := o.reconcileRuntime(runtime); err != nil {
			return err
		}
	}
	return nil
}

func (o *Orchestrator) reconcileRuntime(runtime state.Runtime) error {
	task, err := o.store.GetTask(runtime.TaskID)
	if err != nil {
		return err
	}
	now := time.Now()

	switch runtime.Status {
	case "launching":
		exited, _ := o.processExited(runtime.TaskID)
		if exited || !processAlive(runtime.PID) {
			if task.State == state.TaskStateReady {
				fmt.Printf("%s task %s failed: worker exited before claiming task\n", ts(), runtime.TaskID)
				return o.failRuntime(runtime, "worker_exit_before_claim", "worker exited before claiming task")
			}
			return nil
		}
		if task.State == state.TaskStateReady && now.Sub(runtime.LastHeartbeat) > launchSilenceTimeout {
			fmt.Printf("%s task %s blocked: worker failed to report after launch\n", ts(), runtime.TaskID)
			return o.blockRuntime(runtime, "worker_launch_silence", "worker failed to report after launch")
		}
	case "running":
		exited, _ := o.processExited(runtime.TaskID)
		if exited || !processAlive(runtime.PID) {
			if task.State == state.TaskStateRunning {
				fmt.Printf("%s task %s failed: worker exited without reporting completion\n", ts(), runtime.TaskID)
				return o.failRuntime(runtime, "worker_exit_after_claim", "worker exited without reporting completion")
			}
			return nil
		}
		if task.State == state.TaskStateRunning && now.Sub(runtime.LastHeartbeat) > staleHeartbeatTimeout {
			fmt.Printf("%s task %s blocked: stale heartbeat\n", ts(), runtime.TaskID)
			return o.blockRuntime(runtime, "stale_heartbeat", "stale heartbeat")
		}
	}
	return nil
}

func (o *Orchestrator) failRuntime(runtime state.Runtime, eventType, reason string) error {
	o.forgetProcess(runtime.TaskID)
	if err := o.store.RecordEvent(runtime.TaskID, eventType, runtimeEventPayload(runtime)); err != nil {
		return err
	}
	if err := o.store.FailTask(runtime.TaskID, reason); err != nil {
		return err
	}
	o.printFailedNext(runtime.TaskID)
	return nil
}

func (o *Orchestrator) blockRuntime(runtime state.Runtime, eventType, reason string) error {
	if processAlive(runtime.PID) {
		if err := killUnixProcessTree(runtime.PID); err != nil {
			return err
		}
	}
	o.forgetProcess(runtime.TaskID)
	if err := o.store.RecordEvent(runtime.TaskID, eventType, runtimeEventPayload(runtime)); err != nil {
		return err
	}
	if err := o.store.ReleaseTask(runtime.TaskID, reason); err != nil {
		return err
	}
	o.printBlockedNext(runtime.TaskID)
	return nil
}

func runtimeEventPayload(runtime state.Runtime) map[string]any {
	payload := map[string]any{
		"pid":            runtime.PID,
		"runtime_status": runtime.Status,
	}
	if !runtime.LastHeartbeat.IsZero() {
		payload["last_heartbeat"] = runtime.LastHeartbeat.Format(time.RFC3339)
	}
	return payload
}

func (o *Orchestrator) trackProcess(taskID string, cmd *exec.Cmd) {
	done := make(chan error, 1)
	o.mu.Lock()
	o.procs[taskID] = done
	o.mu.Unlock()
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
}

func (o *Orchestrator) processExited(taskID string) (bool, error) {
	o.mu.Lock()
	ch, ok := o.procs[taskID]
	o.mu.Unlock()
	if !ok {
		return false, nil
	}
	select {
	case err, ok := <-ch:
		o.forgetProcess(taskID)
		if !ok {
			return true, nil
		}
		return true, err
	default:
		return false, nil
	}
}

func (o *Orchestrator) forgetProcess(taskID string) {
	o.mu.Lock()
	delete(o.procs, taskID)
	o.mu.Unlock()
}

func (o *Orchestrator) idle() (bool, error) {
	count, err := o.store.ActiveTaskCount()
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	runtimes, err := o.store.ListActiveRuntimes()
	if err != nil {
		return false, err
	}
	return len(runtimes) == 0, nil
}

func (o *Orchestrator) printRunHandoff() error {
	reviews, err := o.store.ListReviews()
	if err != nil {
		return err
	}
	if len(reviews) == 0 {
		return nil
	}
	fmt.Printf("%s worker execution complete; review decisions remain\n", ts())
	return nil
}

func (o *Orchestrator) printReviewNext(taskID string) {
	fmt.Printf("%s next: cubicleq review accept %s | cubicleq review reject %s [--note \"...\"]\n", ts(), taskID, taskID)
	allowed, err := o.orchestrateReviewAllowed()
	if err != nil {
		return
	}
	if allowed {
		fmt.Printf("%s or run: cubicleq orchestrate\n", ts())
	}
}

func (o *Orchestrator) printBlockedNext(taskID string) {
	fmt.Printf("%s next: cubicleq logs %s\n", ts(), taskID)
	fmt.Printf("%s or:   cubicleq blockers resolve %s\n", ts(), taskID)
}

func (o *Orchestrator) printFailedNext(taskID string) {
	fmt.Printf("%s next: cubicleq logs %s\n", ts(), taskID)
	fmt.Printf("%s or:   cubicleq retry %s\n", ts(), taskID)
}

func (o *Orchestrator) orchestrateReviewAllowed() (bool, error) {
	policy, err := config.LoadPolicy(o.root)
	if err != nil {
		return false, err
	}
	if !policy.Orchestrator.Enabled {
		return false, nil
	}
	if !config.AllowsAction(policy, "review_accept") {
		return false, nil
	}
	return config.AllowsAction(policy, "merge_branch"), nil
}

func Cleanup(root string, store *state.Store) error {
	worktrees, err := store.ListTaskWorktreePaths()
	if err != nil {
		return err
	}
	var cleanupErrs []string
	for _, path := range worktrees {
		if err := worktree.Cleanup(root, path); err != nil {
			cleanupErrs = append(cleanupErrs, err.Error())
		}
	}
	entries, err := os.ReadDir(config.RunsDir(root))
	if err == nil {
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(config.RunsDir(root), entry.Name()))
		}
	}
	if len(cleanupErrs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(cleanupErrs, "; "))
	}
	return nil
}

func Doctor(root string, cfg config.Config) error {
	if _, err := exec.LookPath("git"); err != nil {
		return err
	}
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return errors.New("current directory is not a git work tree")
	}
	if cfg.Backend.Command == "" {
		return errors.New("backend command is not configured")
	}
	if _, err := exec.LookPath(cfg.Backend.Command); err != nil {
		return fmt.Errorf("backend command %q not found: %w", cfg.Backend.Command, err)
	}
	return nil
}

func (o *Orchestrator) handleInterrupt() error {
	fmt.Printf("%s interrupt received, stopping orchestrator\n", ts())
	return Stop(o.store, false)
}

func Stop(store *state.Store, graceful bool) error {
	runtimes, err := store.ListLiveRuntimes()
	if err != nil {
		return err
	}
	if graceful && len(runtimes) > 0 {
		if err := store.SetSetting(stopRequestedKey, "graceful"); err != nil {
			return err
		}
	}
	for _, runtime := range runtimes {
		if graceful {
			_ = interruptUnixProcessTree(runtime.PID, 8*time.Second)
		} else if processAlive(runtime.PID) {
			_ = killUnixProcessTree(runtime.PID)
		}
		if graceful {
			fmt.Printf("%s gracefully stopping %s\n", ts(), runtime.TaskID)
		} else {
			fmt.Printf("%s requeueing %s due to interrupt\n", ts(), runtime.TaskID)
		}
		task, err := store.GetTask(runtime.TaskID)
		if err != nil {
			return err
		}
		if task.State != state.TaskStateRunning && task.State != state.TaskStateReady {
			continue
		}
		if err := store.RetryTask(runtime.TaskID); err != nil {
			return err
		}
		eventType := "stopped"
		reason := "stopped by operator"
		if !graceful {
			eventType = "interrupted"
			reason = "interrupted by operator"
		}
		if err := store.RecordEvent(runtime.TaskID, eventType, map[string]any{"reason": reason}); err != nil {
			return err
		}
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func interruptUnixProcessTree(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	_ = signalUnixProcessTree(pid, syscall.SIGINT)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = signalUnixProcessTree(pid, syscall.SIGTERM)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return killUnixProcessTree(pid)
}

func killUnixProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := signalUnixProcessTree(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// Worker launch uses Setpgid and runtime stop/reconcile use Unix signals,
// so Cubicleq's worker lifecycle is Unix-oriented today.
func signalUnixProcessTree(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(sig)
}

func (o *Orchestrator) consumeStopRequest() (bool, error) {
	value, err := o.store.GetSetting(stopRequestedKey)
	if err != nil {
		return false, err
	}
	if value == "" {
		return false, nil
	}
	if err := o.store.DeleteSetting(stopRequestedKey); err != nil {
		return false, err
	}
	return true, nil
}
