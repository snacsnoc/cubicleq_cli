package orchestratoragent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/actions"
	"github.com/snacsnoc/cubicleq_cli/internal/agents"
	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/prompting"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

const orchestratorTimeout = 90 * time.Second

func ts() string {
	return time.Now().UTC().Format("[15:04:05]")
}

type Result struct {
	Role            string   `json:"role"`
	Status          string   `json:"status"`
	Actions         []Action `json:"actions"`
	CurrentBlockers string   `json:"current_blockers"`
	Notes           string   `json:"notes"`
}

type Action struct {
	Type               string   `json:"type"`
	TaskID             string   `json:"task_id,omitempty"`
	Note               string   `json:"note,omitempty"`
	Title              string   `json:"title,omitempty"`
	Description        string   `json:"description,omitempty"`
	Role               string   `json:"role,omitempty"`
	DependsOn          []string `json:"depends_on,omitempty"`
	ValidationCommands []string `json:"validation_commands,omitempty"`
}

func Run(root, binPath string, cfg config.Config, policy config.Policy, store *state.Store) error {
	if !policy.Orchestrator.Enabled {
		return fmt.Errorf("orchestrator agent is disabled in %s", config.PolicyPath(root))
	}
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
	bundle, err := prompting.WriteOrchestratorBundle(root, policy, tasks, runtimes, blockers, reviews)
	if err != nil {
		return err
	}
	fmt.Printf("%s starting cubicleq orchestrator agent in %s\n", ts(), root)
	fmt.Printf("%s policy allowed=%v\n", ts(), config.AllowedActions(policy))
	fmt.Printf("%s use cubicleq status/logs/review in another terminal to inspect resulting state changes\n", ts())

	result, err := runPlanner(root, binPath, cfg, bundle.PromptPath)
	if err != nil {
		return err
	}
	executed, err := executeActions(root, store, policy, result.Actions)
	if err != nil {
		return err
	}
	if len(executed) == 0 {
		fmt.Printf("%s orchestrator returned no executable actions\n", ts())
		return nil
	}
	for _, line := range executed {
		fmt.Printf("%s %s\n", ts(), line)
	}
	return nil
}

func runPlanner(root, binPath string, cfg config.Config, promptPath string) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), orchestratorTimeout)
	defer cancel()

	cmd, stdoutBuf, stderrBuf, cleanup, err := buildCommand(ctx, root, binPath, cfg, promptPath)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("orchestrator timed out after %s; inspect %s", orchestratorTimeout, filepath.Join(config.LogsDir(root), "orchestrator.stderr.log"))
		}
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			return Result{}, fmt.Errorf("orchestrator failed: %s", stderr)
		}
		return Result{}, err
	}
	result, err := parseResult(stdoutBuf.Bytes())
	if err != nil {
		return Result{}, fmt.Errorf("parse orchestrator action contract: %w", err)
	}
	return result, nil
}

func buildCommand(ctx context.Context, root, binPath string, cfg config.Config, promptPath string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, func(), error) {
	args := append([]string{}, cfg.Backend.Args...)
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	args = append(agents.QwenHeadlessArgs(root, string(promptBytes), true), args...)
	cmd := exec.CommandContext(ctx, cfg.Backend.Command, args...)
	cmd.Dir = root

	stdoutPath := filepath.Join(config.LogsDir(root), "orchestrator.stdout.log")
	stderrPath := filepath.Join(config.LogsDir(root), "orchestrator.stderr.log")
	if err := os.MkdirAll(config.LogsDir(root), 0o755); err != nil {
		return nil, nil, nil, nil, err
	}
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, nil, nil, nil, err
	}
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	cmd.Stdout = io.MultiWriter(stdoutFile, stdoutBuf)
	cmd.Stderr = io.MultiWriter(stderrFile, stderrBuf)
	cmd.Env = append(os.Environ(),
		"CUBICLE_BIN="+binPath,
	)
	cleanup := func() {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	}
	return cmd, stdoutBuf, stderrBuf, cleanup, nil
}

func parseResult(raw []byte) (Result, error) {
	doc := extractJSON(raw)
	if doc == "" {
		return Result{}, errors.New("no JSON object found in orchestrator output")
	}
	var result Result
	if err := json.Unmarshal([]byte(doc), &result); err != nil {
		return Result{}, err
	}
	if result.Role == "" {
		result.Role = "orchestrator"
	}
	return result, nil
}

func extractJSON(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return text[start : end+1]
}

func executeActions(root string, store *state.Store, policy config.Policy, proposedActions []Action) ([]string, error) {
	var executed []string
	executor := actions.Executor{Root: root, Store: store, Policy: policy}
	for _, action := range proposedActions {
		line, err := executeAction(executor, action)
		if err != nil {
			return executed, err
		}
		if line != "" {
			executed = append(executed, line)
		}
	}
	return executed, nil
}

func executeAction(executor actions.Executor, action Action) (string, error) {
	switch action.Type {
	case "":
		return "", nil
	case "review_accept":
		if !config.AllowsAction(executor.Policy, "review_accept") {
			return "", fmt.Errorf("policy denied action %q", "review_accept")
		}
		return executor.AcceptReview(action.TaskID, "orchestrator-agent")
	case "review_reject":
		if !config.AllowsAction(executor.Policy, "review_reject") {
			return "", fmt.Errorf("policy denied action %q", "review_reject")
		}
		return executor.RejectReview(action.TaskID, action.Note, "orchestrator-agent")
	case "retry_task":
		if !config.AllowsAction(executor.Policy, "retry_task") {
			return "", fmt.Errorf("policy denied action %q", "retry_task")
		}
		return executor.RetryTask(action.TaskID, "orchestrator-agent")
	case "resolve_blocker":
		if !config.AllowsAction(executor.Policy, "resolve_blocker") {
			return "", fmt.Errorf("policy denied action %q", "resolve_blocker")
		}
		return executor.ResolveBlocker(action.TaskID, "orchestrator-agent")
	case "create_followup_task":
		if !config.AllowsAction(executor.Policy, "create_followup_task") {
			return "", fmt.Errorf("policy denied action %q", "create_followup_task")
		}
		return executor.CreateFollowupTask(action.Title, action.Description, action.Role, action.DependsOn, action.ValidationCommands, "orchestrator-agent")
	default:
		return "", fmt.Errorf("unknown orchestrator action %q", action.Type)
	}
}
