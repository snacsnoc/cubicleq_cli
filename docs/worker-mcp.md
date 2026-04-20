# worker-mcp - embedded MCP reporting tools

## DESCRIPTION

Workers report through the embedded MCP server.
Tools mutate durable state.

## TOOL REFERENCE

| Tool | Effect |
|---|---|
| `claim_task` | task -> `running`, runtime -> `running`, event written |
| `heartbeat` | runtime heartbeat updated, event written |
| `block_task` | blocker persisted, task -> `blocked`, runtime deleted, event written |
| `complete_task` | completion metadata stored, runtime -> `completed`, event written |
| `attach_artifact` | artifact row upserted, event written |
| `release_task` | blocker persisted, task -> `blocked`, runtime deleted, event written |

## GUARDS

- `claim_task` fails unless task is `ready` and runtime is `launching`
- `heartbeat` fails unless task and runtime are actively running
- `block_task` fails unless task is `running`
- `complete_task` fails unless task is `running`
- `attach_artifact` fails if `task_id` is missing or unknown
- `release_task` fails when runtime row is missing

## WARNINGS

Tool names are part of the runtime contract. Keep names stable unless worker prompts, tests, and reporting server are updated together.
