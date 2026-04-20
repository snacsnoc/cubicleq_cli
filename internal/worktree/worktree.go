package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func BranchName(taskID, title string) string {
	slug := slugify(title)
	if slug == "" {
		slug = "task"
	}
	return fmt.Sprintf("task/%s-%s", taskID, slug)
}

func Ensure(repoRoot, worktreeRoot, branchName, taskID string) (string, error) {
	path := filepath.Join(worktreeRoot, taskID)
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		if err := ensureLocalIgnore(path); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := ensureHead(repoRoot); err != nil {
		return "", err
	}

	if err := runGit(repoRoot, "worktree", "add", "-b", branchName, path, "HEAD"); err == nil {
		if err := ensureLocalIgnore(path); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := runGit(repoRoot, "worktree", "add", path, branchName); err == nil {
		if err := ensureLocalIgnore(path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("unable to create worktree %s for branch %s", path, branchName)
}

func BootstrapRepo(repoRoot string) error {
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		return err
	}
	if !isGitRepo(repoRoot) {
		if err := runGit(repoRoot, "init"); err != nil {
			return err
		}
	}
	if err := ensureInitialCommit(repoRoot); err != nil {
		return err
	}
	return nil
}

func Cleanup(repoRoot, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := runGit(repoRoot, "worktree", "remove", "--force", path); err != nil {
		return err
	}
	return nil
}

func ensureHead(repoRoot string) error {
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = repoRoot
	if err := cmd.Run(); err != nil {
		return errors.New("git repository needs an initial commit before worktrees can be created")
	}
	return nil
}

func ensureInitialCommit(repoRoot string) error {
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = repoRoot
	if err := cmd.Run(); err == nil {
		return nil
	}
	return runGit(repoRoot,
		"-c", "user.name=Cubicleq",
		"-c", "user.email=cubicleq@local",
		"commit", "--allow-empty", "-m", "cubicleq bootstrap")
}

func isGitRepo(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, ".git"))
	return err == nil
}

func runGit(repoRoot string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func ensureLocalIgnore(worktreePath string) error {
	excludePath, err := gitPath(worktreePath, "info/exclude")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	existing := ""
	if data, err := os.ReadFile(excludePath); err == nil {
		existing = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	patterns := []string{"__pycache__/", "*.pyc"}
	var additions []string
	for _, pattern := range patterns {
		if !containsLine(existing, pattern) {
			additions = append(additions, pattern)
		}
	}
	if len(additions) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteByte('\n')
	}
	for _, pattern := range additions {
		b.WriteString(pattern)
		b.WriteByte('\n')
	}
	return os.WriteFile(excludePath, []byte(b.String()), 0o644)
}

func gitPath(root, rel string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-path", rel)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-path %s: %w: %s", rel, err, strings.TrimSpace(string(out)))
	}
	return filepath.Join(root, strings.TrimSpace(string(out))), nil
}

func containsLine(contents, want string) bool {
	for _, line := range strings.Split(contents, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
