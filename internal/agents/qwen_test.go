package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
)

func TestWriteQwenSettingsInheritsProjectSettings(t *testing.T) {
	projectRoot := t.TempDir()
	worktree := t.TempDir()
	projectQwenDir := filepath.Join(projectRoot, ".qwen")
	if err := os.MkdirAll(projectQwenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"tools": map[string]any{
			"approvalMode":    "yolo",
			"experimentalLsp": false,
		},
		"model": map[string]any{
			"name": "qwen3.6-plus",
		},
		"general": map[string]any{
			"gitCoAuthor": false,
			"checkpointing": map[string]any{
				"enabled": true,
			},
		},
		"mcpServers": map[string]any{
			"hanzi-browser": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "hanzi-browse"},
			},
		},
		"mcp": map[string]any{
			"excluded": []string{"hanzi-browser", "cubicleq"},
			"allowed":  []string{"hanzi-browser"},
		},
	}
	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectQwenDir, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeQwenSettings(projectRoot, worktree, "http://127.0.0.1:9999/mcp"); err != nil {
		t.Fatal(err)
	}

	rootRaw, err := os.ReadFile(filepath.Join(projectQwenDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rootRaw) != string(data) {
		t.Fatalf("expected project qwen settings to remain unchanged")
	}

	raw, err := os.ReadFile(filepath.Join(worktree, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	model := ensureMap(got, "model")
	if model["name"] != "qwen3.6-plus" {
		t.Fatalf("expected model to be preserved, got %#v", model["name"])
	}
	tools := ensureMap(got, "tools")
	if tools["approvalMode"] != "yolo" {
		t.Fatalf("expected approvalMode to be preserved, got %#v", tools["approvalMode"])
	}
	mcpServers := ensureMap(got, "mcpServers")
	if _, ok := mcpServers["hanzi-browser"]; !ok {
		t.Fatalf("expected existing mcp server to be preserved")
	}
	cubicleq, ok := mcpServers["cubicleq"].(map[string]any)
	if !ok {
		t.Fatalf("expected cubicleq mcp server to be injected")
	}
	if cubicleq["httpUrl"] != "http://127.0.0.1:9999/mcp" {
		t.Fatalf("unexpected cubicleq httpUrl: %#v", cubicleq["httpUrl"])
	}
	if cubicleq["trust"] != true {
		t.Fatalf("expected cubicleq server to be trusted, got %#v", cubicleq["trust"])
	}
	if cubicleq["timeout"] != float64(600000) {
		t.Fatalf("expected cubicleq timeout to be set, got %#v", cubicleq["timeout"])
	}
	if cubicleq["description"] == "" {
		t.Fatalf("expected cubicleq description to be set")
	}
	includeTools, ok := cubicleq["includeTools"].([]any)
	if !ok || len(includeTools) == 0 {
		t.Fatalf("expected includeTools to be set, got %#v", cubicleq["includeTools"])
	}
	mcp := ensureMap(got, "mcp")
	excluded := stringSlice(mcp["excluded"])
	for _, item := range excluded {
		if item == "cubicleq" {
			t.Fatalf("expected cubicleq to be removed from mcp.excluded")
		}
	}
	allowed := stringSlice(mcp["allowed"])
	found := false
	for _, item := range allowed {
		if item == "cubicleq" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cubicleq to be added to mcp.allowed")
	}
}

func TestWriteQwenSettingsDefaultsWhenMissing(t *testing.T) {
	projectRoot := t.TempDir()
	worktree := t.TempDir()
	if err := writeQwenSettings(projectRoot, worktree, "http://127.0.0.1:8888/mcp"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(worktree, ".qwen", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	tools := ensureMap(got, "tools")
	if tools["approvalMode"] != "yolo" {
		t.Fatalf("expected default approvalMode yolo, got %#v", tools["approvalMode"])
	}
	general := ensureMap(got, "general")
	checkpointing := ensureMap(general, "checkpointing")
	if checkpointing["enabled"] != true {
		t.Fatalf("expected checkpointing enabled by default, got %#v", checkpointing["enabled"])
	}
}

func TestOpenWorkerLogsTruncatesPreviousAttemptOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, config.DirName), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := openWorkerLogs(root, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdout.WriteString("first stdout\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.WriteString("first stderr\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err = openWorkerLogs(root, "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdout.WriteString("second stdout\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.WriteString("second stderr\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}

	stdoutRaw, err := os.ReadFile(config.TaskLogPath(root, "t-1", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(stdoutRaw); got != "second stdout\n" {
		t.Fatalf("expected stdout log to be truncated on relaunch, got %q", got)
	}

	stderrRaw, err := os.ReadFile(config.TaskLogPath(root, "t-1", "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(stderrRaw); got != "second stderr\n" {
		t.Fatalf("expected stderr log to be truncated on relaunch, got %q", got)
	}
}
