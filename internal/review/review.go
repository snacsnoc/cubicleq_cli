package review

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
	"github.com/snacsnoc/cubicleq_cli/internal/worktree"
)

type Artifacts struct {
	SummaryPath string
	DiffPath    string
}

var ErrMergeConflict = errors.New("merge conflict")
var ErrNoMergeChanges = errors.New("no committed task changes to merge")

var runtimeOwnedPaths = []string{
	".qwen/settings.json",
	".qwen/settings.json.orig",
}

type AcceptOptions struct {
	BaseBranch         string
	CleanupWorktree    bool
	MergeCommitMessage string
}

func Write(root string, task state.Task, validationResults []state.ValidationRun) (Artifacts, error) {
	if err := cleanupSnapshotInputs(task.WorktreePath); err != nil {
		return Artifacts{}, err
	}
	dir := state.ArtifactPath(root, task.ID, "")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Artifacts{}, err
	}
	diffPath := filepath.Join(dir, "diff-summary.md")
	summaryPath := filepath.Join(dir, "review-summary.md")
	diffOut, err := exec.Command("git", "-C", task.WorktreePath, "diff", "--stat").CombinedOutput()
	if err != nil {
		return Artifacts{}, fmt.Errorf("generate diff summary: %w", err)
	}
	if err := os.WriteFile(diffPath, diffOut, 0o644); err != nil {
		return Artifacts{}, err
	}
	var validationSummary []string
	for _, run := range validationResults {
		validationSummary = append(validationSummary, fmt.Sprintf("- %s: %s (exit=%d)", run.Command, run.Status, run.ExitCode))
	}
	summary := fmt.Sprintf(`# Review Summary

Task: %s
Title: %s

## Completion
%s

## Changed Files
%s

## Validation
%s
`, task.ID, task.Title, task.CompletionSummary, strings.Join(task.FilesChanged, ", "), strings.Join(validationSummary, "\n"))
	if err := os.WriteFile(summaryPath, []byte(summary), 0o644); err != nil {
		return Artifacts{}, err
	}
	return Artifacts{SummaryPath: summaryPath, DiffPath: diffPath}, nil
}

func Accept(root string, task state.Task, opts AcceptOptions) error {
	if strings.TrimSpace(task.WorktreePath) == "" {
		return errors.New("task has no worktree path")
	}
	if strings.TrimSpace(task.BranchName) == "" {
		return errors.New("task has no branch name")
	}
	if err := snapshotWorktree(task.WorktreePath, task.ID); err != nil {
		return err
	}
	if err := ensureCleanRepo(root); err != nil {
		return err
	}
	if opts.BaseBranch == "" {
		opts.BaseBranch = "main"
	}
	if opts.MergeCommitMessage == "" {
		opts.MergeCommitMessage = fmt.Sprintf("cubicleq: accept %s", task.ID)
	}
	baseHead, err := gitRevParse(root, opts.BaseBranch)
	if err != nil {
		return err
	}
	taskHead, err := gitRevParse(task.WorktreePath, "HEAD")
	if err != nil {
		return err
	}
	if baseHead == taskHead {
		return ErrNoMergeChanges
	}
	if err := runGit(root, "checkout", opts.BaseBranch); err != nil {
		return err
	}
	if err := mergeBranch(root, task.BranchName, opts.MergeCommitMessage); err != nil {
		return err
	}
	mergedHead, err := gitRevParse(root, "HEAD")
	if err != nil {
		return err
	}
	if mergedHead == baseHead {
		return ErrNoMergeChanges
	}
	if opts.CleanupWorktree {
		if err := worktree.Cleanup(root, task.WorktreePath); err != nil {
			return err
		}
	}
	return nil
}

func snapshotWorktree(worktreePath, taskID string) error {
	if err := cleanupSnapshotInputs(worktreePath); err != nil {
		return err
	}
	dirty, err := gitDirty(worktreePath)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if err := runGit(worktreePath, "add", "-A"); err != nil {
		return err
	}
	return runGitConfig(worktreePath,
		[]string{"user.name=Cubicleq", "user.email=cubicleq@local"},
		"commit", "-m", fmt.Sprintf("cubicleq: snapshot %s", taskID),
	)
}

func ensureCleanRepo(root string) error {
	if err := runGit(root, "diff", "--quiet"); err != nil {
		return errors.New("repo root has unstaged changes; commit or stash before accepting review")
	}
	if err := runGit(root, "diff", "--cached", "--quiet"); err != nil {
		return errors.New("repo root has staged changes; commit or stash before accepting review")
	}
	return nil
}

func mergeBranch(root, branchName, message string) error {
	cmd := exec.Command("git",
		"-c", "user.name=Cubicleq",
		"-c", "user.email=cubicleq@local",
		"merge", "--no-ff", "-m", message, branchName,
	)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		conflicts, listErr := gitConflictPaths(root)
		_ = runGit(root, "merge", "--abort")
		if stderr.Len() > 0 && strings.Contains(strings.ToLower(stderr.String()), "conflict") {
			msg := strings.TrimSpace(stderr.String())
			if listErr == nil && len(conflicts) > 0 {
				return fmt.Errorf("%w: %s (paths: %s)", ErrMergeConflict, msg, strings.Join(conflicts, ", "))
			}
			return fmt.Errorf("%w: %s", ErrMergeConflict, msg)
		}
		if listErr == nil && len(conflicts) > 0 {
			return fmt.Errorf("%w: conflicting paths: %s", ErrMergeConflict, strings.Join(conflicts, ", "))
		}
		return err
	}
	return nil
}

func cleanupSnapshotInputs(worktreePath string) error {
	// Snapshot commits should only capture task-owned changes.
	for _, rel := range runtimeOwnedPaths {
		if err := revertTrackedPath(worktreePath, rel); err != nil {
			return err
		}
	}

	untracked, err := gitUntrackedFiles(worktreePath)
	if err != nil {
		return err
	}
	pycacheDirs := map[string]struct{}{}
	for _, rel := range untracked {
		switch {
		case rel == ".qwen/settings.json" || rel == ".qwen/settings.json.orig":
			if err := os.Remove(filepath.Join(worktreePath, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		case strings.HasSuffix(rel, ".pyc"):
			if err := os.Remove(filepath.Join(worktreePath, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if dir, ok := pycacheDir(rel); ok {
			pycacheDirs[dir] = struct{}{}
		}
	}
	for dir := range pycacheDirs {
		if err := os.RemoveAll(filepath.Join(worktreePath, dir)); err != nil {
			return err
		}
	}
	return nil
}

func revertTrackedPath(worktreePath, rel string) error {
	tracked, err := gitTrackedPath(worktreePath, rel)
	if err != nil {
		return err
	}
	if !tracked {
		return nil
	}
	return runGit(worktreePath, "restore", "--staged", "--worktree", "--", rel)
}

func gitTrackedPath(root, rel string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", rel)
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func gitUntrackedFiles(root string) ([]string, error) {
	out, err := gitOutput(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	items := strings.Split(string(out), "\x00")
	files := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		files = append(files, item)
	}
	return files, nil
}

func pycacheDir(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		if part == "__pycache__" {
			return filepath.Join(parts[:i+1]...), true
		}
	}
	return "", false
}

func gitConflictPaths(root string) ([]string, error) {
	out, err := gitOutput(root, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}

func runGit(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return runCmd(cmd)
}

func runGitConfig(root string, cfg []string, args ...string) error {
	fullArgs := make([]string, 0, len(cfg)*2+len(args))
	for _, item := range cfg {
		fullArgs = append(fullArgs, "-c", item)
	}
	fullArgs = append(fullArgs, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Dir = root
	return runCmd(cmd)
}

func runCmd(cmd *exec.Cmd) error {
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

func gitDirty(root string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func gitRevParse(root, rev string) (string, error) {
	out, err := gitOutput(root, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}
