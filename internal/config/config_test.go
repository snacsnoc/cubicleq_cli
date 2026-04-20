package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPolicyDoesNotAutoRejectReview(t *testing.T) {
	policy := DefaultPolicy("main")
	for _, action := range AllowedActions(policy) {
		if action == "review_reject" {
			t.Fatalf("default policy should not default to review_reject")
		}
	}
}

func TestDefaultPinsWorktreeDirToRepoWorktrees(t *testing.T) {
	root := t.TempDir()
	cfg, err := Default(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "worktrees")
	if cfg.WorktreeDir != want {
		t.Fatalf("worktree dir = %q, want %q", cfg.WorktreeDir, want)
	}
}

func TestLoadRejectsNonRepoLocalWorktreeDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\"max_parallel_tasks\":2,\"worktree_dir\":\"/tmp/outside\",\"backend\":{\"command\":\"qwen\",\"args\":null}}")
	if err := os.WriteFile(filepath.Join(root, DirName, ConfigName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("expected invalid worktree_dir to be rejected")
	}
}

func TestLoadPolicyRejectsMissingExplicitFields(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"base_branch":"main","cleanup_worktree_on_accept":false,"orchestrator":{"enabled":true,"allowed_actions":[]}}`)
	if err := os.WriteFile(filepath.Join(root, DirName, PolicyName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(root); err == nil {
		t.Fatal("expected partial policy file to be rejected")
	}
}

func TestWriteDefaultPolicyRejectsMissingExplicitFields(t *testing.T) {
	root := t.TempDir()
	err := WriteDefaultPolicy(root, Policy{
		BaseBranch:              "main",
		CleanupWorktreeOnAccept: false,
		Orchestrator: OrchestratorPolicy{
			Enabled:        true,
			AllowedActions: nil,
		},
	})
	if err == nil {
		t.Fatal("expected missing explicit policy fields to be rejected")
	}
}
