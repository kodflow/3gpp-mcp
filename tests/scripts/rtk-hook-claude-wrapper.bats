#!/usr/bin/env bats
# Tests for rtk-hook-claude.sh — fail-open contract.
# Layer A of plan tidy-skipping-hollerith (issue #348).
#
# CONTRACT — for every input class, the wrapper MUST:
#   1) exit 0 (any non-zero blocks every Bash call in Claude Code)
#   2) emit valid JSON on stdout (Claude Code parses it)
#   3) NEVER surface stderr to Claude (writes to log file instead)
#   4) forward stdin to rtk on the happy path (regression guard for the
#      stdin-forwarding bug that motivated this PR)

setup() {
    load '../helpers/setup'
    common_setup

    SCRIPT="${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/scripts/rtk-hook-claude.sh"

    # Sandbox HOME so the wrapper's log writes go into TEST_TMPDIR, not the
    # developer's real ~/.claude/logs.
    export TEST_HOME="$TEST_TMPDIR/home"
    mkdir -p "$TEST_HOME"

    # Sandbox a fake git repo so _resolve_branch returns a deterministic name.
    git -C "$TEST_HOME" init -q -b test-branch 2>/dev/null
}

teardown() {
    common_teardown
}

# Run the wrapper with PATH manipulated so a stub rtk (or no rtk) is seen.
# The stub directory is prepended to PATH; pass "" as $1 to disable rtk.
run_wrapper() {
    local stub_dir="$1"
    shift
    local payload="${1:-{\"session_id\":\"t\",\"tool_input\":{\"command\":\"git status\"}}}"

    local path_prefix=""
    [ -n "$stub_dir" ] && path_prefix="$stub_dir:"

    cd "$TEST_HOME" 2>/dev/null

    HOME="$TEST_HOME" \
    PATH="${path_prefix}/usr/bin:/bin" \
        bash "$SCRIPT" <<<"$payload"
}

# Build a stub rtk binary that echoes a fixed payload + chosen exit code.
# Stub also captures stdin to a sentinel file (so tests can assert forwarding)
# and optionally emits a fixed payload on stderr (so tests can verify the
# wrapper does not corrupt stdout JSON when rtk emits a warning).
make_stub_rtk() {
    local stub_dir="$1"
    local stdout_payload="$2"
    local exit_code="${3:-0}"
    local stdin_capture="${4:-}"
    local stderr_payload="${5:-}"

    mkdir -p "$stub_dir"
    cat > "$stub_dir/rtk" <<EOF
#!/bin/bash
# Stub rtk — captures stdin (if asked), emits fixed stdout/stderr, exits $exit_code.
$([ -n "$stdin_capture" ] && echo "cat > '$stdin_capture'")
$([ -n "$stderr_payload" ] && echo "printf '%s' '$stderr_payload' >&2")
printf '%s' '$stdout_payload'
exit $exit_code
EOF
    chmod +x "$stub_dir/rtk"
}

# === Test 1: rtk absent on PATH → exit 0 + empty {} + log warning ===

@test "rtk-hook-wrapper: rtk absent → exit 0, emits {}, logs warning" {
    run run_wrapper ""  # no stub_dir → real PATH minus /usr/local/bin
    [ "$status" -eq 0 ]
    [[ "$output" == "{}" ]]
    # Log file should exist and contain the warning.
    local log="$TEST_HOME/.claude/logs/test-branch/rtk-hook.log"
    [ -f "$log" ]
    grep -q "rtk binary not found" "$log"
}

# === Test 2: rtk present + valid JSON → wrapper passes through ===

@test "rtk-hook-wrapper: happy path passes through rtk's JSON unchanged" {
    local stub="$TEST_TMPDIR/stub-bin"
    make_stub_rtk "$stub" '{"hookSpecificOutput":{"permissionDecision":"allow"}}' 0
    run run_wrapper "$stub"
    [ "$status" -eq 0 ]
    [[ "$output" == *'"permissionDecision":"allow"'* ]]
    # Log should be absent or empty on happy path.
    local log="$TEST_HOME/.claude/logs/test-branch/rtk-hook.log"
    [ ! -f "$log" ] || [ ! -s "$log" ]
}

# === Test 3: rtk emits non-JSON → wrapper falls back to {} + logs ===

@test "rtk-hook-wrapper: non-JSON output → wrapper emits {} + captures garbage" {
    local stub="$TEST_TMPDIR/stub-bin"
    make_stub_rtk "$stub" 'this is not JSON at all' 0
    run run_wrapper "$stub"
    [ "$status" -eq 0 ]
    [[ "$output" == "{}" ]]
    local log="$TEST_HOME/.claude/logs/test-branch/rtk-hook.log"
    [ -f "$log" ]
    grep -q "this is not JSON" "$log"
}

# === Test 4: rtk segfaults (rc=139) → wrapper still exits 0 + {} ===

@test "rtk-hook-wrapper: rtk rc=139 (simulated segfault) → exit 0 + {}" {
    local stub="$TEST_TMPDIR/stub-bin"
    make_stub_rtk "$stub" '' 139
    run run_wrapper "$stub"
    [ "$status" -eq 0 ]
    [[ "$output" == "{}" ]]
    local log="$TEST_HOME/.claude/logs/test-branch/rtk-hook.log"
    [ -f "$log" ]
    grep -qE "rc=139" "$log"
}

# === Test 5: REGRESSION GUARD — wrapper forwards stdin to rtk ===
#
# Without this guarantee the wrapper would consume the Claude Code payload
# itself and rtk would always degrade, silently disabling token rewrites
# even on a healthy binary. THIS is the test that catches the original bug.

@test "rtk-hook-wrapper: stdin is forwarded to rtk (regression guard)" {
    local stub="$TEST_TMPDIR/stub-bin"
    local capture="$TEST_TMPDIR/stdin-seen.txt"
    make_stub_rtk "$stub" '{"hookSpecificOutput":{"permissionDecision":"allow"}}' 0 "$capture"
    local payload='{"session_id":"abc-marker-xyz","tool_input":{"command":"git status"}}'
    run run_wrapper "$stub" "$payload"
    [ "$status" -eq 0 ]
    [ -f "$capture" ]
    grep -q "abc-marker-xyz" "$capture"
}

# === Test 5b: REGRESSION GUARD — stderr warning must not corrupt stdout JSON ===
#
# rtk could plausibly emit a deprecation notice or info log on stderr while
# producing valid JSON on stdout. The first wrapper draft merged stderr into
# stdout via 2>&1 before validating JSON, so the warning would corrupt the
# stream and the wrapper would fall back to {} — silently disabling rtk for
# that call. Fix: separate streams. The stderr payload is logged but the
# stdout JSON is what gets validated and forwarded.

@test "rtk-hook-wrapper: rtk emits stderr warning + valid stdout JSON → JSON passes through" {
    local stub="$TEST_TMPDIR/stub-bin"
    local payload='{"hookSpecificOutput":{"permissionDecision":"allow"}}'
    make_stub_rtk "$stub" "$payload" 0 "" "warning: deprecated flag"
    run run_wrapper "$stub"
    [ "$status" -eq 0 ]
    [[ "$output" == *'"permissionDecision":"allow"'* ]]
    # The stderr warning must end up in the log, not corrupt stdout.
    local log="$TEST_HOME/.claude/logs/test-branch/rtk-hook.log"
    [ -f "$log" ]
    grep -q "deprecated flag" "$log"
}

# === Test 6: log dir auto-created when ~/.claude/logs/<branch>/ missing ===

@test "rtk-hook-wrapper: creates log dir on first run" {
    [ ! -d "$TEST_HOME/.claude/logs" ]
    run run_wrapper ""  # rtk absent path triggers log write
    [ "$status" -eq 0 ]
    [ -d "$TEST_HOME/.claude/logs/test-branch" ]
}

# === Test 7: settings.json declares the wrapper with timeout=5 (static) ===

@test "rtk-hook-wrapper: settings.json wires the wrapper at timeout=5" {
    local settings="${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/settings.json"
    [ -f "$settings" ]
    # Exactly one PreToolUse Bash hook entry must reference the wrapper, with timeout=5.
    run jq -e '
        [.hooks.PreToolUse[]
         | select(.matcher == "Bash")
         | .hooks[]
         | select(.command == "/home/vscode/.claude/scripts/rtk-hook-claude.sh")
         | .timeout] | length == 1 and (.[0] | . == 5)
    ' "$settings"
    [ "$status" -eq 0 ]
}
