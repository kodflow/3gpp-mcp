#!/bin/bash
# ============================================================================
# test-utils.sh - Test utilities for hook scripts
# ============================================================================
# Pattern: spawn hook script with JSON stdin, validate exit code and output.
# Inspired by ECC test framework, adapted for shell-only environment.
# ============================================================================

TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0
FAILURES_LOG=""

# Colors (if terminal supports it)
if [ -t 1 ]; then
    GREEN='\033[0;32m'
    RED='\033[0;31m'
    NC='\033[0m'
else
    GREEN="" RED="" NC=""
fi

# Run a hook test
# Usage: test_hook "test name" "script path" "stdin json" expected_exit [expected_stderr_pattern]
test_hook() {
    local name="$1"
    local script="$2"
    local input="$3"
    local expected_exit="${4:-0}"
    local expected_stderr_pattern="${5:-}"

    TESTS_RUN=$((TESTS_RUN + 1))

    local stdout stderr actual_exit
    local tmp_stderr
    tmp_stderr=$(mktemp)
    stdout=$(echo "$input" | bash "$script" 2>"$tmp_stderr")
    actual_exit=$?
    stderr=$(cat "$tmp_stderr" 2>/dev/null)
    rm -f "$tmp_stderr"

    local passed=true

    # Check exit code
    if [ "$actual_exit" -ne "$expected_exit" ]; then
        passed=false
    fi

    # Check stderr pattern if specified
    if [ -n "$expected_stderr_pattern" ] && ! echo "$stderr" | grep -qE "$expected_stderr_pattern"; then
        passed=false
    fi

    if $passed; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        printf "${GREEN}  PASS${NC}: %s\n" "$name"
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        printf "${RED}  FAIL${NC}: %s\n" "$name"
        [ "$actual_exit" -ne "$expected_exit" ] && \
            printf "    Expected exit: %d, got: %d\n" "$expected_exit" "$actual_exit"
        [ -n "$expected_stderr_pattern" ] && ! echo "$stderr" | grep -qE "$expected_stderr_pattern" && \
            printf "    Expected stderr pattern: %s\n    Actual stderr: %s\n" "$expected_stderr_pattern" "$stderr"
        FAILURES_LOG="${FAILURES_LOG}\n  - $name"
    fi
}

# Run a hook test and check stdout contains a pattern
# Usage: test_hook_stdout "test name" "script path" "stdin json" expected_exit "stdout_pattern"
test_hook_stdout() {
    local name="$1"
    local script="$2"
    local input="$3"
    local expected_exit="${4:-0}"
    local expected_stdout_pattern="${5:-}"

    TESTS_RUN=$((TESTS_RUN + 1))

    local stdout actual_exit
    local tmp_stderr
    tmp_stderr=$(mktemp)
    stdout=$(echo "$input" | bash "$script" 2>"$tmp_stderr")
    actual_exit=$?
    rm -f "$tmp_stderr"

    local passed=true

    if [ "$actual_exit" -ne "$expected_exit" ]; then
        passed=false
    fi

    if [ -n "$expected_stdout_pattern" ] && ! echo "$stdout" | grep -qE "$expected_stdout_pattern"; then
        passed=false
    fi

    if $passed; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        printf "${GREEN}  PASS${NC}: %s\n" "$name"
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        printf "${RED}  FAIL${NC}: %s\n" "$name"
        [ "$actual_exit" -ne "$expected_exit" ] && \
            printf "    Expected exit: %d, got: %d\n" "$expected_exit" "$actual_exit"
        [ -n "$expected_stdout_pattern" ] && ! echo "$stdout" | grep -qE "$expected_stdout_pattern" && \
            printf "    Expected stdout pattern: %s\n    Actual stdout: %.200s\n" "$expected_stdout_pattern" "$stdout"
        FAILURES_LOG="${FAILURES_LOG}\n  - $name"
    fi
}

# Print test suite summary
print_summary() {
    local suite="${1:-Tests}"
    echo ""
    echo "═══════════════════════════════════════════════"
    printf "  %s: %d run, " "$suite" "$TESTS_RUN"
    printf "${GREEN}%d passed${NC}, " "$TESTS_PASSED"
    if [ "$TESTS_FAILED" -gt 0 ]; then
        printf "${RED}%d failed${NC}\n" "$TESTS_FAILED"
        printf "\n  Failed tests:%b\n" "$FAILURES_LOG"
    else
        printf "0 failed\n"
    fi
    echo "═══════════════════════════════════════════════"
    return $TESTS_FAILED
}

# Build a PreToolUse Bash JSON input
# Usage: make_bash_input "git commit -m 'test'"
make_bash_input() {
    local cmd="$1"
    local escaped
    escaped=$(printf '%s' "$cmd" | sed 's/\\/\\\\/g; s/"/\\"/g')
    printf '{"tool_name":"Bash","tool_input":{"command":"%s"}}' "$escaped"
}

# Build a PreToolUse input for non-Bash tools
make_tool_input() {
    local tool="$1"
    local escaped
    escaped=$(printf '%s' "$tool" | sed 's/\\/\\\\/g; s/"/\\"/g')
    printf '{"tool_name":"%s","tool_input":{}}' "$escaped"
}
