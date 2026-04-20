package reporting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

func TestCubicleToolsIncludeSchemas(t *testing.T) {
	tools := cubicleqTools()
	if len(tools) == 0 {
		t.Fatal("expected cubicleq tools to be advertised")
	}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("tool missing name: %#v", tool)
		}
		if _, ok := tool["description"].(string); !ok {
			t.Fatalf("tool %s missing description", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("tool %s missing inputSchema", name)
		}
		if schema["type"] != "object" {
			t.Fatalf("tool %s schema type = %#v", name, schema["type"])
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) == 0 {
			t.Fatalf("tool %s missing properties", name)
		}
		if _, ok := properties["task_id"]; !ok {
			t.Fatalf("tool %s missing task_id property", name)
		}
	}
}

func TestAttachArtifactRequiresTaskID(t *testing.T) {
	store := testStore(t)
	server := NewServer(store)

	_, err := server.callTool("attach_artifact", map[string]any{
		"path": "notes.txt",
		"kind": "checkpoint",
	})
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected missing task_id error, got %v", err)
	}
}

func TestAttachArtifactRejectsUnknownTaskID(t *testing.T) {
	store := testStore(t)
	server := NewServer(store)

	_, err := server.callTool("attach_artifact", map[string]any{
		"task_id": "t-missing",
		"path":    "notes.txt",
		"kind":    "checkpoint",
	})
	if err == nil || !strings.Contains(err.Error(), "task t-missing not found") {
		t.Fatalf("expected unknown task error, got %v", err)
	}
}

func TestAttachArtifactStoresArtifactForKnownTask(t *testing.T) {
	store := testStore(t)
	server := NewServer(store)

	now := time.Now().UTC()
	if err := store.AddTask(state.Task{
		ID:        "t-1",
		Title:     "task",
		Priority:  "medium",
		State:     state.TaskStateTodo,
		RoleHint:  "implementer",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := server.callTool("attach_artifact", map[string]any{
		"task_id": "t-1",
		"path":    "notes.txt",
		"kind":    "checkpoint",
	})
	if err != nil {
		t.Fatal(err)
	}

	artifacts, err := store.ListTaskArtifacts("t-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected one artifact, got %#v", artifacts)
	}
	if artifacts[0].Kind != "checkpoint" || artifacts[0].Path != "notes.txt" {
		t.Fatalf("unexpected artifact: %#v", artifacts[0])
	}
}

func testStore(t *testing.T) *state.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cubicleq"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitSchema(); err != nil {
		t.Fatal(err)
	}
	return store
}
