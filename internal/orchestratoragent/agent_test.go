package orchestratoragent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
)

func TestParseResultExtractsStructuredActions(t *testing.T) {
	raw := []byte("```json\n{\"role\":\"orchestrator\",\"status\":\"complete\",\"actions\":[{\"type\":\"review_accept\",\"task_id\":\"t-1\"}],\"current_blockers\":\"None\",\"notes\":\"ok\"}\n```")
	result, err := parseResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected one action, got %d", len(result.Actions))
	}
	if result.Actions[0].Type != "review_accept" || result.Actions[0].TaskID != "t-1" {
		t.Fatalf("unexpected parsed action: %#v", result.Actions[0])
	}
}

func TestBuildCommandUsesProjectScopedQwenRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(config.QwenDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	projectSettings := `{
  "modelProviders": {
    "openai": [
      {
        "id": "BytePlus-Seed",
        "envKey": "ARK_API_KEY",
        "baseUrl": "https://ark.cn-beijing.volces.com/api/v3"
      }
    ]
  },
  "security": {
    "auth": {
      "selectedType": "openai"
    }
  },
  "model": {
    "name": "BytePlus-Seed"
  }
}
`
	if err := os.WriteFile(config.QwenSettingsPath(root), []byte(projectSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("plan the next action"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Backend: config.BackendConfig{Command: "sh"},
	}
	cmd, _, _, cleanup, err := buildCommand(context.Background(), root, "/tmp/cubicleq", cfg, promptPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if cmd.Dir != root {
		t.Fatalf("expected orchestrator cwd to stay at repo root, got %q", cmd.Dir)
	}
	settingsPath := config.OrchestratorRuntimeQwenSettingsPath(root)
	if lookupEnv(cmd.Env, "QWEN_CODE_SYSTEM_SETTINGS_PATH") != settingsPath {
		t.Fatalf("expected orchestrator env to point at runtime qwen settings %q, got %q", settingsPath, lookupEnv(cmd.Env, "QWEN_CODE_SYSTEM_SETTINGS_PATH"))
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	model := got["model"].(map[string]any)
	if model["name"] != "BytePlus-Seed" {
		t.Fatalf("expected orchestrator runtime model selection to be preserved, got %#v", model["name"])
	}
	security := got["security"].(map[string]any)
	auth := security["auth"].(map[string]any)
	if auth["selectedType"] != "openai" {
		t.Fatalf("expected orchestrator runtime auth type to be preserved, got %#v", auth["selectedType"])
	}
	modelProviders := got["modelProviders"].(map[string]any)
	openaiProviders := modelProviders["openai"].([]any)
	provider := openaiProviders[0].(map[string]any)
	if provider["baseUrl"] != "https://ark.cn-beijing.volces.com/api/v3" {
		t.Fatalf("expected orchestrator runtime provider baseUrl to be preserved, got %#v", provider["baseUrl"])
	}
	if _, ok := got["mcpServers"]; ok {
		t.Fatalf("did not expect worker MCP settings to be injected into orchestrator runtime settings")
	}
}

func lookupEnv(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
