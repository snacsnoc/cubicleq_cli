package prompting

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
	"github.com/snacsnoc/cubicleq_cli/internal/state"
)

type Bundle struct {
	PromptPath string
}

func WriteBundle(root string, task state.Task) (Bundle, error) {
	dir := state.ArtifactPath(root, task.ID, "")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Bundle{}, err
	}
	promptPath := filepath.Join(dir, "prompt.md")
	contextPath := filepath.Join(dir, "context.json")
	prompt := fmt.Sprintf(`ROLE: %s
GOAL: Complete task %s and leave it ready for Cubicleq validation and review.

TASK:
- ID: %s
- Title: %s

DESCRIPTION:
%s

VALIDATION:
%v

WORKSPACE:
- Work only inside the assigned worktree.

REPORTING CONTRACT:
- claim_task before substantial work
- heartbeat for progress
- block_task when you cannot proceed
- release_task when control should return to the orchestrator
- complete_task only when the task is genuinely ready

OUTPUT:
- Do not narrate reasoning
- Prefer direct edits, checks, and structured reporting
`, task.RoleHint, task.ID, task.ID, task.Title, task.Description, task.ValidationCommands)
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return Bundle{}, err
	}
	contextData, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return Bundle{}, err
	}
	if err := os.WriteFile(contextPath, contextData, 0o644); err != nil {
		return Bundle{}, err
	}
	return Bundle{PromptPath: promptPath}, nil
}

type OrchestratorContext struct {
	Policy   config.Policy   `json:"policy"`
	Tasks    []state.Task    `json:"tasks"`
	Runtimes []state.Runtime `json:"runtimes"`
	Blockers []state.Blocker `json:"blockers"`
	Reviews  []state.Review  `json:"reviews"`
}

func WriteOrchestratorBundle(root string, policy config.Policy, tasks []state.Task, runtimes []state.Runtime, blockers []state.Blocker, reviews []state.Review) (Bundle, error) {
	dir := filepath.Join(config.RunsDir(root), "orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Bundle{}, err
	}
	promptPath := filepath.Join(dir, "prompt.md")
	contextPath := filepath.Join(dir, "context.json")
	context := OrchestratorContext{
		Policy:   policy,
		Tasks:    tasks,
		Runtimes: runtimes,
		Blockers: blockers,
		Reviews:  reviews,
	}
	contextData, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return Bundle{}, err
	}
	prompt := fmt.Sprintf(`ROLE: orchestrator
GOAL: Read Cubicleq state and return a small set of policy-allowed structured actions.

MISSION:
- Cubicleq remains the control plane.
- You are a privileged worker, not a second control plane.
- You do not write feature code directly.

POLICY:
- Base branch: %s
- Allowed actions: %v

RULES:
- Use only the provided state/context to decide actions.
- Prefer no_action over speculative actions.
- Use review_reject only for concrete defects or failed validation.
- If review_accept is not allowed, leave acceptable tasks in review and return no_action.
- Create follow-up tasks only when the existing review or blocker evidence clearly implies next work.
- Every follow-up implementation task must include at least one concrete validation command.

OUTPUT:
- Output ONLY raw JSON in this exact schema:
- {
-   "role": "orchestrator",
-   "status": "complete|blocked|no_action",
-   "actions": [
-     {
-       "type": "review_accept|review_reject|retry_task|resolve_blocker|create_followup_task",
-       "task_id": "<task id when applicable>",
-       "note": "<required for review_reject when applicable>",
-       "title": "<required for create_followup_task>",
-       "description": "<optional follow-up task description>",
-       "role": "implementer|orchestrator",
-       "depends_on": ["<optional dependency ids>"],
-       "validation_commands": ["<required for create_followup_task>"]
-     }
-   ],
-   "current_blockers": "<Describe active blockers or 'None'>",
-   "notes": "<Short execution note or 'None'>"
- }

STATE CONTEXT JSON:
%s
`, policy.BaseBranch, config.AllowedActions(policy), string(contextData))
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return Bundle{}, err
	}
	if err := os.WriteFile(contextPath, contextData, 0o644); err != nil {
		return Bundle{}, err
	}
	return Bundle{PromptPath: promptPath}, nil
}
