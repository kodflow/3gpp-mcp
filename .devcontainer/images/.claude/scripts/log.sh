#!/bin/bash
# ============================================================================
# log.sh - Context persistence for Claude Code recovery
# Hook: PreToolUse + PostToolUse (all tools)
# Exit 0 = success (always, fail-open - never block Claude)
#
# Purpose: Log all tool actions by branch for context recovery after crash.
# Output: /workspace/.claude/logs/<branch>/session.jsonl + checkpoint.json
#
# Features:
#   - flock for concurrency (parallel hooks safe)
#   - Payload sanitization (truncation + redaction)
#   - Atomic checkpoint (tmp + mv)
#   - Branch-scoped logs
#   - Fail-open (errors don't block Claude)
#
# Data Model (JSONL per entry):
#   - timestamp, session_id, tool_use_id, hook_event_name
#   - branch, commit, tool_name
#   - tool_input (sanitized), tool_response (sanitized)
# ============================================================================

set -uo pipefail

# === Configuration ===
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
LOGS_BASE="$PROJECT_DIR/.claude/logs"
MAX_COMMAND_LEN=500
MAX_OUTPUT_LEN=2000
MAX_STDERR_LEN=1000

# === Helper: Truncate string ===
truncate_str() {
    local str="$1"
    local max="${2:-500}"
    if [[ ${#str} -gt $max ]]; then
        printf '%s...(truncated)' "${str:0:$max}"
    else
        printf '%s' "$str"
    fi
}

# === Helper: Redact secrets ===
redact_secrets() {
    local str="$1"
    printf '%s' "$str" | sed -E \
        -e 's/(token|apikey|api_key|password|secret|authorization)[[:space:]]*[:=][[:space:]]*[^[:space:]"'\'']+/<redacted>/gi' \
        -e 's/(ghp_|gho_|github_pat_|sk-|AKIA)[A-Za-z0-9_-]+/<redacted>/g' \
        -e 's/Bearer [A-Za-z0-9._-]+/Bearer <redacted>/g'
}

# === Read hook input (JSON from stdin) ===
# Temporarily disable pipefail to handle empty stdin gracefully
set +o pipefail
INPUT="$(cat 2>/dev/null)"
set -o pipefail
INPUT="${INPUT:-{}}"

# Fail gracefully if no input or empty
if [[ -z "$INPUT" ]] || [[ "$INPUT" == "{}" ]]; then
    exit 0
fi

# === Ensure jq exists ===
if ! command -v jq &>/dev/null; then
    exit 0
fi

# === Extract fields from hook JSON ===
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
TOOL_NAME=$(printf '%s' "$INPUT" | jq -r '.tool_name // "unknown"' 2>/dev/null || echo "unknown")
TOOL_USE_ID=$(printf '%s' "$INPUT" | jq -r '.tool_use_id // ""' 2>/dev/null || echo "")
HOOK_EVENT=$(printf '%s' "$INPUT" | jq -r '.hook_event_name // "unknown"' 2>/dev/null || echo "unknown")
PERMISSION_MODE=$(printf '%s' "$INPUT" | jq -r '.permission_mode // ""' 2>/dev/null || echo "")
CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // ""' 2>/dev/null || echo "")
TRANSCRIPT_PATH=$(printf '%s' "$INPUT" | jq -r '.transcript_path // ""' 2>/dev/null || echo "")

# === Get current branch (cached to avoid subprocess overhead) ===
# Use cached values from session-init.sh if available, fallback to git
if [[ -n "${CLAUDE_GIT_BRANCH:-}" ]]; then
    BRANCH="$CLAUDE_GIT_BRANCH"
else
    BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
fi
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')
if [[ -n "${CLAUDE_GIT_COMMIT:-}" ]]; then
    COMMIT_SHA="$CLAUDE_GIT_COMMIT"
else
    COMMIT_SHA=$(git -C "$PROJECT_DIR" rev-parse --short HEAD 2>/dev/null || echo "")
fi

# === Setup log directory ===
LOG_DIR="$LOGS_BASE/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || exit 0

SESSION_LOG="$LOG_DIR/session.jsonl"
CHECKPOINT="$LOG_DIR/checkpoint.json"
LOCKFILE="$LOG_DIR/.lock"

# === Timestamp ===
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# === Build safe tool_input ===
build_safe_input() {
    local raw_input
    raw_input=$(printf '%s' "$INPUT" | jq -c '.tool_input // {}' 2>/dev/null || echo '{}')

    case "$TOOL_NAME" in
        Write|Edit)
            # Only keep file_path and content lengths (never content itself)
            local file_path content_len old_len new_len
            file_path=$(printf '%s' "$raw_input" | jq -r '.file_path // ""' 2>/dev/null || echo "")
            content_len=$(printf '%s' "$raw_input" | jq -r '.content // "" | length' 2>/dev/null || echo "0")
            old_len=$(printf '%s' "$raw_input" | jq -r '.old_string // "" | length' 2>/dev/null || echo "0")
            new_len=$(printf '%s' "$raw_input" | jq -r '.new_string // "" | length' 2>/dev/null || echo "0")

            jq -n -c \
                --arg fp "$file_path" \
                --argjson cl "$content_len" \
                --argjson ol "$old_len" \
                --argjson nl "$new_len" \
                '{file_path: $fp, content_len: $cl, old_string_len: $ol, new_string_len: $nl}'
            ;;
        Bash)
            # Truncate and redact command
            local cmd desc timeout bg
            cmd=$(printf '%s' "$raw_input" | jq -r '.command // ""' 2>/dev/null || echo "")
            cmd=$(redact_secrets "$(truncate_str "$cmd" "$MAX_COMMAND_LEN")")
            desc=$(printf '%s' "$raw_input" | jq -r '.description // ""' 2>/dev/null || echo "")
            desc=$(truncate_str "$desc" 200)
            timeout=$(printf '%s' "$raw_input" | jq -r '.timeout // null' 2>/dev/null || echo "null")
            bg=$(printf '%s' "$raw_input" | jq -r '.run_in_background // false' 2>/dev/null || echo "false")

            jq -n -c \
                --arg c "$cmd" \
                --arg d "$desc" \
                --argjson t "$timeout" \
                --argjson bg "$bg" \
                '{command: $c, description: $d, timeout: $t, run_in_background: $bg}'
            ;;
        Read)
            # Keep file_path only
            local file_path
            file_path=$(printf '%s' "$raw_input" | jq -r '.file_path // ""' 2>/dev/null || echo "")
            jq -n -c --arg fp "$file_path" '{file_path: $fp}'
            ;;
        Glob|Grep)
            # Keep pattern and path
            local pattern path
            pattern=$(printf '%s' "$raw_input" | jq -r '.pattern // ""' 2>/dev/null || echo "")
            path=$(printf '%s' "$raw_input" | jq -r '.path // ""' 2>/dev/null || echo "")
            jq -n -c --arg p "$pattern" --arg d "$path" '{pattern: $p, path: $d}'
            ;;
        Task)
            # Keep description and subagent_type
            local desc stype
            desc=$(printf '%s' "$raw_input" | jq -r '.description // ""' 2>/dev/null || echo "")
            stype=$(printf '%s' "$raw_input" | jq -r '.subagent_type // ""' 2>/dev/null || echo "")
            jq -n -c --arg d "$desc" --arg s "$stype" '{description: $d, subagent_type: $s}'
            ;;
        *)
            # For other tools, keep minimal info (skip large fields)
            printf '%s' "$raw_input" | jq -c 'del(.content, .new_string, .old_string, .prompt) | to_entries | map(select(.value | type != "string" or length < 200)) | from_entries' 2>/dev/null || echo '{}'
            ;;
    esac
}

# === Build safe tool_response ===
build_safe_response() {
    local raw_response
    raw_response=$(printf '%s' "$INPUT" | jq -c '.tool_response // {}' 2>/dev/null || echo '{}')

    # Check if Bash response (has return_code, output, stderr)
    local has_rc
    has_rc=$(printf '%s' "$raw_response" | jq -r 'has("return_code") or has("output") or has("stderr")' 2>/dev/null || echo "false")

    if [[ "$has_rc" == "true" ]]; then
        local rc output stderr
        rc=$(printf '%s' "$raw_response" | jq -r '.return_code // null' 2>/dev/null || echo "null")
        output=$(printf '%s' "$raw_response" | jq -r '.output // ""' 2>/dev/null || echo "")
        output=$(redact_secrets "$(truncate_str "$output" "$MAX_OUTPUT_LEN")")
        stderr=$(printf '%s' "$raw_response" | jq -r '.stderr // ""' 2>/dev/null || echo "")
        stderr=$(redact_secrets "$(truncate_str "$stderr" "$MAX_STDERR_LEN")")

        jq -n -c \
            --argjson rc "$rc" \
            --arg out "$output" \
            --arg err "$stderr" \
            '{return_code: $rc, output: $out, stderr: $err}'
    else
        # For other tools (Write, Edit, Read), keep as-is (usually small)
        printf '%s' "$raw_response"
    fi
}

# === Build event JSON ===
SAFE_INPUT=$(build_safe_input)
SAFE_RESPONSE=$(build_safe_response)

EVENT=$(jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg tuid "$TOOL_USE_ID" \
    --arg hook "$HOOK_EVENT" \
    --arg pm "$PERMISSION_MODE" \
    --arg cwd "$CWD" \
    --arg branch "$BRANCH" \
    --arg branch_safe "$BRANCH_SAFE" \
    --arg commit "$COMMIT_SHA" \
    --arg tool "$TOOL_NAME" \
    --argjson input "$SAFE_INPUT" \
    --argjson response "$SAFE_RESPONSE" \
    --arg transcript "$TRANSCRIPT_PATH" \
    '{
        timestamp: $ts,
        session_id: $sid,
        tool_use_id: $tuid,
        hook_event_name: $hook,
        permission_mode: $pm,
        cwd: $cwd,
        branch: $branch,
        branch_safe: $branch_safe,
        commit: $commit,
        tool_name: $tool,
        tool_input: $input,
        tool_response: $response,
        transcript_path: $transcript
    }' 2>/dev/null) || exit 0

# === Write with flock (concurrent hooks safe) ===
(
    # Try to acquire lock with timeout (don't block forever)
    flock -x -w 3 9 || exit 0

    # Append to JSONL
    printf '%s\n' "$EVENT" >> "$SESSION_LOG"

    # Atomic checkpoint update
    TMP_CHECKPOINT="$CHECKPOINT.tmp.$$"
    jq -n \
        --arg ts "$TIMESTAMP" \
        --arg sid "$SESSION_ID" \
        --arg branch "$BRANCH" \
        --arg branch_safe "$BRANCH_SAFE" \
        --arg commit "$COMMIT_SHA" \
        --arg log_file "$SESSION_LOG" \
        --argjson last_event "$EVENT" \
        '{
            last_update: $ts,
            session_id: $sid,
            branch: $branch,
            branch_safe: $branch_safe,
            commit: $commit,
            log_file: $log_file,
            last_event: $last_event,
            recovery_hint: "Read checkpoint.json for last action, tail session.jsonl for full context."
        }' > "$TMP_CHECKPOINT" 2>/dev/null

    # Atomic move
    mv "$TMP_CHECKPOINT" "$CHECKPOINT" 2>/dev/null || rm -f "$TMP_CHECKPOINT"

) 9>"$LOCKFILE" 2>/dev/null

exit 0
