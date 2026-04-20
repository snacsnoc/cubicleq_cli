package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBootstrapEnsureAndCleanup(t *testing.T) {
	root := t.TempDir()
	if err := BootstrapRepo(root); err != nil {
		t.Fatal(err)
	}
	worktreeRoot := filepath.Join(root, "worktrees")
	path, err := Ensure(root, worktreeRoot, BranchName("t-1", "example task"), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("expected worktree to exist: %v", err)
	}
	assertExcludeContains(t, path, "__pycache__/")
	assertExcludeContains(t, path, "*.pyc")
	if err := Cleanup(root, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected worktree path to be removed, got err=%v", err)
	}
}

func TestCleanupUsesExplicitRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := BootstrapRepo(root); err != nil {
		t.Fatal(err)
	}
	worktreeRoot := filepath.Join(root, "nested", "runtime", "worktrees")
	path, err := Ensure(root, worktreeRoot, BranchName("t-2", "nested task"), "t-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := Cleanup(root, path); err != nil {
		t.Fatal(err)
	}
}

func TestBranchNameUsesTaskPrefix(t *testing.T) {
	got := BranchName("t-1", "Hello, World!")
	want := "task/t-1-hello-world"
	if got != want {
		t.Fatalf("branch name = %q, want %q", got, want)
	}
}

func TestBootstrapCreatesInitialCommit(t *testing.T) {
	root := t.TempDir()
	if err := BootstrapRepo(root); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected HEAD after bootstrap: %v\n%s", err, string(out))
	}
}

func TestEnsureReusesExistingWorktreeWithoutChangingBranch(t *testing.T) {
	root := t.TempDir()
	if err := BootstrapRepo(root); err != nil {
		t.Fatal(err)
	}
	worktreeRoot := filepath.Join(root, "worktrees")
	path := filepath.Join(worktreeRoot, "t-1")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "worktree", "add", "-b", "unexpected-branch", path, "HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add failed: %v\n%s", err, string(out))
	}
	t.Cleanup(func() {
		cleanupCmd := exec.Command("git", "worktree", "remove", "--force", path)
		cleanupCmd.Dir = root
		_, _ = cleanupCmd.CombinedOutput()
	})

	got, err := Ensure(root, worktreeRoot, BranchName("t-1", "expected branch"), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("expected existing path %q, got %q", path, got)
	}

	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = path
	out, err := branchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --show-current failed: %v\n%s", err, string(out))
	}
	if string(bytesTrimSpace(out)) != "unexpected-branch" {
		t.Fatalf("expected existing branch to remain unchanged, got %q", string(bytesTrimSpace(out)))
	}
}

func assertExcludeContains(t *testing.T, worktreePath, want string) {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--git-path", "info/exclude")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse --git-path info/exclude failed: %v\n%s", err, string(out))
	}
	path := filepath.Join(worktreePath, string(bytesTrimSpace(out)))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(string(data), want) {
		t.Fatalf("expected %q in %s, got %q", want, path, string(data))
	}
}

func bytesTrimSpace(v []byte) []byte {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\n' || v[0] == '\t' || v[0] == '\r') {
		v = v[1:]
	}
	for len(v) > 0 {
		last := v[len(v)-1]
		if last != ' ' && last != '\n' && last != '\t' && last != '\r' {
			break
		}
		v = v[:len(v)-1]
	}
	return v
}
