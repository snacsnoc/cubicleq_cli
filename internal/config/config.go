package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DirName    = ".cubicleq"
	ConfigName = "config.json"
	PolicyName = "policy.json"
)

type Config struct {
	MaxParallelTasks int           `json:"max_parallel_tasks"`
	WorktreeDir      string        `json:"worktree_dir"`
	Backend          BackendConfig `json:"backend"`
}

type BackendConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type Policy struct {
	BaseBranch              string             `json:"base_branch"`
	CleanupWorktreeOnAccept bool               `json:"cleanup_worktree_on_accept"`
	RejectionTargetState    string             `json:"rejection_target_state"`
	Orchestrator            OrchestratorPolicy `json:"orchestrator"`
}

type OrchestratorPolicy struct {
	Enabled        bool     `json:"enabled"`
	AllowedActions []string `json:"allowed_actions"`
}

func Default(root string) (Config, error) {
	worktreeDir, err := normalizeWorktreeDir(root, filepath.Join(root, "worktrees"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		MaxParallelTasks: 2,
		WorktreeDir:      worktreeDir,
		Backend: BackendConfig{
			Command: "qwen",
			Args:    nil,
		},
	}, nil
}

func DefaultPolicy(baseBranch string) Policy {
	baseBranch = normalizeBaseBranch(baseBranch)
	return Policy{
		BaseBranch:              baseBranch,
		CleanupWorktreeOnAccept: false,
		RejectionTargetState:    "todo",
		Orchestrator: OrchestratorPolicy{
			Enabled: true,
			AllowedActions: []string{
				"retry_task",
				"resolve_blocker",
				"merge_branch",
			},
		},
	}
}

func Load(root string) (Config, error) {
	path := filepath.Join(root, DirName, ConfigName)
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.WorktreeDir, err = normalizeWorktreeDir(root, cfg.WorktreeDir)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func WriteDefault(root string, cfg Config) error {
	worktreeDir, err := normalizeWorktreeDir(root, cfg.WorktreeDir)
	if err != nil {
		return err
	}
	cfg.WorktreeDir = worktreeDir
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.WorktreeDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ConfigName), data, 0o644)
}

func normalizeWorktreeDir(root, value string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("repo root is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	expected := filepath.Clean(filepath.Join(rootAbs, "worktrees"))
	if strings.TrimSpace(value) == "" {
		return expected, nil
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(rootAbs, value)
	}
	valueAbs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	valueAbs = filepath.Clean(valueAbs)
	if valueAbs != expected {
		return "", fmt.Errorf("worktree_dir must be repo-local at %s", expected)
	}
	return valueAbs, nil
}

func LoadPolicy(root string) (Policy, error) {
	path := filepath.Join(root, DirName, PolicyName)
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, err
	}
	policy.BaseBranch = normalizeBaseBranch(policy.BaseBranch)
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func WriteDefaultPolicy(root string, policy Policy) error {
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	policy.BaseBranch = normalizeBaseBranch(policy.BaseBranch)
	if err := validatePolicy(policy); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, PolicyName), data, 0o644)
}

func DBPath(root string) string {
	return filepath.Join(root, DirName, "state.db")
}

func RunsDir(root string) string {
	return filepath.Join(root, DirName, "runs")
}

func LogsDir(root string) string {
	return filepath.Join(root, DirName, "logs")
}

func TaskLogPath(root, taskID, stream string) string {
	return filepath.Join(LogsDir(root), taskID+"."+stream+".log")
}

func PolicyPath(root string) string {
	return filepath.Join(root, DirName, PolicyName)
}

func QwenDir(root string) string {
	return filepath.Join(root, ".qwen")
}

func QwenSettingsPath(root string) string {
	return filepath.Join(QwenDir(root), "settings.json")
}

func QwenEnvPath(root string) string {
	return filepath.Join(QwenDir(root), ".env")
}

func OrchestratorRuntimeDir(root string) string {
	return filepath.Join(RunsDir(root), "orchestrator")
}

func OrchestratorRuntimeQwenSettingsPath(root string) string {
	return filepath.Join(OrchestratorRuntimeDir(root), ".qwen", "settings.json")
}

func AllowedActions(policy Policy) []string {
	return append([]string{}, policy.Orchestrator.AllowedActions...)
}

func AllowsAction(policy Policy, action string) bool {
	for _, allowed := range policy.Orchestrator.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}

func validatePolicy(policy Policy) error {
	if strings.TrimSpace(policy.RejectionTargetState) == "" {
		return fmt.Errorf("policy rejection_target_state is required")
	}
	if len(policy.Orchestrator.AllowedActions) == 0 {
		return fmt.Errorf("policy orchestrator.allowed_actions is required")
	}
	return nil
}

func normalizeBaseBranch(baseBranch string) string {
	if baseBranch == "" {
		return "main"
	}
	return baseBranch
}
