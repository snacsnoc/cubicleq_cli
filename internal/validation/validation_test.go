package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRunTimesOutWithCapturedLogsAndDeterministicSummary(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	task := state.Task{
		ID:                 "t-timeout",
		WorktreePath:       worktree,
		ValidationCommands: []string{"printf before-out; printf before-err >&2; sleep 5"},
	}

	restore := SetCommandTimeoutForTest(50 * time.Millisecond)
	defer restore()

	runs, err := Run(root, task)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one validation run, got %d", len(runs))
	}

	run := runs[0]
	if run.Status != "failed" {
		t.Fatalf("expected failed status, got %q", run.Status)
	}
	if run.ExitCode != 124 {
		t.Fatalf("expected timeout exit code 124, got %d", run.ExitCode)
	}
	if run.Summary != "validation timed out after 50ms" {
		t.Fatalf("unexpected timeout summary: %q", run.Summary)
	}

	stdout, err := os.ReadFile(run.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "before-out" {
		t.Fatalf("unexpected stdout log: %q", string(stdout))
	}

	stderr, err := os.ReadFile(run.StderrPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stderr) != "before-err" {
		t.Fatalf("unexpected stderr log: %q", string(stderr))
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
