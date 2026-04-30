package agents

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

type Adapter struct {
	cfg config.BackendConfig
}

type LaunchSpec struct {
	Root       string
	BinPath    string
	Task       state.Task
	Runtime    state.Runtime
	PromptPath string
	MCPURL     string
}

func New(cfg config.BackendConfig) Adapter {
	return Adapter{cfg: cfg}
}

func (a Adapter) Launch(spec LaunchSpec) (*exec.Cmd, error) {
	args := append([]string{}, a.cfg.Args...)
	promptBytes, err := os.ReadFile(spec.PromptPath)
	if err != nil {
		return nil, err
	}
	if err := writeQwenSettings(spec.Root, spec.Runtime.WorktreePath, spec.MCPURL); err != nil {
		return nil, err
	}
	args = append(QwenHeadlessArgs(spec.Root, string(promptBytes), false), args...)
	cmd := exec.Command(a.cfg.Command, args...)
	cmd.Dir = spec.Runtime.WorktreePath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdoutFile, stderrFile, err := openWorkerLogs(spec.Root, spec.Task.ID)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.Env = append(os.Environ(),
		"CUBICLE_PROJECT_ROOT="+spec.Root,
		"CUBICLE_BIN="+spec.BinPath,
		"CUBICLE_TASK_ID="+spec.Task.ID,
		"CUBICLE_TASK_TITLE="+spec.Task.Title,
		"CUBICLE_TASK_DESCRIPTION="+spec.Task.Description,
		"CUBICLE_WORKTREE_PATH="+spec.Runtime.WorktreePath,
		"CUBICLE_MCP_URL="+spec.MCPURL,
		"CUBICLE_SESSION_ID="+spec.Runtime.SessionID,
	)
	if err := cmd.Start(); err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		return nil, err
	}
	_ = stdoutFile.Close()
	_ = stderrFile.Close()
	return cmd, nil
}

func NewRuntime(task state.Task, branchName, worktreePath string) state.Runtime {
	now := time.Now().UTC()
	return state.Runtime{
		TaskID:        task.ID,
		BranchName:    branchName,
		WorktreePath:  worktreePath,
		SessionID:     task.ID + "-session",
		Status:        "launching",
		LastHeartbeat: now,
		PID:           0,
	}
}

func QwenHeadlessArgs(root, prompt string, orchestrator bool) []string {
	args := []string{
		"--approval-mode=yolo",
		"--output-format", "text",
		"--append-system-prompt", QwenSystemPrompt(orchestrator),
		prompt,
	}
	if orchestrator {
		args = append([]string{"--max-session-turns", "32"}, args...)
	}
	if root != "" {
		args = append([]string{"--allowed-mcp-server-names", "cubicleq"}, args...)
	}
	return args
}

func QwenSystemPrompt(orchestrator bool) string {
	if orchestrator {
		return "Do not output conversational text, reasoning, or preamble. Return only the requested final output."
	}
	return "Do not output conversational text, reasoning, or preamble. Work directly, report through Cubicleq tools, and stop on explicit stop conditions."
}

func writeQwenSettings(projectRoot, worktreePath, mcpURL string) error {
	dir := filepath.Join(worktreePath, ".qwen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	settings, err := loadBaseQwenSettings(projectRoot)
	if err != nil {
		return err
	}
	ensureDefaultQwenSettings(settings)
	mergeCubicleMCP(settings, mcpURL)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o644)
}

func loadBaseQwenSettings(projectRoot string) (map[string]any, error) {
	path := filepath.Join(projectRoot, ".qwen", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

func ensureDefaultQwenSettings(settings map[string]any) {
	tools := ensureMap(settings, "tools")
	if _, ok := tools["approvalMode"]; !ok {
		tools["approvalMode"] = "yolo"
	}
	if _, ok := tools["experimentalLsp"]; !ok {
		tools["experimentalLsp"] = false
	}

	general := ensureMap(settings, "general")
	if _, ok := general["gitCoAuthor"]; !ok {
		general["gitCoAuthor"] = false
	}
	checkpointing := ensureMap(general, "checkpointing")
	if _, ok := checkpointing["enabled"]; !ok {
		checkpointing["enabled"] = true
	}
}

func mergeCubicleMCP(settings map[string]any, mcpURL string) {
	mcpServers := ensureMap(settings, "mcpServers")
	mcpServers["cubicleq"] = map[string]any{
		"httpUrl":     mcpURL,
		"trust":       true,
		"timeout":     600000,
		"description": "Cubicleq task reporting and control-plane tools",
		"includeTools": []string{
			"claim_task",
			"heartbeat",
			"block_task",
			"complete_task",
			"attach_artifact",
			"release_task",
		},
	}

	mcp := ensureMap(settings, "mcp")
	if allowedRaw, ok := mcp["allowed"]; ok {
		allowed := stringSlice(allowedRaw)
		found := false
		for _, a := range allowed {
			if a == "cubicleq" {
				found = true
				break
			}
		}
		if !found {
			mcp["allowed"] = append(allowed, "cubicleq")
		}
	}
	if excludedRaw, ok := mcp["excluded"]; ok {
		excluded := stringSlice(excludedRaw)
		filtered := make([]string, 0, len(excluded))
		for _, e := range excluded {
			if e != "cubicleq" {
				filtered = append(filtered, e)
			}
		}
		mcp["excluded"] = filtered
	}
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key]; ok {
		if out, ok := existing.(map[string]any); ok {
			return out
		}
	}
	out := map[string]any{}
	parent[key] = out
	return out
}

func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		if strings, ok := v.([]string); ok {
			return append([]string{}, strings...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func openWorkerLogs(root, taskID string) (*os.File, *os.File, error) {
	if err := os.MkdirAll(config.LogsDir(root), 0o755); err != nil {
		return nil, nil, err
	}
	stdoutPath := config.TaskLogPath(root, taskID, "stdout")
	stderrPath := config.TaskLogPath(root, taskID, "stderr")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, err
	}
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, nil, err
	}
	return stdoutFile, stderrFile, nil
}
