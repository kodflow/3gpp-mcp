#!/bin/bash
# rtk-hook-claude.sh — fail-open wrapper around `rtk hook claude`.
#
# Why: a non-zero exit from this PreToolUse hook blocks every Bash call in
# Claude Code. rtk is best-effort token compression — never worth blocking
# the agent on. CLAUDE.md §2.0 explicitly promises "runtime: never blocking";
# this wrapper is that contract's implementation.
#
# stdin → JSON payload from Claude Code; captured once, re-piped to rtk.
# stdout → JSON payload Claude Code consumes; rtk's response on success,
#          empty `{}` on any failure (no rewrite, allow original command).
# stderr → diagnostic log written to ~/.claude/logs/<branch>/rtk-hook.log
#          for /audit visibility — NEVER surfaced to Claude (would block).
#
# See issue #348 for the regression that motivated this layer.

set +e

_resolve_branch() {
    git symbolic-ref --short HEAD 2>/dev/null || printf 'default'
}

LOG_DIR="${HOME}/.claude/logs/$(_resolve_branch)"
mkdir -p "$LOG_DIR" 2>/dev/null
LOG_FILE="${LOG_DIR}/rtk-hook.log"

# Capture stdin ONCE — `rtk hook claude` reads JSON from stdin per its --help:
# "Process Claude Code PreToolUse hook (reads JSON from stdin)". Without this
# capture the wrapper would consume the payload itself and rtk would always
# degrade, silently disabling token rewrites even on a healthy binary.
PAYLOAD="$(cat)"

if ! command -v rtk >/dev/null 2>&1; then
    printf '[rtk-hook] rtk binary not found; fail-open\n' >>"$LOG_FILE" 2>/dev/null
    printf '{}\n'
    exit 0
fi

# Separate stdout from stderr — merging would corrupt JSON if rtk ever emits
# a warning/deprecation notice alongside a valid hook response. stderr always
# flows to the log file (success OR failure); stdout is what we validate.
ERR_FILE=$(mktemp 2>/dev/null) || ERR_FILE="${LOG_DIR}/.rtk-hook-stderr.$$"
OUT="$(printf '%s' "$PAYLOAD" | rtk hook claude 2>"$ERR_FILE")"
RC=$?

if [ -s "$ERR_FILE" ]; then
    cat "$ERR_FILE" >>"$LOG_FILE" 2>/dev/null
fi
rm -f "$ERR_FILE"

if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | jq -e . >/dev/null 2>&1; then
    printf '%s\n' "$OUT"
    exit 0
fi

{
    printf '[rtk-hook] rtk hook failed rc=%s; fail-open\n' "$RC"
    printf '%s\n' "$OUT"
} >>"$LOG_FILE" 2>/dev/null

printf '{}\n'
exit 0
