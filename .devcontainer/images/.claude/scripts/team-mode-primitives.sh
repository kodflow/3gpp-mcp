#!/bin/bash
# =============================================================================
# team-mode-primitives.sh
# =============================================================================
# Single source of truth for Agent Teams runtime primitives.
# Sourced by:
#   - Hooks: task-created.sh, task-completed.sh, teammate-idle.sh
#   - Unit tests: .devcontainer/tests/unit/test-*.sh
#
# Provides:
#   team_mode_debug_log <msg>            — stderr log when TEAM_MODE_DEBUG=1
#   classify_terminal                    — known-compatible | known-incompatible | unknown
#   detect_runtime_mode                  — TEAMS_TMUX | TEAMS_INPROCESS | SUBAGENTS
#   extract_task_contract <description>  — JSON or empty
#   validate_contract_version <json>     — exit 0 if version == 1
#   epoch_now                            — seconds since epoch (portable)
#   epoch_from_iso <iso8601>             — seconds since epoch (GNU/BSD portable)
#   epoch_24h_ago                        — now - 86400
#
# All functions are pure where possible; side effects are explicit.
# =============================================================================

# -----------------------------------------------------------------------------
# Debug log (off by default, zero cost when off)
# -----------------------------------------------------------------------------
team_mode_debug_log() {
    [ "${TEAM_MODE_DEBUG:-0}" = "1" ] && printf '[team-mode] %s\n' "$*" >&2
    return 0
}

# -----------------------------------------------------------------------------
# Terminal classification (heuristic, maintained by regression tests)
# -----------------------------------------------------------------------------
# Priority: known-incompatible > known-compatible > unknown
# Output: one of known-compatible | known-incompatible | unknown
# -----------------------------------------------------------------------------
classify_terminal() {
    # Incompatible first
    if [ -n "${VSCODE_PID:-}" ]; then
        team_mode_debug_log "terminal: known-incompatible (VSCODE_PID set)"
        echo "known-incompatible"
        return
    fi
    if [ "${TERM_PROGRAM:-}" = "vscode" ]; then
        team_mode_debug_log "terminal: known-incompatible (TERM_PROGRAM=vscode)"
        echo "known-incompatible"
        return
    fi
    if [ -n "${WT_SESSION:-}" ]; then
        team_mode_debug_log "terminal: known-incompatible (WT_SESSION set)"
        echo "known-incompatible"
        return
    fi

    # Inside tmux is always compatible
    if [ -n "${TMUX:-}" ]; then
        team_mode_debug_log "terminal: known-compatible (TMUX active)"
        echo "known-compatible"
        return
    fi

    # Known-compatible terminals
    case "${TERM_PROGRAM:-}" in
        iTerm.app|WezTerm|ghostty|kitty)
            team_mode_debug_log "terminal: known-compatible ($TERM_PROGRAM)"
            echo "known-compatible"
            return
            ;;
    esac

    # Everything else: conservative
    team_mode_debug_log "terminal: unknown (TERM_PROGRAM=${TERM_PROGRAM:-unset})"
    echo "unknown"
}

# -----------------------------------------------------------------------------
# Runtime mode detection
# -----------------------------------------------------------------------------
# Reads capability file (cache) + live probe. Live probe wins.
# Output: TEAMS_TMUX | TEAMS_INPROCESS | SUBAGENTS
# -----------------------------------------------------------------------------
detect_runtime_mode() {
    local cap
    cap=$(cat "${HOME}/.claude/.team-capability" 2>/dev/null || echo NONE)
    team_mode_debug_log "capability-read: $cap"

    # Kill switch: NONE means hard-disable, always SUBAGENTS
    if [ "$cap" = "NONE" ]; then
        team_mode_debug_log "runtime-mode: SUBAGENTS (capability=NONE, kill switch)"
        echo "SUBAGENTS"
        return
    fi

    # Env flag required for any TEAMS mode
    if [ "${CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS:-0}" != "1" ]; then
        team_mode_debug_log "runtime-mode: SUBAGENTS (env flag off)"
        echo "SUBAGENTS"
        return
    fi

    # Version probe (best-effort; if claude CLI absent, fall back to capability)
    if command -v claude >/dev/null 2>&1; then
        local current min="2.1.32"
        current=$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
        if [ -z "$current" ] || ! version_at_least "$min" "$current"; then
            team_mode_debug_log "runtime-mode: SUBAGENTS (claude=${current:-none} < $min)"
            echo "SUBAGENTS"
            return
        fi
    fi

    # Terminal + tmux check
    local term_class
    term_class=$(classify_terminal)
    if command -v tmux >/dev/null 2>&1 && [ "$term_class" = "known-compatible" ]; then
        team_mode_debug_log "runtime-mode: TEAMS_TMUX"
        echo "TEAMS_TMUX"
        return
    fi

    team_mode_debug_log "runtime-mode: TEAMS_INPROCESS (tmux=$(command -v tmux >/dev/null && echo yes || echo no) term=$term_class)"
    echo "TEAMS_INPROCESS"
}

# -----------------------------------------------------------------------------
# Task contract extraction (delimiter-tolerant, version-aware)
# -----------------------------------------------------------------------------
# Input: task_description string (via $1)
# Output: JSON string on stdout (single line, compact) or empty on failure
# -----------------------------------------------------------------------------
extract_task_contract() {
    local description="$1"
    [ -z "$description" ] && return 0

    # Tolerant opening marker: flexible whitespace, any version number
    # Tolerant closing marker: any line containing -->
    # jq -c validates AND compacts to one line
    printf '%s' "$description" | awk '
        /<!--[[:space:]]*task-contract[[:space:]]+v[0-9]+/ { p=1; next }
        p && /-->/                                        { p=0; exit }
        p                                                 { print }
    ' | jq -c . 2>/dev/null
}

# -----------------------------------------------------------------------------
# Validate contract version
# -----------------------------------------------------------------------------
# Input: contract JSON string (via $1)
# Returns: 0 if contract_version == 1, 1 otherwise
# -----------------------------------------------------------------------------
validate_contract_version() {
    local contract_json="$1"
    [ -z "$contract_json" ] && return 1
    local version
    version=$(printf '%s' "$contract_json" | jq -r '.contract_version // 0' 2>/dev/null)
    [ "$version" = "1" ]
}

# -----------------------------------------------------------------------------
# Portable epoch helpers (GNU/BSD/busybox)
# -----------------------------------------------------------------------------
epoch_now() {
    date -u +%s
}

epoch_24h_ago() {
    local now
    now=$(epoch_now)
    echo $((now - 86400))
}

# Input: ISO-8601 timestamp string (via $1), e.g. 2026-04-09T14:30:00Z
# Output: epoch seconds, or 0 on failure
epoch_from_iso() {
    local iso="$1"
    [ -z "$iso" ] && { echo 0; return; }

    # Try GNU date first
    local result
    result=$(date -u -d "$iso" +%s 2>/dev/null) && { echo "$result"; return; }

    # Try BSD date (-j = no set, -f = parse format)
    result=$(date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$iso" +%s 2>/dev/null) && { echo "$result"; return; }

    # Busybox: try without -u (busybox date is limited)
    result=$(date -d "$iso" +%s 2>/dev/null) && { echo "$result"; return; }

    # Give up
    echo 0
}

# -----------------------------------------------------------------------------
# Version comparison
# -----------------------------------------------------------------------------
# Input: $1 = required, $2 = current
# Returns: 0 if current >= required, 1 otherwise
# -----------------------------------------------------------------------------
version_at_least() {
    local required="$1"
    local current="$2"
    [ -z "$current" ] && return 1
    # Strip any pre-release suffix (e.g. 2.1.32-beta → 2.1.32)
    current=$(printf '%s' "$current" | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    [ -z "$current" ] && return 1
    # Portable version comparison (works on GNU, BSD, busybox, Alpine)
    local IFS=.
    set -- $required
    local r1="${1:-0}" r2="${2:-0}" r3="${3:-0}"
    set -- $current
    local c1="${1:-0}" c2="${2:-0}" c3="${3:-0}"
    [ "$c1" -gt "$r1" ] && return 0
    [ "$c1" -lt "$r1" ] && return 1
    [ "$c2" -gt "$r2" ] && return 0
    [ "$c2" -lt "$r2" ] && return 1
    [ "$c3" -ge "$r3" ] && return 0
    return 1
}
