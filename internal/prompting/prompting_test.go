package prompting

import (
	"os"
	"strings"
	"testing"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
)

func TestWriteOrchestratorBundleKeepsPromptFocusedOnStateAndActions(t *testing.T) {
	root := t.TempDir()
	policy := config.Policy{
		BaseBranch: "main",
		Orchestrator: config.OrchestratorPolicy{
			Enabled:        true,
			AllowedActions: []string{"review_reject", "retry_task", "resolve_blocker"},
		},
	}

	bundle, err := WriteOrchestratorBundle(root, policy, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bundle.PromptPath)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(raw)
	if !strings.Contains(prompt, "You are a privileged worker, not a second control plane.") {
		t.Fatalf("prompt missing worker-vs-control-plane boundary")
	}
	if !strings.Contains(prompt, "leave acceptable tasks in review and return no_action") {
		t.Fatalf("prompt missing no_action instruction")
	}
	if strings.Contains(prompt, "ALLOWED COMMANDS:") {
		t.Fatalf("prompt should not carry command-level orchestration scaffolding")
	}
	if !strings.Contains(prompt, `"actions": [`) {
		t.Fatalf("prompt missing structured actions schema")
	}
	if !strings.Contains(prompt, "STATE CONTEXT JSON:") {
		t.Fatalf("prompt missing embedded state context")
	}
	if !strings.Contains(prompt, `"policy": {`) {
		t.Fatalf("prompt missing serialized orchestrator policy context")
	}
}
