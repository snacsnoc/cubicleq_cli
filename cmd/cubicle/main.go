package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/easto/cubicle-dev-flow-aut/internal/actions"
	"github.com/easto/cubicle-dev-flow-aut/internal/config"
	"github.com/easto/cubicle-dev-flow-aut/internal/orchestrator"
	"github.com/easto/cubicle-dev-flow-aut/internal/orchestratoragent"
	"github.com/easto/cubicle-dev-flow-aut/internal/reporting"
	"github.com/easto/cubicle-dev-flow-aut/internal/state"
	"github.com/easto/cubicle-dev-flow-aut/internal/worktree"
)

const (
	displayLaunchSilenceTimeout  = 60 * time.Second
	displayStaleHeartbeatTimeout = 2 * time.Minute
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	rootOverride, args, err := parseGlobalArgs(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "help", "--help", "-h":
		return usage()
	}

	switch args[0] {
	case "init":
		return runInit(rootOverride, args[1:])
	case "tasks":
		return runTasks(rootOverride, args[1:])
	case "blockers":
		return runBlockers(rootOverride, args[1:])
	case "review":
		return runReview(rootOverride, args[1:])
	case "workers", "sessions":
		return runRuntimes(rootOverride)
	case "status":
		return runStatus(rootOverride)
	case "run":
		return runOrchestrator(rootOverride, args[1:])
	case "orchestrate":
		return runOrchestrate(rootOverride, args[1:])
	case "stop":
		return runStop(rootOverride)
	case "retry":
		return runRetry(rootOverride, args[1:])
	case "cleanup":
		return runCleanup(rootOverride)
	case "doctor":
		return runDoctor(rootOverride)
	case "logs":
		return runLogs(rootOverride, args[1:])
	case "mcp-call":
		return runMCPCall(args[1:])
	default:
		return usage()
	}
}

func usage() error {
	fmt.Println(`cubicle

Local CLI orchestrator for coding agents with explicit git worktrees, durable task state, and review-oriented outputs.

Usage:
  cubicle [--root /abs/path/to/repo] <command> [args]

Core workflow:
  cubicle init [--bootstrap-git]
  cubicle tasks add --title "task title" [--description "..."] [--priority high] [--validate "npm test"] [--depends-on "t-1,t-2"]
  cubicle tasks set-deps <task-id> --depends-on "t-1,t-2"
  cubicle run
  cubicle orchestrate
  cubicle stop
  cubicle status
  cubicle logs <task-id>
  cubicle blockers list
  cubicle review list

Commands:
  init               Initialize .cubicle/ state in the target repo
  tasks              Add, list, show, and update tasks
  blockers           Inspect or resolve blocked tasks
  review             Inspect review-ready tasks
  workers            Show worker runtime records
  sessions           Alias for workers
  status             Show a compact task/runtime/blocker/review summary
  run                Run the orchestrator in the foreground
  orchestrate        Run the dedicated orchestrator agent in the foreground
  stop               Gracefully stop active workers and requeue unfinished tasks
  retry              Reset a task for another attempt
  cleanup            Remove runtime artifacts and worktrees
  doctor             Check git/backend prerequisites
  logs               Show recent task events and worker stdout/stderr
  mcp-call           Low-level MCP test command
  help               Show this help

  Notes:
  --root defaults to the current directory when omitted.
  run stays in the foreground. Use another terminal with 'cubicle status' to inspect progress.
  task-targeting commands accept unique task-id prefixes.
  tasks set-deps replaces the entire dependency list; use --depends-on "" to clear dependencies.
  Ctrl+C stops the orchestrator, kills active workers, and requeues unfinished tasks.
  stop sends a graceful interrupt to active workers before requeueing them.`)
	return nil
}

func runInit(rootOverride string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	bootstrapGit := fs.Bool("bootstrap-git", false, "initialize git and create an initial empty commit when needed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("init takes no positional arguments")
	}
	root, err := workingRoot(rootOverride)
	if err != nil {
		return err
	}
	if *bootstrapGit {
		if err := worktree.BootstrapRepo(root); err != nil {
			return err
		}
	}
	cfg, err := config.Default(root)
	if err != nil {
		return err
	}
	baseBranch, err := detectBaseBranch(root)
	if err != nil {
		return err
	}
	policy := config.DefaultPolicy(baseBranch)
	if err := config.WriteDefault(root, cfg); err != nil {
		return err
	}
	if err := config.WriteDefaultPolicy(root, policy); err != nil {
		return err
	}
	store, err := state.Open(root)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.InitSchema(); err != nil {
		return err
	}
	fmt.Println("initialized cubicle runtime in", filepath.Join(root, ".cubicle"))
	return nil
}

func runTasks(rootOverride string, args []string) error {
	if len(args) == 0 {
		return errors.New("tasks subcommand required")
	}
	_, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("tasks add", flag.ContinueOnError)
		title := fs.String("title", "", "task title")
		description := fs.String("description", "", "task description")
		priority := fs.String("priority", "medium", "low|medium|high")
		role := fs.String("role", "implementer", "role hint")
		validate := fs.String("validate", "", "comma-separated validation commands")
		deps := fs.String("depends-on", "", "comma-separated dependency ids")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := authorizeAction(rootOverride, "create_followup_task"); err != nil {
			return err
		}
		if strings.TrimSpace(*title) == "" {
			return errors.New("--title is required")
		}
		dependencies, err := parseDependencyCSV(*deps)
		if err != nil {
			return err
		}
		task := state.Task{
			ID:                 state.NewTaskID(),
			Title:              *title,
			Description:        *description,
			Priority:           *priority,
			State:              state.TaskStateTodo,
			RoleHint:           *role,
			Dependencies:       dependencies,
			ValidationCommands: splitCSV(*validate),
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
		}
		if err := store.AddTask(task); err != nil {
			return err
		}
		fmt.Println(task.ID)
		return nil
	case "list":
		tasks, err := store.ListTasks()
		if err != nil {
			return err
		}
		for _, task := range tasks {
			fmt.Printf("%s\t%s\t%s\t%s\n", task.ID, task.State, task.Priority, task.Title)
		}
		return nil
	case "show":
		if len(args) < 2 {
			return errors.New("tasks show <task-id>")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		task, err := store.GetTask(taskID)
		if err != nil {
			return err
		}
		return printJSON(task)
	case "set-validation":
		fs := flag.NewFlagSet("tasks set-validation", flag.ContinueOnError)
		validate := fs.String("validate", "", "comma-separated validation commands")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		rest := fs.Args()
		if len(rest) < 1 {
			return errors.New("tasks set-validation <task-id> --validate \"cmd1,cmd2\"")
		}
		taskID, err := resolveTaskIDArg(store, rest[0])
		if err != nil {
			return err
		}
		commands := splitCSV(*validate)
		if len(commands) == 0 {
			return errors.New("--validate is required")
		}
		if err := store.SetTaskValidationCommands(taskID, commands); err != nil {
			return err
		}
		fmt.Printf("updated validation for %s\n", taskID)
		return nil
	case "set-deps":
		if len(args) < 2 {
			return errors.New("tasks set-deps <task-id> --depends-on \"id1,id2\"")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("tasks set-deps", flag.ContinueOnError)
		deps := fs.String("depends-on", "", "comma-separated dependency ids; replaces the current dependency list; use --depends-on \"\" to clear")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		depsProvided := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "depends-on" {
				depsProvided = true
			}
		})
		if !depsProvided {
			return errors.New("--depends-on is required; use --depends-on \"\" to clear dependencies")
		}
		dependencies, err := parseDependencyCSV(*deps)
		if err != nil {
			return err
		}
		if err := store.SetTaskDependencies(taskID, dependencies); err != nil {
			return err
		}
		fmt.Printf("updated dependencies for %s\n", taskID)
		return nil
	case "ready":
		if len(args) < 2 {
			return errors.New("tasks ready <task-id>")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		return store.MarkTaskReady(taskID)
	default:
		return errors.New("unknown tasks subcommand")
	}
}

func runBlockers(rootOverride string, args []string) error {
	if len(args) == 0 {
		fmt.Print(blockersUsageText())
		return nil
	}
	root, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()

	switch args[0] {
	case "list":
		blockers, err := store.ListBlockers()
		if err != nil {
			return err
		}
		for _, blocker := range blockers {
			fmt.Printf("%s\t%s\n", blocker.TaskID, blocker.Reason)
		}
		return nil
	case "resolve":
		if len(args) < 2 {
			return errors.New("blockers resolve <task-id>")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		if err := authorizeAction(rootOverride, "resolve_blocker"); err != nil {
			return err
		}
		policy, err := config.LoadPolicy(root)
		if err != nil {
			return err
		}
		line, err := (actions.Executor{Root: root, Store: store, Policy: policy}).ResolveBlocker(taskID, "operator")
		if err != nil {
			return err
		}
		fmt.Println(line)
		return nil
	default:
		return errors.New("unknown blockers subcommand")
	}
}

func runReview(rootOverride string, args []string) error {
	if len(args) == 0 {
		fmt.Print(reviewUsageText())
		return nil
	}
	root, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()

	switch args[0] {
	case "list":
		reviews, err := store.ListReviews()
		if err != nil {
			return err
		}
		for _, review := range reviews {
			fmt.Printf("%s\t%s\t%s\n", review.TaskID, review.Status, review.SummaryPath)
		}
		return nil
	case "show":
		if len(args) < 2 {
			return errors.New("review show <task-id>")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		review, err := store.GetReview(taskID)
		if err != nil {
			return err
		}
		return printJSON(review)
	case "accept":
		if len(args) != 2 {
			return errors.New("review accept <task-id>")
		}
		taskID, err := resolveTaskIDArg(store, args[1])
		if err != nil {
			return err
		}
		if err := authorizeAction(rootOverride, "review_accept"); err != nil {
			return err
		}
		policy, err := config.LoadPolicy(root)
		if err != nil {
			return err
		}
		line, err := (actions.Executor{Root: root, Store: store, Policy: policy}).AcceptReview(taskID, "operator")
		if err != nil {
			return err
		}
		fmt.Println(line)
		return nil
	case "reject":
		taskID, rejectArgs := splitLeadingTaskID(args[1:])
		fs := flag.NewFlagSet("review reject", flag.ContinueOnError)
		note := fs.String("note", "", "operator note")
		if err := fs.Parse(rejectArgs); err != nil {
			return err
		}
		if taskID == "" && fs.NArg() == 1 {
			taskID = fs.Arg(0)
		}
		if taskID == "" {
			return errors.New("review reject <task-id> --note \"...\"")
		}
		taskID, err = resolveTaskIDArg(store, taskID)
		if err != nil {
			return err
		}
		if err := authorizeAction(rootOverride, "review_reject"); err != nil {
			return err
		}
		policy, err := config.LoadPolicy(root)
		if err != nil {
			return err
		}
		line, err := (actions.Executor{Root: root, Store: store, Policy: policy}).RejectReview(taskID, *note, "operator")
		if err != nil {
			return err
		}
		fmt.Println(line)
		return nil
	default:
		return errors.New("unknown review subcommand")
	}
}

func runRuntimes(rootOverride string) error {
	_, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	runtimes, err := store.ListRuntimes()
	if err != nil {
		return err
	}
	for _, runtime := range runtimes {
		fmt.Printf("%s\t%s\t%s\t%d\t%s\n", runtime.TaskID, runtime.Status, runtime.WorktreePath, runtime.PID, runtime.LastHeartbeat.Format(time.RFC3339))
	}
	return nil
}

func runStatus(rootOverride string) error {
	_, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()

	tasks, err := store.ListTasks()
	if err != nil {
		return err
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		return err
	}
	blockers, err := store.ListBlockers()
	if err != nil {
		return err
	}
	reviews, err := store.ListReviews()
	if err != nil {
		return err
	}
	runnableWork, err := store.HasRunnableTasks()
	if err != nil {
		return err
	}

	fmt.Println("Tasks")
	if len(tasks) == 0 {
		fmt.Println("  (none)")
	} else {
		runtimeByTask := make(map[string]state.Runtime, len(runtimes))
		for _, runtime := range runtimes {
			runtimeByTask[runtime.TaskID] = runtime
		}
		for _, task := range tasks {
			displayState := task.State
			if runtime, ok := runtimeByTask[task.ID]; ok && task.State == state.TaskStateReady {
				displayState = renderRuntimeState(runtime)
			}
			fmt.Printf("  %s  %-9s  %-6s  %s\n", task.ID, displayState, task.Priority, task.Title)
		}
	}

	fmt.Println("\nRuntimes")
	if len(runtimes) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, runtime := range runtimes {
			fmt.Printf("  %s  %-10s pid=%d  last=%s  %s\n", runtime.TaskID, renderRuntimeState(runtime), runtime.PID, describeAge(runtime.LastHeartbeat), runtime.WorktreePath)
		}
	}

	fmt.Println("\nBlockers")
	if len(blockers) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, blocker := range blockers {
			fmt.Printf("  %s  %s\n", blocker.TaskID, blocker.Reason)
		}
	}

	fmt.Println("\nReview")
	if len(reviews) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, review := range reviews {
			fmt.Printf("  %s  %s\n", review.TaskID, review.SummaryPath)
		}
	}

	recommendations := buildStatusRecommendations(tasks, blockers, reviews, runtimes, runnableWork)
	fmt.Println("\nRecommended Next Commands")
	if len(recommendations) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	for _, recommendation := range recommendations {
		fmt.Printf("  %s\n", recommendation.summary())
		for _, command := range recommendation.Commands {
			fmt.Printf("    %-7s %s\n", command.Label+":", command.Value)
		}
	}
	return nil
}

func runOrchestrator(rootOverride string, args []string) error {
	if len(args) != 0 {
		return errors.New("run takes no arguments")
	}
	root, cfg, store, err := openRuntime(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()

	binPath, err := os.Executable()
	if err != nil {
		return err
	}
	orch := orchestrator.New(root, binPath, cfg, store)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return orch.Run(ctx)
}

func runOrchestrate(rootOverride string, args []string) error {
	if len(args) != 0 {
		return errors.New("orchestrate takes no arguments")
	}
	root, cfg, store, err := openRuntime(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	policy, err := config.LoadPolicy(root)
	if err != nil {
		return err
	}
	binPath, err := os.Executable()
	if err != nil {
		return err
	}
	return orchestratoragent.Run(root, binPath, cfg, policy, store)
}

func runStop(rootOverride string) error {
	_, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	runtimes, err := store.ListActiveRuntimes()
	if err != nil {
		return err
	}
	if len(runtimes) == 0 {
		fmt.Println("no active workers")
		return nil
	}
	return orchestrator.Stop(store, true)
}

func runRetry(rootOverride string, args []string) error {
	if len(args) != 1 {
		return errors.New("retry <task-id>")
	}
	if err := authorizeAction(rootOverride, "retry_task"); err != nil {
		return err
	}
	root, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	taskID, err := resolveTaskIDArg(store, args[0])
	if err != nil {
		return err
	}
	policy, err := config.LoadPolicy(root)
	if err != nil {
		return err
	}
	line, err := (actions.Executor{Root: root, Store: store, Policy: policy}).RetryTask(taskID, "operator")
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}

func runCleanup(rootOverride string) error {
	root, _, store, err := openRuntime(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	return orchestrator.Cleanup(root, store)
}

func runDoctor(rootOverride string) error {
	root, cfg, store, err := openRuntime(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := orchestrator.Doctor(root, cfg); err != nil {
		return err
	}
	runtimes, err := store.ListRuntimes()
	if err != nil {
		return err
	}
	fmt.Println("Root")
	fmt.Printf("  %s\n", root)
	fmt.Println("\nGit")
	fmt.Println("  ok")
	fmt.Println("\nBackend")
	fmt.Printf("  type=%s command=%s\n", cfg.Backend.Type, cfg.Backend.Command)
	policy, err := config.LoadPolicy(root)
	if err == nil {
		fmt.Println("\nPolicy")
		fmt.Printf("  base=%s mode=%s allowed=%v\n", policy.BaseBranch, policy.Orchestrator.Mode, config.AllowedActions(policy))
	}
	fmt.Println("\nQwen Settings")
	settingsPath := filepath.Join(root, ".qwen", "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		fmt.Printf("  found %s\n", settingsPath)
	} else {
		fmt.Printf("  missing %s\n", settingsPath)
	}
	fmt.Println("\nRuntimes")
	if len(runtimes) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	for _, runtime := range runtimes {
		health := "ok"
		if !processKnownAlive(runtime.PID) {
			health = "missing-process"
		} else if runtime.Status == "launching" && time.Since(runtime.LastHeartbeat) > displayLaunchSilenceTimeout {
			health = "no-report-yet"
		} else if time.Since(runtime.LastHeartbeat) > displayStaleHeartbeatTimeout {
			health = "stale-heartbeat"
		}
		fmt.Printf("  %s  pid=%d  state=%s  last=%s  %s\n", runtime.TaskID, runtime.PID, renderRuntimeState(runtime), describeAge(runtime.LastHeartbeat), health)
	}
	return nil
}

func runLogs(rootOverride string, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	tail := fs.Int("tail", 40, "number of recent lines per stream")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("logs <task-id>")
	}
	root, store, err := openStore(rootOverride)
	if err != nil {
		return err
	}
	defer store.Close()
	taskID := fs.Arg(0)
	taskID, err = resolveTaskIDArg(store, taskID)
	if err != nil {
		return err
	}
	task, err := store.GetTask(taskID)
	if err != nil {
		return err
	}
	events, err := store.ListEvents(taskID, 10)
	if err != nil {
		return err
	}
	artifacts, err := store.ListTaskArtifacts(taskID)
	if err != nil {
		return err
	}
	validationRuns, err := store.ListValidationRuns(taskID)
	if err != nil {
		return err
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].ID < events[j].ID
	})

	fmt.Println("Task")
	fmt.Printf("  %s  %s  %s\n", task.ID, task.State, task.Title)
	if task.WorktreePath != "" {
		fmt.Printf("  worktree: %s\n", task.WorktreePath)
	}
	if task.BranchName != "" {
		fmt.Printf("  branch:   %s\n", task.BranchName)
	}

	fmt.Println("\nEvents")
	if len(events) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, event := range events {
			fmt.Printf("  %s  %-12s  %s\n", event.CreatedAt.Format(time.RFC3339), event.Type, compactJSON(event.Payload))
		}
	}

	fmt.Println("\nArtifacts")
	if len(artifacts) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, artifact := range artifacts {
			fmt.Printf("  %-16s %s\n", artifact.Kind, artifact.Path)
		}
	}

	fmt.Println("\nValidation")
	if len(validationRuns) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, run := range validationRuns {
			fmt.Printf("  %-22s %-6s exit=%d  %s\n", run.Command, run.Status, run.ExitCode, run.Summary)
		}
	}

	stdoutPath := config.TaskLogPath(root, taskID, "stdout")
	stderrPath := config.TaskLogPath(root, taskID, "stderr")
	fmt.Printf("\nStdout: %s\n", stdoutPath)
	if err := printTail(stdoutPath, *tail); err != nil {
		fmt.Printf("  (unavailable: %v)\n", err)
	}
	fmt.Printf("\nStderr: %s\n", stderrPath)
	if err := printTail(stderrPath, *tail); err != nil {
		fmt.Printf("  (unavailable: %v)\n", err)
	}
	return nil
}

func runMCPCall(args []string) error {
	fs := flag.NewFlagSet("mcp-call", flag.ContinueOnError)
	url := fs.String("url", "", "mcp url")
	tool := fs.String("tool", "", "tool name")
	payload := fs.String("payload", "{}", "json object payload")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *tool == "" {
		return errors.New("--url and --tool are required")
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(*payload), &input); err != nil {
		return err
	}
	return reporting.CallTool(context.Background(), *url, *tool, input)
}

func openStore(rootOverride string) (string, *state.Store, error) {
	root, err := workingRoot(rootOverride)
	if err != nil {
		return "", nil, err
	}
	store, err := state.Open(root)
	if err != nil {
		return "", nil, err
	}
	if err := store.InitSchema(); err != nil {
		store.Close()
		return "", nil, err
	}
	return root, store, nil
}

func openRuntime(rootOverride string) (string, config.Config, *state.Store, error) {
	root, store, err := openStore(rootOverride)
	if err != nil {
		return "", config.Config{}, nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		store.Close()
		return "", config.Config{}, nil, err
	}
	return root, cfg, store, nil
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseDependencyCSV(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("dependency ids must not be empty")
		}
		out = append(out, part)
	}
	return out, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func workingRoot(rootOverride string) (string, error) {
	if root := strings.TrimSpace(rootOverride); root != "" {
		return filepath.Abs(root)
	}
	if root := strings.TrimSpace(os.Getenv("CUBICLE_ROOT")); root != "" {
		return filepath.Abs(root)
	}
	return os.Getwd()
}

func parseGlobalArgs(args []string) (string, []string, error) {
	var root string
	rest := args
	for len(rest) > 0 {
		switch {
		case rest[0] == "--root":
			if len(rest) < 2 {
				return "", nil, errors.New("--root requires a path")
			}
			root = rest[1]
			rest = rest[2:]
		case strings.HasPrefix(rest[0], "--root="):
			root = strings.TrimPrefix(rest[0], "--root=")
			rest = rest[1:]
		default:
			return root, rest, nil
		}
	}
	return root, rest, nil
}

func authorizeAction(rootOverride, action string) error {
	if os.Getenv("CUBICLE_ACTOR") != "orchestrator-agent" {
		return nil
	}
	root, err := workingRoot(rootOverride)
	if err != nil {
		return err
	}
	policy, err := config.LoadPolicy(root)
	if err != nil {
		return err
	}
	if !config.AllowsAction(policy, action) {
		return fmt.Errorf("policy denied action %q", action)
	}
	if action == "review_accept" && !config.AllowsAction(policy, "merge_branch") {
		return fmt.Errorf("policy denied action %q", "merge_branch")
	}
	return nil
}

func detectBaseBranch(root string) (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "main", nil
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "main", nil
	}
	return branch, nil
}

func printTail(path string, lines int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		fmt.Println("  (empty)")
		return nil
	}
	parts := strings.Split(trimmed, "\n")
	if lines > 0 && len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	for _, line := range parts {
		fmt.Printf("  %s\n", line)
	}
	return nil
}

func compactJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return "{}"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(raw)); err != nil {
		return raw
	}
	return out.String()
}

func describeAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	age := time.Since(t).Round(time.Second)
	if age < 0 {
		age = 0
	}
	label := age.String() + " ago"
	if age > displayStaleHeartbeatTimeout {
		return label + " stale"
	}
	return label
}

func processKnownAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func renderRuntimeState(runtime state.Runtime) string {
	if runtime.Status == "launching" && time.Since(runtime.LastHeartbeat) > displayLaunchSilenceTimeout {
		return "unreported"
	}
	return runtime.Status
}

func splitLeadingTaskID(args []string) (string, []string) {
	if len(args) == 0 {
		return "", args
	}
	if strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
}

func reviewUsageText() string {
	return `review commands:
  cubicle review list
  cubicle review show <task-id>
  cubicle review accept <task-id>
  cubicle review reject <task-id> --note "..."
`
}

func blockersUsageText() string {
	return `blockers commands:
  cubicle blockers list
  cubicle blockers resolve <task-id>
`
}

func resolveTaskIDArg(store *state.Store, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("task id is required")
	}
	tasks, err := store.ListTasks()
	if err != nil {
		return "", err
	}
	for _, task := range tasks {
		if task.ID == raw {
			return task.ID, nil
		}
	}
	var matches []string
	for _, task := range tasks {
		if strings.HasPrefix(task.ID, raw) {
			matches = append(matches, task.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("unknown task id or prefix: %s", raw)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous task id prefix %q: matches %s", raw, strings.Join(matches, ", "))
	}
}

type statusRecommendation struct {
	Kind     string
	TaskID   string
	Reason   string
	Commands []recommendedCommand
}

type recommendedCommand struct {
	Label string
	Value string
}

func (r statusRecommendation) summary() string {
	if r.TaskID == "" {
		return r.Reason
	}
	if r.Kind == "" {
		return fmt.Sprintf("%s  %s", r.TaskID, r.Reason)
	}
	return fmt.Sprintf("%s  %s  %s", r.Kind, r.TaskID, r.Reason)
}

func buildStatusRecommendations(tasks []state.Task, blockers []state.Blocker, reviews []state.Review, runtimes []state.Runtime, runnableWork bool) []statusRecommendation {
	if len(tasks) == 0 {
		return []statusRecommendation{{
			Reason: "no tasks exist",
			Commands: []recommendedCommand{{
				Label: "next",
				Value: `cubicle tasks add --title "..." --validate "..."`,
			}},
		}}
	}

	taskByID := make(map[string]state.Task, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
	}

	recommendations := make([]statusRecommendation, 0, len(blockers)+len(reviews)+1)
	for _, blocker := range blockers {
		task := taskByID[blocker.TaskID]
		if blocker.Reason == "no validation configured" && len(task.ValidationCommands) == 0 {
			recommendations = append(recommendations, statusRecommendation{
				Kind:   "blocked",
				TaskID: blocker.TaskID,
				Reason: blocker.Reason,
				Commands: []recommendedCommand{
					{Label: "next", Value: fmt.Sprintf(`cubicle tasks set-validation %s --validate "..."`, blocker.TaskID)},
					{Label: "then", Value: fmt.Sprintf("cubicle blockers resolve %s", blocker.TaskID)},
				},
			})
			continue
		}
		recommendations = append(recommendations, statusRecommendation{
			Kind:   "blocked",
			TaskID: blocker.TaskID,
			Reason: blocker.Reason,
			Commands: []recommendedCommand{
				{Label: "inspect", Value: fmt.Sprintf("cubicle logs %s", blocker.TaskID)},
			},
		})
	}
	for _, review := range reviews {
		recommendations = append(recommendations, statusRecommendation{
			Kind:   "review",
			TaskID: review.TaskID,
			Reason: "review-ready task needs operator decision",
			Commands: []recommendedCommand{
				{Label: "next", Value: fmt.Sprintf("cubicle review accept %s", review.TaskID)},
				{Label: "or", Value: fmt.Sprintf(`cubicle review reject %s --note "..."`, review.TaskID)},
			},
		})
	}
	if !hasActiveRuntime(runtimes) && runnableWork {
		recommendations = append(recommendations, statusRecommendation{
			Reason: "runnable tasks exist with no active workers",
			Commands: []recommendedCommand{{
				Label: "next",
				Value: "cubicle run",
			}},
		})
	}
	return recommendations
}

func hasActiveRuntime(runtimes []state.Runtime) bool {
	for _, runtime := range runtimes {
		if runtime.Status == "launching" || runtime.Status == "running" {
			return true
		}
	}
	return false
}
