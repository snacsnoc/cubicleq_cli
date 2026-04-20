package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestMissingConfigRunFailsWhenStdoutLogCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	task := state.Task{ID: "t-1"}
	dir := state.ArtifactPath(root, task.ID, "validation")
	if err := os.MkdirAll(filepath.Join(dir, "001.stdout.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := MissingConfigRun(root, task)
	if err == nil || !strings.Contains(err.Error(), "write validation stdout log") {
		t.Fatalf("expected stdout log write failure, got %v", err)
	}
}

func TestRunFailsWhenStdoutLogCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	task := state.Task{
		ID:                 "t-1",
		WorktreePath:       worktree,
		ValidationCommands: []string{"printf ok"},
	}
	dir := state.ArtifactPath(root, task.ID, "validation")
	if err := os.MkdirAll(filepath.Join(dir, "001.stdout.log"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, task)
	if err == nil || !strings.Contains(err.Error(), "write validation stdout log") {
		t.Fatalf("expected stdout log write failure, got %v", err)
	}
}
