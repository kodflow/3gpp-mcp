#!/bin/bash
# pre-validate.sh - Validate modifications to protected files
# Usage: pre-validate.sh <file_path>
# Exit 0 = allow, Exit 2 = block

set -uo pipefail
# Note: Removed -e (errexit) to fail-open on unexpected errors

# Read file_path from stdin JSON (preferred) or fallback to argument
INPUT="$(cat 2>/dev/null || true)"
FILE=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    FILE=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // ""' 2>/dev/null || true)
fi
FILE="${FILE:-${1:-}}"

if [ -z "$FILE" ]; then
    exit 0
fi

# Configuration file paths
PROTECTED_PATHS_FILE="/workspace/.claude/protected-paths.yml"
PROTECTED_PATHS_DEFAULT="$HOME/.claude/protected-paths.yml"

# Use yq if available, otherwise fall back to hardcoded patterns
USE_YQ=false
if command -v yq &>/dev/null; then
    if [[ -f "$PROTECTED_PATHS_FILE" ]]; then
        USE_YQ=true
        CONFIG_FILE="$PROTECTED_PATHS_FILE"
    elif [[ -f "$PROTECTED_PATHS_DEFAULT" ]]; then
        USE_YQ=true
        CONFIG_FILE="$PROTECTED_PATHS_DEFAULT"
    fi
fi

# Default protected patterns (fallback) - only truly dangerous paths
PROTECTED_PATTERNS=(
    "node_modules/"
    ".git/"
    "vendor/"
    "dist/"
    "build/"
    ".env"
    "*.lock"
    "package-lock.json"
    "yarn.lock"
    "pnpm-lock.yaml"
    "Cargo.lock"
    "poetry.lock"
    "go.sum"
)

# Exceptions (always allowed)
EXCEPTIONS=(
    "*.md"
    "README*"
    "CHANGELOG*"
    ".claude/contexts/"
    ".claude/plans/"
    ".claude/sessions/"
)

# Function to check if the file matches an exception
is_exception() {
    local file="$1"
    for pattern in "${EXCEPTIONS[@]}"; do
        # Use bash pattern matching
        if [[ "$file" == *"$pattern"* ]] || [[ "$file" == "$pattern" ]]; then
            return 0
        fi
    done
    return 1
}

# Check exceptions first
if is_exception "$FILE"; then
    exit 0
fi

# === Verification with yq if available ===
if [[ "$USE_YQ" == "true" ]]; then
    # Read protected patterns from the YAML file
    # mikefarah/yq syntax (no -r flag needed, raw output is default)
    YAML_PATTERNS=$(yq '.protected[]' "$CONFIG_FILE" 2>/dev/null || echo "")

    for pattern in $YAML_PATTERNS; do
        [[ -z "$pattern" ]] && continue

        # Check if the file matches the pattern
        if [[ "$FILE" == *"$pattern"* ]] || [[ "$FILE" == "$pattern" ]]; then
            REASON="Protected file: $FILE (pattern: $pattern)"
            echo "🚫 $REASON" >&2
            if command -v jq &>/dev/null; then
                jq -n --arg reason "$REASON" \
                    '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":$reason}}'
                exit 0
            fi
            exit 2
        fi
    done
else
    # Fallback: use hardcoded patterns
    for pattern in "${PROTECTED_PATTERNS[@]}"; do
        if [[ "$FILE" == *"$pattern"* ]]; then
            REASON="Protected file: $FILE (pattern: $pattern)"
            echo "🚫 $REASON" >&2
            if command -v jq &>/dev/null; then
                jq -n --arg reason "$REASON" \
                    '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":$reason}}'
                exit 0
            fi
            exit 2
        fi
    done
fi

# === ktn-linter: package context before edit ===
# Calls ktn-linter HTTP endpoint to surface existing issues in the package
# being modified. Graceful degradation if ktn-linter is not running.
# Pre-edit fast check: only structural/signature breaks block before the edit;
# logic/perf/style/comment categories are deferred to post-edit.sh.
KTN_PORT="${KTN_LINTER_PORT:-7717}"
if command -v curl &>/dev/null && [[ "$FILE" != *.md ]] && [[ "$FILE" != *.json ]] && \
   [[ "$FILE" != *.yaml ]] && [[ "$FILE" != *.yml ]] && [[ "$FILE" != *.toml ]] && \
   [[ "$FILE" != /tmp/* ]] && [[ "$FILE" != *".claude/"* ]]; then
    # Per-hook phase scope (override via KTN_PRE_PHASES env var, comma-separated).
    # Servers pre-#190 ignore the unknown field and fall back to YAML config.
    KTN_PHASES_CSV="${KTN_PRE_PHASES:-structural,signatures}"
    KTN_PHASES_CSV="${KTN_PHASES_CSV// /}"
    KTN_BODY="${INPUT:-}"
    [ -z "$KTN_BODY" ] && KTN_BODY="{}"
    if command -v jq &>/dev/null; then
        KTN_TRY=$(printf '%s' "$KTN_BODY" | jq -c --arg p "$KTN_PHASES_CSV" \
            '. + {phases: ($p | split(","))}' 2>/dev/null) \
            && KTN_BODY="$KTN_TRY"
    fi
    KTN_RESP=$(curl -sf --max-time 4 \
        -H "Content-Type: application/json" \
        -d "$KTN_BODY" \
        "http://localhost:${KTN_PORT}/hooks/pre-tool-use" 2>/dev/null) || true
    if [ -n "$KTN_RESP" ] && [ "$KTN_RESP" != "{}" ] && [ "$KTN_RESP" != "null" ]; then
        if printf '%s' "$KTN_RESP" | jq -e '.hookSpecificOutput' &>/dev/null; then
            printf '%s' "$KTN_RESP"
            exit 0
        fi
        jq -n -c --arg ctx "$(printf '%s' "$KTN_RESP" | head -c 500)" \
            '{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":$ctx}}' \
            2>/dev/null || true
    fi
fi

# === Security Pattern Warnings (allow but warn, once per session) ===
# Inspired by anthropics/claude-plugins-official/security-guidance

# Sanitize session ID to prevent path traversal
SESSION_ID="${CLAUDE_SESSION_ID:-unknown}"
SESSION_ID=$(echo "$SESSION_ID" | tr -cd 'A-Za-z0-9._-')
SESSION_ID="${SESSION_ID:-unknown}"
STATE_FILE="$HOME/.claude/.security_warnings_${SESSION_ID}"

CONTENT=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    CONTENT=$(printf '%s' "$INPUT" | jq -r '.tool_input.content // .tool_input.new_string // ""' 2>/dev/null || true)
fi

if [ -n "$CONTENT" ]; then
    # pattern|warning pairs
    SEC_CHECKS=(
        'eval(|Code injection: eval() executes arbitrary code. Use JSON.parse() or safer alternatives.'
        'new Function|Code injection: new Function() creates code from strings. Consider alternatives.'
        'child_process.exec|Command injection: exec() passes to shell. Use execFile() with argument arrays.'
        'dangerouslySetInnerHTML|XSS: renders raw HTML. Sanitize with DOMPurify or use safe alternatives.'
        'document.write|XSS: can inject content. Use DOM methods (createElement, appendChild).'
        '.innerHTML =|XSS: innerHTML can execute scripts. Use textContent or DOMPurify.'
        'pickle|Deserialization: pickle can execute arbitrary code. Use JSON or safe formats.'
        'subprocess.call|Command injection: subprocess with shell=True is dangerous. Use list args.'
    )

    for entry in "${SEC_CHECKS[@]}"; do
        pattern="${entry%%|*}"
        warning="${entry#*|}"

        if [[ "$CONTENT" == *"$pattern"* ]]; then
            # Skip if already warned in this session
            if [ -f "$STATE_FILE" ] && grep -qF "$pattern" "$STATE_FILE" 2>/dev/null; then
                continue
            fi
            echo "$pattern" >> "$STATE_FILE" 2>/dev/null || true
            echo "SECURITY: $warning" >&2
            echo "  Pattern '$pattern' in $FILE (warning shown once per session)" >&2
        fi
    done

    # Clean up old state files (>7 days)
    find "$HOME/.claude/" -name ".security_warnings_*" -mtime +7 -delete 2>/dev/null || true
fi

exit 0
