package reporting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

type Server struct {
	store *state.Store
	http  *http.Server
	url   string
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewServer(store *state.Store) *Server {
	return &Server{store: store}
}

func ts() string {
	return time.Now().UTC().Format("[15:04:05]")
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.url = "http://" + ln.Addr().String() + "/mcp"
	s.http = &http.Server{Handler: mux}
	go s.http.Serve(ln)
	return nil
}

func (s *Server) URL() string { return s.url }

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}})
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	result, err := s.dispatch(req.Method, req.Params)
	if err != nil {
		resp.Error = &rpcError{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) dispatch(method string, params map[string]any) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "cubicleq-mcp",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}, nil
	case "notifications/initialized":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{
			"tools": cubicleqTools(),
		}, nil
	case "tools/call":
		name, _ := params["name"].(string)
		input, _ := params["arguments"].(map[string]any)
		return s.callTool(name, input)
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

func cubicleqTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "claim_task",
			"description": "Claim the assigned task before starting work.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID."},
					"agent":   map[string]any{"type": "string", "description": "Optional worker name."},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "heartbeat",
			"description": "Report that the task is still active.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID."},
					"summary": map[string]any{"type": "string", "description": "Short progress update."},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "block_task",
			"description": "Block the task with an explicit reason.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID."},
					"reason":  map[string]any{"type": "string", "description": "Blocker reason."},
				},
				"required": []string{"task_id", "reason"},
			},
		},
		{
			"name":        "complete_task",
			"description": "Mark the task complete and ready for validation.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":       map[string]any{"type": "string", "description": "Task ID."},
					"summary":       map[string]any{"type": "string", "description": "Completion summary."},
					"files_changed": map[string]any{"type": "array", "description": "Changed files.", "items": map[string]any{"type": "string"}},
					"test_results":  map[string]any{"type": "array", "description": "Validation or test results.", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"task_id", "summary"},
			},
		},
		{
			"name":        "attach_artifact",
			"description": "Attach an artifact path to the task record.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID."},
					"path":    map[string]any{"type": "string", "description": "Artifact path."},
					"kind":    map[string]any{"type": "string", "description": "Artifact kind."},
				},
				"required": []string{"task_id", "path"},
			},
		},
		{
			"name":        "release_task",
			"description": "Release the task without completing it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "Task ID."},
					"reason":  map[string]any{"type": "string", "description": "Release reason."},
				},
				"required": []string{"task_id"},
			},
		},
	}
}

func (s *Server) callTool(name string, input map[string]any) (any, error) {
	taskID := asString(input["task_id"])
	switch name {
	case "claim_task":
		agent := asString(input["agent"])
		if agent == "" {
			agent = "worker"
		}
		fmt.Printf("%s task %s claimed by %s\n", ts(), taskID, agent)
		if err := s.store.ClaimTask(taskID, agent); err != nil {
			return nil, err
		}
		return ok(taskID, name, nil, s.store)
	case "heartbeat":
		fmt.Printf("%s heartbeat from %s\n", ts(), taskID)
		if err := s.store.RecordHeartbeat(taskID); err != nil {
			return nil, err
		}
		return ok(taskID, name, input, s.store)
	case "block_task":
		reason := asString(input["reason"])
		if reason == "" {
			return nil, errors.New("reason is required")
		}
		fmt.Printf("%s task %s blocked: %s\n", ts(), taskID, reason)
		if err := s.store.BlockTask(taskID, reason); err != nil {
			return nil, err
		}
		return ok(taskID, name, input, s.store)
	case "complete_task":
		summary := asString(input["summary"])
		files := asStringSlice(input["files_changed"])
		results := asStringSlice(input["test_results"])
		fmt.Printf("%s task %s completed by worker\n", ts(), taskID)
		if err := s.store.CompleteTask(taskID, summary, files, results); err != nil {
			return nil, err
		}
		return ok(taskID, name, input, s.store)
	case "attach_artifact":
		if taskID == "" {
			return nil, errors.New("task_id is required")
		}
		if _, err := s.store.GetTask(taskID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("task %s not found", taskID)
			}
			return nil, err
		}
		path := asString(input["path"])
		if path == "" {
			return nil, errors.New("path is required")
		}
		kind := asString(input["kind"])
		if kind == "" {
			kind = "artifact"
		}
		if err := s.store.UpsertTaskArtifact(taskID, kind, path); err != nil {
			return nil, err
		}
		return ok(taskID, name, input, s.store)
	case "release_task":
		reason := asString(input["reason"])
		if err := s.store.ReleaseTask(taskID, reason); err != nil {
			return nil, err
		}
		return ok(taskID, name, input, s.store)
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func ok(taskID, name string, payload any, store *state.Store) (map[string]any, error) {
	if err := store.RecordEvent(taskID, name, payload); err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": "ok"}},
		"isError": false,
	}, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func CallTool(ctx context.Context, url, tool string, input map[string]any) error {
	client := &http.Client{Timeout: 5 * time.Second}
	if _, err := rpcCall(ctx, client, url, "initialize", map[string]any{}); err != nil {
		return err
	}
	_, err := rpcCall(ctx, client, url, "tools/call", map[string]any{
		"name":      tool,
		"arguments": input,
	})
	return err
}

func rpcCall(ctx context.Context, client *http.Client, url, method string, params map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      time.Now().UnixNano(),
		Method:  method,
		Params:  params,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var decoded rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if decoded.Error != nil {
		return nil, errors.New(decoded.Error.Message)
	}
	if decoded.Result == nil {
		return nil, nil
	}
	result, _ := decoded.Result.(map[string]any)
	return result, nil
}
