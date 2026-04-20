package review

import (
	"testing"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestWriteFailsWhenDiffSummaryCannotBeGenerated(t *testing.T) {
	root := t.TempDir()
	task := state.Task{
		ID:           "t-bad",
		Title:        "bad worktree",
		WorktreePath: root + "/missing-worktree",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if _, err := Write(root, task, nil); err == nil {
		t.Fatal("expected diff generation failure")
	}
}
