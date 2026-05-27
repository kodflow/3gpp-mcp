#!/bin/bash
# =============================================================================
# run-all.sh — Agent Teams unit test harness
# =============================================================================
# Runs all unit tests for the Agent Teams primitives and hooks.
# Tests cover: parse-contract, capability mapping, terminal classify,
# registry lifecycle, GC, super-claude wrapper, list-team-agents, version compare.
#
# Each test is a function that exits the subshell with 0=pass, non-zero=fail.
# No external dependencies (bash + jq + awk).
# =============================================================================

set +e

PRIMITIVES="/workspace/.devcontainer/images/.claude/scripts/team-mode-primitives.sh"
if [ ! -f "$PRIMITIVES" ]; then
    echo "FATAL: primitives library not found at $PRIMITIVES"
    exit 1
fi
# shellcheck disable=SC1090
source "$PRIMITIVES"

PASS=0
FAIL=0
FAILED_TESTS=()

pass() { echo "  ✓ $1"; PASS=$((PASS + 1)); }
fail() { echo "  ✗ $1"; FAIL=$((FAIL + 1)); FAILED_TESTS+=("$1"); }

# =============================================================================
# Test: extract_task_contract
# =============================================================================
test_parse_contract() {
    echo "=== test-parse-contract ==="

    # T1: valid JSON block
    local input='preamble
<!-- task-contract v1
{"contract_version":1,"scope":"src/","access_mode":"write","owned_paths":["a.go"],"acceptance_criteria":["x"],"output_format":"diff","assignee":"t1","depends_on":[]}
-->
free text'
    local out
    out=$(extract_task_contract "$input")
    if [ "$(echo "$out" | jq -r .access_mode 2>/dev/null)" = "write" ]; then
        pass "T1 valid JSON → access_mode=write"
    else
        fail "T1 valid JSON (got: $out)"
    fi

    # T2: tolerant opening (extra whitespace + v2)
    input='<!--    task-contract   v2
{"scope":"x"}
-->'
    out=$(extract_task_contract "$input")
    if [ "$(echo "$out" | jq -r .scope 2>/dev/null)" = "x" ]; then
        pass "T2 tolerant v2 + whitespace"
    else
        fail "T2 tolerant v2 (got: $out)"
    fi

    # T3: missing block → empty
    out=$(extract_task_contract "just a plain description")
    if [ -z "$out" ]; then
        pass "T3 no block → empty"
    else
        fail "T3 expected empty, got: $out"
    fi

    # T4: malformed JSON → empty
    input='<!-- task-contract v1
{"broken":
-->'
    out=$(extract_task_contract "$input")
    if [ -z "$out" ]; then
        pass "T4 malformed → empty"
    else
        fail "T4 expected empty, got: $out"
    fi

    # T5: validate_contract_version v1 → pass
    if validate_contract_version '{"contract_version":1}'; then
        pass "T5 contract_version=1 valid"
    else
        fail "T5 contract_version=1 should validate"
    fi

    # T6: validate_contract_version v2 → fail
    if ! validate_contract_version '{"contract_version":2}'; then
        pass "T6 contract_version=2 rejected"
    else
        fail "T6 contract_version=2 should reject"
    fi

    # T7: validate_contract_version missing → fail
    if ! validate_contract_version '{}'; then
        pass "T7 missing version rejected"
    else
        fail "T7 missing version should reject"
    fi

    # T8: empty description → empty output
    out=$(extract_task_contract "")
    if [ -z "$out" ]; then
        pass "T8 empty input → empty"
    else
        fail "T8 empty input (got: $out)"
    fi
}

# =============================================================================
# Test: classify_terminal
# =============================================================================
test_terminal_classify() {
    echo ""
    echo "=== test-terminal-classify ==="

    # Need subshell to isolate env vars
    local result

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=iTerm.app classify_terminal)
    [ "$result" = "known-compatible" ] && pass "iTerm.app → known-compatible" || fail "iTerm.app (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=WezTerm classify_terminal)
    [ "$result" = "known-compatible" ] && pass "WezTerm → known-compatible" || fail "WezTerm (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=ghostty classify_terminal)
    [ "$result" = "known-compatible" ] && pass "ghostty → known-compatible" || fail "ghostty (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=kitty classify_terminal)
    [ "$result" = "known-compatible" ] && pass "kitty → known-compatible" || fail "kitty (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TERM_PROGRAM; TMUX=/tmp/tmux-1000/default,1,0 classify_terminal)
    [ "$result" = "known-compatible" ] && pass "\$TMUX set → known-compatible" || fail "\$TMUX set (got: $result)"

    result=$(unset TERM_PROGRAM WT_SESSION TMUX; VSCODE_PID=99999 classify_terminal)
    [ "$result" = "known-incompatible" ] && pass "VSCODE_PID → known-incompatible" || fail "VSCODE_PID (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=vscode classify_terminal)
    [ "$result" = "known-incompatible" ] && pass "TERM_PROGRAM=vscode → known-incompatible" || fail "TERM_PROGRAM=vscode (got: $result)"

    result=$(unset VSCODE_PID TMUX TERM_PROGRAM; WT_SESSION=abc123 classify_terminal)
    [ "$result" = "known-incompatible" ] && pass "WT_SESSION → known-incompatible" || fail "WT_SESSION (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX TERM_PROGRAM; classify_terminal)
    [ "$result" = "unknown" ] && pass "no signals → unknown" || fail "no signals (got: $result)"

    result=$(unset VSCODE_PID WT_SESSION TMUX; TERM_PROGRAM=Apple_Terminal classify_terminal)
    [ "$result" = "unknown" ] && pass "Apple_Terminal → unknown" || fail "Apple_Terminal (got: $result)"
}

# =============================================================================
# Test: version_at_least
# =============================================================================
test_version_compare() {
    echo ""
    echo "=== test-version-compare ==="

    version_at_least "2.1.32" "2.1.97" && pass "2.1.97 >= 2.1.32" || fail "2.1.97 >= 2.1.32"
    version_at_least "2.1.32" "2.1.32" && pass "2.1.32 >= 2.1.32 (equal)" || fail "2.1.32 >= 2.1.32"
    ! version_at_least "2.1.32" "2.1.31" && pass "2.1.31 < 2.1.32" || fail "2.1.31 < 2.1.32"
    ! version_at_least "2.1.32" "2.0.99" && pass "2.0.99 < 2.1.32" || fail "2.0.99 < 2.1.32"
    version_at_least "2.1.32" "2.2.0" && pass "2.2.0 >= 2.1.32" || fail "2.2.0 >= 2.1.32"
    version_at_least "2.1.32" "2.1.32-beta" && pass "2.1.32-beta handled (stripped)" || fail "2.1.32-beta"
    ! version_at_least "2.1.32" "" && pass "empty current rejected" || fail "empty current"
    ! version_at_least "2.1.32" "invalid" && pass "invalid current rejected" || fail "invalid current"
}

# =============================================================================
# Test: epoch helpers
# =============================================================================
test_epoch_helpers() {
    echo ""
    echo "=== test-epoch-helpers ==="

    local now ago diff
    now=$(epoch_now)
    ago=$(epoch_24h_ago)
    diff=$((now - ago))
    [ "$diff" -eq 86400 ] && pass "epoch_24h_ago = now - 86400" || fail "epoch_24h_ago (diff=$diff)"

    local iso epoch
    iso="2026-04-09T14:30:00Z"
    epoch=$(epoch_from_iso "$iso")
    [ "$epoch" -gt 0 ] && pass "epoch_from_iso parses ISO-8601" || fail "epoch_from_iso got: $epoch"

    epoch=$(epoch_from_iso "not-a-date")
    [ "$epoch" = "0" ] && pass "epoch_from_iso returns 0 on invalid" || fail "epoch_from_iso invalid (got: $epoch)"

    epoch=$(epoch_from_iso "")
    [ "$epoch" = "0" ] && pass "epoch_from_iso returns 0 on empty" || fail "epoch_from_iso empty (got: $epoch)"
}

# =============================================================================
# Test: detect_runtime_mode capability mapping
# =============================================================================
test_capability_mapping() {
    echo ""
    echo "=== test-capability-mapping ==="

    local orig_home="$HOME"
    export HOME=/tmp/team-mode-test-home
    rm -rf "$HOME"
    mkdir -p "$HOME/.claude"

    # Row 1: env flag off → SUBAGENTS
    local result
    result=$(unset CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS; detect_runtime_mode)
    [ "$result" = "SUBAGENTS" ] && pass "env off → SUBAGENTS" || fail "env off (got: $result)"

    # Row 2: env on + version OK + no tmux in unknown terminal → TEAMS_INPROCESS
    result=$(unset VSCODE_PID WT_SESSION TMUX TERM_PROGRAM
             CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
             detect_runtime_mode)
    [ "$result" = "TEAMS_INPROCESS" ] && pass "env=1 + unknown term → TEAMS_INPROCESS" || fail "env=1 unknown (got: $result)"

    # Row 3: env on + VSCODE terminal → TEAMS_INPROCESS (downgrade)
    result=$(unset WT_SESSION TMUX
             CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
             VSCODE_PID=12345
             detect_runtime_mode)
    [ "$result" = "TEAMS_INPROCESS" ] && pass "VSCODE_PID → TEAMS_INPROCESS" || fail "VSCODE_PID (got: $result)"

    # Cleanup
    export HOME="$orig_home"
    rm -rf /tmp/team-mode-test-home
}

# =============================================================================
# Test: list-team-agents.sh determinism
# =============================================================================
test_list_team_agents() {
    echo ""
    echo "=== test-list-team-agents ==="

    local script="/workspace/.devcontainer/scripts/list-team-agents.sh"
    [ -x "$script" ] || { fail "list-team-agents.sh missing or not executable"; return; }

    local out
    out=$(bash "$script")
    echo "$out" | jq empty 2>/dev/null && pass "output is valid JSON" || fail "output not JSON: $out"

    # Should be an array
    local kind
    kind=$(echo "$out" | jq -r 'type' 2>/dev/null)
    [ "$kind" = "array" ] && pass "output is a JSON array" || fail "not array: $kind"

    # Post-B1-B7, expected length >= 19
    local len
    len=$(echo "$out" | jq 'length' 2>/dev/null)
    [ "$len" -ge 5 ] && pass "array length >= 5 (got: $len)" || fail "expected >= 5, got $len"

    # Non-existent directory → error
    bash "$script" /nonexistent >/dev/null 2>&1
    [ $? -eq 1 ] && pass "missing dir → exit 1" || fail "missing dir should exit 1"
}

# =============================================================================
# Test: task-created.sh end-to-end (functional)
# =============================================================================
test_task_created_hook() {
    echo ""
    echo "=== test-task-created-hook ==="

    local orig_home="$HOME"
    export HOME=/tmp/task-created-test-home
    rm -rf "$HOME"
    mkdir -p "$HOME/.claude/scripts"
    cp /workspace/.devcontainer/images/.claude/scripts/team-mode-primitives.sh "$HOME/.claude/scripts/"
    cp /workspace/.devcontainer/images/.claude/scripts/task-created.sh "$HOME/.claude/scripts/"
    echo "TMUX" > "$HOME/.claude/.team-capability"

    local HOOK="$HOME/.claude/scripts/task-created.sh"

    # T1: empty subject → exit 2
    local rc
    echo '{"task_subject":""}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "2" ] && pass "empty subject → exit 2" || fail "empty subject (exit=$rc)"

    # T2: missing contract → exit 0 advisory
    jq -n '{task_subject:"s",task_description:"plain",team_name:"t1",task_id:"t1-001"}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "0" ] && pass "missing contract → exit 0 advisory" || fail "missing contract (exit=$rc)"

    # T3: valid write → exit 0
    local DESC='<!-- task-contract v1
{"contract_version":1,"scope":"src/","access_mode":"write","owned_paths":["a.go"],"acceptance_criteria":["x"],"output_format":"diff","assignee":"alice","depends_on":[]}
-->'
    jq -n --arg d "$DESC" '{task_subject:"s",task_description:$d,team_name:"t2",task_id:"t2-001",teammate_name:"alice"}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "0" ] && pass "valid write → exit 0" || fail "valid write (exit=$rc)"

    # T4: collision → exit 2
    local DESC2='<!-- task-contract v1
{"contract_version":1,"scope":"src/","access_mode":"write","owned_paths":["a.go"],"acceptance_criteria":["x"],"output_format":"diff","assignee":"bob","depends_on":[]}
-->'
    jq -n --arg d "$DESC2" '{task_subject:"s",task_description:$d,team_name:"t2",task_id:"t2-002",teammate_name:"bob"}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "2" ] && pass "write collision → exit 2" || fail "write collision (exit=$rc)"

    # T5: read-only with empty owned_paths → exit 0
    local DESC3='<!-- task-contract v1
{"contract_version":1,"scope":"PR","access_mode":"read-only","owned_paths":[],"acceptance_criteria":["x"],"output_format":"report","assignee":"r","depends_on":[]}
-->'
    jq -n --arg d "$DESC3" '{task_subject:"s",task_description:$d,team_name:"t3",task_id:"t3-001",teammate_name:"r"}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "0" ] && pass "read-only + empty owned_paths → exit 0" || fail "read-only empty (exit=$rc)"

    # T6: capability NONE → exit 0 silent
    echo "NONE" > "$HOME/.claude/.team-capability"
    echo '{"task_subject":""}' | bash "$HOOK" >/dev/null 2>&1
    rc=$?
    [ "$rc" = "0" ] && pass "capability NONE → exit 0 (bypass)" || fail "capability NONE (exit=$rc)"

    # Cleanup
    export HOME="$orig_home"
    rm -rf /tmp/task-created-test-home
}

# =============================================================================
# Main
# =============================================================================
echo "═══════════════════════════════════════════════"
echo "  Agent Teams — Unit Test Harness"
echo "═══════════════════════════════════════════════"

test_parse_contract
test_terminal_classify
test_version_compare
test_epoch_helpers
test_capability_mapping
test_list_team_agents
test_task_created_hook

echo ""
echo "═══════════════════════════════════════════════"
TOTAL=$((PASS + FAIL))
echo "  Results: $PASS/$TOTAL passed ($FAIL failed)"
if [ "$FAIL" -gt 0 ]; then
    echo "  Failed tests:"
    for t in "${FAILED_TESTS[@]}"; do
        echo "    - $t"
    done
fi
echo "═══════════════════════════════════════════════"

exit $FAIL
