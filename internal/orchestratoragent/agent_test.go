package orchestratoragent

import "testing"

func TestParseResultExtractsStructuredActions(t *testing.T) {
	raw := []byte("```json\n{\"role\":\"orchestrator\",\"status\":\"complete\",\"actions\":[{\"type\":\"review_accept\",\"task_id\":\"t-1\"}],\"current_blockers\":\"None\",\"notes\":\"ok\"}\n```")
	result, err := parseResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 1 {
		t.Fatalf("expected one action, got %d", len(result.Actions))
	}
	if result.Actions[0].Type != "review_accept" || result.Actions[0].TaskID != "t-1" {
		t.Fatalf("unexpected parsed action: %#v", result.Actions[0])
	}
}
