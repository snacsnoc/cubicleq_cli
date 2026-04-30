package validation

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

const (
	DefaultCommandTimeout   = 5 * time.Minute
	timeoutValidationStatus = "failed"
	timeoutExitCode         = 124
)

var commandTimeout = DefaultCommandTimeout

// SetCommandTimeoutForTest lets sibling package tests shorten the fixed timeout
// without adding a config surface.
func SetCommandTimeoutForTest(timeout time.Duration) func() {
	previous := commandTimeout
	commandTimeout = timeout
	return func() {
		commandTimeout = previous
	}
}

func MissingConfigRun(root string, task state.Task) (state.ValidationRun, error) {
	dir := state.ArtifactPath(root, task.ID, "validation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return state.ValidationRun{}, err
	}
	stdoutPath := filepath.Join(dir, fmt.Sprintf("%03d.%s", 1, "stdout.log"))
	stderrPath := filepath.Join(dir, fmt.Sprintf("%03d.%s", 1, "stderr.log"))
	summary := "no validation commands configured"
	if err := writeValidationLogs(stdoutPath, []byte{}, stderrPath, []byte(summary+"\n")); err != nil {
		return state.ValidationRun{}, err
	}
	return state.ValidationRun{
		TaskID:     task.ID,
		Command:    "validation:not-configured",
		ExitCode:   0,
		Status:     "skipped",
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		Summary:    summary,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

func Run(root string, task state.Task) ([]state.ValidationRun, error) {
	dir := state.ArtifactPath(root, task.ID, "validation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var runs []state.ValidationRun
	for i, command := range task.ValidationCommands {
		stdoutPath := filepath.Join(dir, fmt.Sprintf("%03d.%s", i+1, "stdout.log"))
		stderrPath := filepath.Join(dir, fmt.Sprintf("%03d.%s", i+1, "stderr.log"))
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		cmd := exec.CommandContext(ctx, "bash", "-lc", command)
		cmd.Dir = task.WorktreePath
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		timedOut := ctx.Err() == context.DeadlineExceeded
		cancel()
		if writeErr := writeValidationLogs(stdoutPath, stdout.Bytes(), stderrPath, stderr.Bytes()); writeErr != nil {
			return nil, writeErr
		}
		run := state.ValidationRun{
			TaskID:     task.ID,
			Command:    command,
			StdoutPath: stdoutPath,
			StderrPath: stderrPath,
			CreatedAt:  time.Now().UTC(),
		}
		if timedOut {
			run.ExitCode = timeoutExitCode
			run.Status = timeoutValidationStatus
			run.Summary = fmt.Sprintf("validation timed out after %s", commandTimeout)
		} else if err != nil {
			run.ExitCode = exitCode(err)
			run.Status = "failed"
			run.Summary = strings.TrimSpace(stderr.String())
		} else {
			run.ExitCode = 0
			run.Status = "passed"
			run.Summary = "validation passed"
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func writeValidationLogs(stdoutPath string, stdout []byte, stderrPath string, stderr []byte) error {
	if err := os.WriteFile(stdoutPath, stdout, 0o644); err != nil {
		return fmt.Errorf("write validation stdout log %s: %w", stdoutPath, err)
	}
	if err := os.WriteFile(stderrPath, stderr, 0o644); err != nil {
		return fmt.Errorf("write validation stderr log %s: %w", stderrPath, err)
	}
	return nil
}
