package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestAcceptRejectsNoOpBranch(t *testing.T) {
	root := t.TempDir()
	runGitTest(t, root, "init")
	runGitTest(t, root, "config", "user.name", "Test User")
	runGitTest(t, root, "config", "user.email", "test@example.com")
	writeTestFile(t, filepath.Join(root, "README.md"), "# fixture\n")
	runGitTest(t, root, "add", "README.md")
	runGitTest(t, root, "commit", "-m", "initial")
	branch := strings.TrimSpace(runGitOutput(t, root, "branch", "--show-current"))

	task := state.Task{
		ID:           "t-1",
		BranchName:   branch,
		WorktreePath: root,
	}
	err := Accept(root, task, AcceptOptions{BaseBranch: branch})
	if err == nil {
		t.Fatal("expected no-op merge error")
	}
	if !errors.Is(err, ErrNoMergeChanges) {
		t.Fatalf("expected ErrNoMergeChanges, got %v", err)
	}
}

func TestSnapshotWorktreeCleansRuntimeNoiseOnly(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)

	writeTestFile(t, filepath.Join(root, ".qwen", "settings.json"), "{\"durable\":true}\n")
	writeTestFile(t, filepath.Join(root, "app.py"), "print('base')\n")
	runGitTest(t, root, "add", ".")
	runGitTest(t, root, "commit", "-m", "base")

	writeTestFile(t, filepath.Join(root, ".qwen", "settings.json"), "{\"durable\":false}\n")
	writeTestFile(t, filepath.Join(root, ".qwen", "settings.json.orig"), "runtime backup\n")
	writeTestFile(t, filepath.Join(root, "__pycache__", "app.cpython-311.pyc"), "bytecode\n")
	writeTestFile(t, filepath.Join(root, "app.py"), "print('updated')\n")
	writeTestFile(t, filepath.Join(root, "feature.py"), "print('feature')\n")

	if err := snapshotWorktree(root, "t-1"); err != nil {
		t.Fatal(err)
	}

	headFiles := strings.Fields(runGitOutput(t, root, "show", "--pretty=", "--name-only", "HEAD"))
	assertContains(t, headFiles, "app.py")
	assertContains(t, headFiles, "feature.py")
	assertNotContains(t, headFiles, ".qwen/settings.json")
	assertNotContains(t, headFiles, ".qwen/settings.json.orig")
	assertNotContains(t, headFiles, "__pycache__/app.cpython-311.pyc")

	gotSettings := readTestFile(t, filepath.Join(root, ".qwen", "settings.json"))
	if gotSettings != "{\"durable\":true}\n" {
		t.Fatalf("expected tracked qwen settings to be restored, got %q", gotSettings)
	}
	if _, err := os.Stat(filepath.Join(root, ".qwen", "settings.json.orig")); !os.IsNotExist(err) {
		t.Fatalf("expected untracked qwen backup to be deleted, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "__pycache__")); !os.IsNotExist(err) {
		t.Fatalf("expected untracked __pycache__ to be deleted, got err=%v", err)
	}
}

func TestAcceptMergeConflictIncludesPaths(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	baseBranch := strings.TrimSpace(runGitOutput(t, root, "branch", "--show-current"))
	writeTestFile(t, filepath.Join(root, "README.md"), "base\n")
	runGitTest(t, root, "add", "README.md")
	runGitTest(t, root, "commit", "-m", "add readme")

	worktreePath := filepath.Join(root, "wt")
	runGitTest(t, root, "worktree", "add", "-b", "task/t-1", worktreePath, "HEAD")

	writeTestFile(t, filepath.Join(worktreePath, "README.md"), "task change\n")
	runGitTest(t, worktreePath, "add", "README.md")
	runGitTest(t, worktreePath, "commit", "-m", "task change")

	writeTestFile(t, filepath.Join(root, "README.md"), "main change\n")
	runGitTest(t, root, "add", "README.md")
	runGitTest(t, root, "commit", "-m", "main change")

	err := Accept(root, state.Task{
		ID:           "t-1",
		BranchName:   "task/t-1",
		WorktreePath: worktreePath,
	}, AcceptOptions{BaseBranch: baseBranch})
	if err == nil {
		t.Fatal("expected merge conflict")
	}
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("expected conflict paths in error, got %v", err)
	}
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	runGitTest(t, root, "init")
	runGitTest(t, root, "config", "user.name", "Test User")
	runGitTest(t, root, "config", "user.email", "test@example.com")
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("expected %q in %v", want, values)
}

func assertNotContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			t.Fatalf("did not expect %q in %v", want, values)
		}
	}
}
