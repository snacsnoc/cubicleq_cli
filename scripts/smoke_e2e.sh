#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/smoke_e2e.sh [--keep] [--repo-dir PATH] [--skip-orchestrate]

Builds cubicleq, creates a scratch repo, runs a fixture-backed end-to-end flow,
and verifies:
  - one task reaches review and is merged by the orchestrator fixture
  - one task becomes blocked with an explicit reason
  - the Qwen adapter path writes runtime-local worktree `.qwen/settings.json`
  - review artifacts and final merged output exist

Options:
  --keep               Keep the scratch repo after success
  --repo-dir PATH      Use a specific new or empty repo path; never auto-deleted
  --skip-orchestrate   Stop after `cubicleq run` and review/blocker verification
  --help               Show this help text
EOF
}

log() {
  printf '==> %s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_contains() {
  local haystack="$1"
  local needle="$2"
  local context="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    printf 'error: expected %s to contain %q\n' "$context" "$needle" >&2
    printf '--- %s ---\n%s\n' "$context" "$haystack" >&2
    exit 1
  fi
}

json_escape() {
  local raw="$1"
  raw=${raw//\\/\\\\}
  raw=${raw//\"/\\\"}
  raw=${raw//$'\n'/\\n}
  raw=${raw//$'\r'/\\r}
  raw=${raw//$'\t'/\\t}
  printf '%s' "${raw}"
}

extract_mcp_url() {
  local run_output="$1"
  local line
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    if [[ "${line}" == mcp\ server\ listening\ on\ * ]]; then
      printf '%s' "${line#mcp server listening on }"
      return 0
    fi
  done <<< "${run_output}"
  return 1
}

write_backend_config() {
  local command="$1"
  local escaped_worktree_dir escaped_command
  escaped_worktree_dir="$(json_escape "${SCRATCH_REPO}/worktrees")"
  escaped_command="$(json_escape "${command}")"
  cat > "${SCRATCH_REPO}/.cubicleq/config.json" <<EOF
{
  "max_parallel_tasks": 2,
  "worktree_dir": "${escaped_worktree_dir}",
  "backend": {
    "command": "${escaped_command}",
    "args": []
  }
}
EOF
}

write_policy() {
  cat > "${SCRATCH_REPO}/.cubicleq/policy.json" <<EOF
{
  "base_branch": "main",
  "cleanup_worktree_on_accept": false,
  "rejection_target_state": "todo",
  "orchestrator": {
    "enabled": true,
    "allowed_actions": [
      "review_accept",
      "review_reject",
      "retry_task",
      "resolve_blocker",
      "merge_branch"
    ]
  }
}
EOF
}

run_cubicleq() {
  "${BIN_PATH}" --root "${SCRATCH_REPO}" "$@"
}

KEEP_REPO=0
RUN_ORCHESTRATOR=1
SCRATCH_REPO=""
SCRIPT_CREATED_REPO=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep)
      KEEP_REPO=1
      shift
      ;;
    --repo-dir)
      [[ $# -ge 2 ]] || fail "--repo-dir requires a path"
      SCRATCH_REPO="$2"
      shift 2
      ;;
    --skip-orchestrate)
      RUN_ORCHESTRATOR=0
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
BIN_PATH="${REPO_ROOT}/bin/cubicleq"

cleanup() {
  local exit_code=$?
  if [[ -z "${SCRATCH_REPO}" ]]; then
    exit "${exit_code}"
  fi
  if [[ ${exit_code} -ne 0 || ${KEEP_REPO} -eq 1 || ${SCRIPT_CREATED_REPO} -ne 1 ]]; then
    printf 'scratch repo kept at %s\n' "${SCRATCH_REPO}"
  else
    rm -rf "${SCRATCH_REPO}"
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

mkdir -p "${REPO_ROOT}/.cache/go-build" "${REPO_ROOT}/.cache/go-mod" "${REPO_ROOT}/.cache/go-tmp" "${REPO_ROOT}/bin"
export GOCACHE="${REPO_ROOT}/.cache/go-build"
export GOMODCACHE="${REPO_ROOT}/.cache/go-mod"
export GOTMPDIR="${REPO_ROOT}/.cache/go-tmp"
export GOSUMDB=off

if [[ -z "${SCRATCH_REPO}" ]]; then
  SCRATCH_REPO="$(mktemp -d "${TMPDIR:-/tmp}/cubicleq-smoke.XXXXXX")"
  SCRIPT_CREATED_REPO=1
else
  mkdir -p "${SCRATCH_REPO}"
  if [[ -n "$(find "${SCRATCH_REPO}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
    fail "--repo-dir must point to a new or empty directory"
  fi
fi
SCRATCH_REPO="$(cd "${SCRATCH_REPO}" && pwd -P)"

log "building cubicleq"
(cd "${REPO_ROOT}" && go build -o "${BIN_PATH}" ./cmd/cubicleq)

log "bootstrapping scratch repo at ${SCRATCH_REPO}"
git -C "${SCRATCH_REPO}" init -b main >/dev/null
git -C "${SCRATCH_REPO}" config user.name "Cubicleq Smoke"
git -C "${SCRATCH_REPO}" config user.email "cubicleq-smoke@example.com"
run_cubicleq init --bootstrap-git >/dev/null
mkdir -p "${SCRATCH_REPO}/.qwen"
cat > "${SCRATCH_REPO}/.qwen/settings.json" <<'EOF'
{
  "$version": 3,
  "model": {
    "name": "qwen3.6-plus"
  },
  "tools": {
    "approvalMode": "yolo",
    "experimentalLsp": false
  },
  "general": {
    "gitCoAuthor": false,
    "checkpointing": {
      "enabled": true
    }
  }
}
EOF
ROOT_QWEN_SETTINGS="$(cat "${SCRATCH_REPO}/.qwen/settings.json")"
PROJECT_NAME="todo-cli"
PROJECT_DIR="${SCRATCH_REPO}/${PROJECT_NAME}"
mkdir -p "${PROJECT_DIR}"
cat <<'EOF' > "${PROJECT_DIR}/README.md"
# Todo CLI

This stub represents the project data the worker will update.
EOF
cat <<'EOF' > "${PROJECT_DIR}/todo.py"
import json
import pathlib
import sys
from datetime import datetime

TODO_FILE = pathlib.Path(__file__).with_suffix('.json')
TODO_FILE.write_text('[]') if not TODO_FILE.exists() else None
data = json.loads(TODO_FILE.read_text())

if len(sys.argv) >= 2 and sys.argv[1] == "add":
    entry = {
        "task": sys.argv[2] if len(sys.argv) >= 3 else "smoke entry",
        "created": datetime.utcnow().isoformat() + "Z",
    }
    data.append(entry)
    TODO_FILE.write_text(json.dumps(data, indent=2))
    print("added", entry["task"])
else:
    print(json.dumps(data, indent=2))

EOF

WORKER_PATH="${SCRATCH_REPO}/fixture-qwen.sh"
cat > "${WORKER_PATH}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

mcp_call() {
  "$CUBICLE_BIN" mcp-call --url "$CUBICLE_MCP_URL" --tool "$1" --payload "$2"
}

mcp_call claim_task "{\"task_id\":\"$CUBICLE_TASK_ID\",\"agent\":\"fixture-qwen\"}"
mcp_call heartbeat "{\"task_id\":\"$CUBICLE_TASK_ID\",\"summary\":\"working\"}"

if [[ "$CUBICLE_TASK_TITLE" == *"success"* ]]; then
  printf 'done\n' > done.txt
  python3 "@PROJECT_DIR@/todo.py" add "Verify a smoke run creates this file"
  mcp_call complete_task "{\"task_id\":\"$CUBICLE_TASK_ID\",\"summary\":\"finished\",\"files_changed\":[\"done.txt\"],\"test_results\":[\"fixture\"]}"
else
  mcp_call block_task "{\"task_id\":\"$CUBICLE_TASK_ID\",\"reason\":\"needs input\"}"
fi
EOF
chmod +x "${WORKER_PATH}"
write_backend_config "${WORKER_PATH}"
python3 <<PY
from pathlib import Path
import json
path = Path("${WORKER_PATH}")
text = path.read_text()
text = text.replace("@PROJECT_DIR@", json.dumps("${PROJECT_DIR}")[1:-1])
path.write_text(text)
PY

log "creating tasks"
SUCCESS_ID="$(run_cubicleq tasks add --title "success task" --description "should finish and validate" --priority high --validate "test -f done.txt" | tr -d '\n')"
BLOCKED_ID="$(run_cubicleq tasks add --title "blocked task" --description "should block with an explicit reason" --priority medium | tr -d '\n')"
TODO_MANAGER_ID="$(run_cubicleq tasks add --title "todo manager cleanup" --description "add a utility entry" --priority low | tr -d '\n')"
run_cubicleq tasks add --title "CLI text editor spec" --description "outline CLI requirements" --priority low --validate "python3 todo-cli/todo.py" >/dev/null
run_cubicleq tasks add --title "update README" --description "document the smoke helper" --priority low >/dev/null

log "running cubicleq scheduler"
RUN_OUTPUT="$(run_cubicleq run)"
printf '%s\n' "${RUN_OUTPUT}"
MCP_URL="$(extract_mcp_url "${RUN_OUTPUT}")" || fail "expected MCP URL in run output"

REVIEW_LIST="$(run_cubicleq review list)"
BLOCKERS="$(run_cubicleq blockers list)"
STATUS="$(run_cubicleq status)"

require_contains "${REVIEW_LIST}" "${SUCCESS_ID}" "review list"
require_contains "${BLOCKERS}" "${BLOCKED_ID}" "blockers list"
require_contains "${BLOCKERS}" "needs input" "blockers list"
require_contains "${STATUS}" "cubicleq review accept ${SUCCESS_ID}" "status output"
require_contains "${STATUS}" "cubicleq logs ${BLOCKED_ID}" "status output"

ROOT_QWEN_AFTER="$(cat "${SCRATCH_REPO}/.qwen/settings.json")"
[[ "${ROOT_QWEN_AFTER}" == "${ROOT_QWEN_SETTINGS}" ]] || fail "expected root .qwen/settings.json to remain unchanged"
BLOCKED_WORKTREE_SETTINGS="${SCRATCH_REPO}/worktrees/${BLOCKED_ID}/.qwen/settings.json"
[[ -f "${BLOCKED_WORKTREE_SETTINGS}" ]] || fail "expected blocked task worktree qwen settings"
BLOCKED_WORKTREE_RAW="$(cat "${BLOCKED_WORKTREE_SETTINGS}")"
require_contains "${BLOCKED_WORKTREE_RAW}" "${MCP_URL}" "blocked worktree qwen settings"
require_contains "${BLOCKED_WORKTREE_RAW}" '"cubicleq"' "blocked worktree qwen settings"
if [[ "${ROOT_QWEN_AFTER}" == *"${MCP_URL}"* ]]; then
  fail "did not expect runtime MCP URL in root .qwen/settings.json"
fi

log "review/blocker verification passed"
printf '%s\n' "${STATUS}"

TODO_JSON="${PROJECT_DIR}/todo.json"
[[ -f "${TODO_JSON}" ]] || fail "expected todo manager data file ${TODO_JSON}"
TODO_DATA="$(cat "${TODO_JSON}")"
require_contains "${TODO_DATA}" "Verify a smoke run creates this file" "todo manager artifact"

if [[ ${RUN_ORCHESTRATOR} -eq 1 ]]; then
  ORCHESTRATOR_PATH="${SCRATCH_REPO}/fixture-orchestrator-qwen.sh"
  cat > "${ORCHESTRATOR_PATH}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

task_id=$("$CUBICLE_BIN" review list | awk 'NR==1 {print $1}')
if [[ -z "${task_id:-}" ]]; then
  printf '{"role":"orchestrator","status":"no_action","actions":[],"current_blockers":"None","notes":"no review task found"}\n'
  exit 0
fi
printf '{"role":"orchestrator","status":"complete","actions":[{"type":"review_accept","task_id":"%s"}],"current_blockers":"None","notes":"fixture accept"}\n' "$task_id"
EOF
  chmod +x "${ORCHESTRATOR_PATH}"
  write_backend_config "${ORCHESTRATOR_PATH}"
  write_policy

  log "running orchestrator fixture"
  run_cubicleq orchestrate

  FINAL_STATUS="$(run_cubicleq status)"
  require_contains "${FINAL_STATUS}" "${SUCCESS_ID}" "final status output"
  require_contains "${FINAL_STATUS}" "done" "final status output"
  [[ -f "${SCRATCH_REPO}/done.txt" ]] || fail "expected merged done.txt in base repo"

  log "orchestrator verification passed"
  printf '%s\n' "${FINAL_STATUS}"
fi

printf '\nsmoke e2e passed\n'
if [[ ${SCRIPT_CREATED_REPO} -eq 1 && ${KEEP_REPO} -eq 0 ]]; then
  printf 'temporary repo will be removed on exit; rerun with --keep to preserve it\n'
else
  printf 'repo will be kept on exit\n'
fi
