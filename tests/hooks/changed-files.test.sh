#!/bin/bash
# ============================================================================
# changed-files.test.sh - Tests for get_branch_changed_files() in common.sh
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=test-utils.sh disable=SC1091
source "$SCRIPT_DIR/test-utils.sh"
# shellcheck source=/dev/null disable=SC1091
source "$HOME/.claude/scripts/common.sh"

echo "Testing: get_branch_changed_files()"
echo "───────────────────────────────────────────────"

# Create a fresh git repo for tests
TEST_REPO=$(mktemp -d)
git -C "$TEST_REPO" init -b main >/dev/null 2>&1
git -C "$TEST_REPO" config user.email "test@test.com"
git -C "$TEST_REPO" config user.name "Test"
touch "$TEST_REPO/README.md"
git -C "$TEST_REPO" add README.md
git -C "$TEST_REPO" commit -m "init" >/dev/null 2>&1

# === Test: clean main returns empty ===
TESTS_RUN=$((TESTS_RUN + 1))
result=$(get_branch_changed_files "main" "$TEST_REPO")
if [ -z "$result" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "clean main returns empty"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "clean main returns empty"
    printf "    Expected empty, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - clean main returns empty"
fi

# === Test: unstaged changes on main ===
TESTS_RUN=$((TESTS_RUN + 1))
echo "modified" > "$TEST_REPO/README.md"
result=$(get_branch_changed_files "main" "$TEST_REPO")
if [ "$result" = "README.md" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "unstaged changes on main"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "unstaged changes on main"
    printf "    Expected: README.md, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - unstaged changes on main"
fi
git -C "$TEST_REPO" checkout -- README.md 2>/dev/null

# === Test: staged changes on main ===
TESTS_RUN=$((TESTS_RUN + 1))
echo "package main" > "$TEST_REPO/new.go"
git -C "$TEST_REPO" add new.go
result=$(get_branch_changed_files "main" "$TEST_REPO")
if [ "$result" = "new.go" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "staged changes on main"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "staged changes on main"
    printf "    Expected: new.go, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - staged changes on main"
fi
git -C "$TEST_REPO" reset HEAD new.go >/dev/null 2>&1
rm -f "$TEST_REPO/new.go"

# === Test: branch diff on feature branch ===
TESTS_RUN=$((TESTS_RUN + 1))
git -C "$TEST_REPO" checkout -b feat/test >/dev/null 2>&1
echo "package main" > "$TEST_REPO/main.go"
git -C "$TEST_REPO" add main.go
git -C "$TEST_REPO" commit -m "add main.go" >/dev/null 2>&1
result=$(get_branch_changed_files "main" "$TEST_REPO")
if [ "$result" = "main.go" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "branch diff on feature branch"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "branch diff on feature branch"
    printf "    Expected: main.go, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - branch diff on feature branch"
fi

# === Test: combines branch diff and unstaged ===
TESTS_RUN=$((TESTS_RUN + 1))
echo "modified" > "$TEST_REPO/README.md"
result=$(get_branch_changed_files "main" "$TEST_REPO")
has_main=$(echo "$result" | grep -c "main.go")
has_readme=$(echo "$result" | grep -c "README.md")
if [ "$has_main" -eq 1 ] && [ "$has_readme" -eq 1 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "combines branch diff and unstaged"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "combines branch diff and unstaged"
    printf "    Expected main.go + README.md, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - combines branch diff and unstaged"
fi
git -C "$TEST_REPO" checkout -- README.md 2>/dev/null

# === Test: deduplicates files ===
TESTS_RUN=$((TESTS_RUN + 1))
echo "package main // v2" > "$TEST_REPO/main.go"
result=$(get_branch_changed_files "main" "$TEST_REPO")
count=$(echo "$result" | grep -c "main.go")
if [ "$count" -eq 1 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "deduplicates files in multiple diffs"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "deduplicates files in multiple diffs"
    printf "    Expected 1 occurrence of main.go, got: %d\n" "$count"
    FAILURES_LOG="${FAILURES_LOG}\n  - deduplicates files in multiple diffs"
fi

# === Test: non-git directory returns empty ===
TESTS_RUN=$((TESTS_RUN + 1))
NON_GIT=$(mktemp -d)
result=$(get_branch_changed_files "main" "$NON_GIT")
if [ -z "$result" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "non-git directory returns empty"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "non-git directory returns empty"
    printf "    Expected empty, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - non-git directory returns empty"
fi
rm -rf "$NON_GIT"

# === Test: defaults base to main ===
TESTS_RUN=$((TESTS_RUN + 1))
result=$(CLAUDE_PROJECT_DIR="$TEST_REPO" get_branch_changed_files)
# Should include main.go from branch diff (still on feat/test with modified main.go)
has_main=$(echo "$result" | grep -c "main.go")
if [ "$has_main" -eq 1 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "defaults base to main"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "defaults base to main"
    printf "    Expected main.go, got: %s\n" "$result"
    FAILURES_LOG="${FAILURES_LOG}\n  - defaults base to main"
fi

# Cleanup
rm -rf "$TEST_REPO"

print_summary "get_branch_changed_files"

# Reset counters for second suite
TESTS_RUN=0 TESTS_PASSED=0 TESTS_FAILED=0 FAILURES_LOG=""

echo ""
echo "Testing: on-stop.sh session-scoped tracker"
echo "───────────────────────────────────────────────"

STOP_SCRIPT="$HOME/.claude/scripts/on-stop.sh"

# Helper: run on-stop.sh with a given SESSION_ID and tracker content
# Returns stderr (visible output) and stdout (JSON for Claude)
run_stop_with_tracker() {
    local sid="$1"
    local tracker_content="$2"
    local tracker_file="/tmp/.claude-edited-files-${sid}"

    # Setup tracker
    if [ -n "$tracker_content" ]; then
        printf '%s\n' "$tracker_content" > "$tracker_file"
    else
        rm -f "$tracker_file"
    fi

    # Run with minimal JSON input
    local tmp_stderr
    tmp_stderr=$(mktemp)
    printf '{"session_id":"%s"}' "$sid" | \
        CLAUDE_PROJECT_DIR="/workspace" bash "$STOP_SCRIPT" 2>"$tmp_stderr" >/dev/null
    local stderr
    stderr=$(cat "$tmp_stderr" 2>/dev/null)
    rm -f "$tmp_stderr" "$tracker_file" "${tracker_file}.lock"

    # Return stderr for assertion
    echo "$stderr"
}

# === Test: read-only session (no tracker) skips ktn-linter ===
TESTS_RUN=$((TESTS_RUN + 1))
stderr_out=$(run_stop_with_tracker "test-readonly-$$" "")
if ! echo "$stderr_out" | grep -q "ktn-linter"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "read-only session skips ktn-linter"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "read-only session skips ktn-linter"
    printf "    Expected no ktn-linter output, got: %.100s\n" "$stderr_out"
    FAILURES_LOG="${FAILURES_LOG}\n  - read-only session skips ktn-linter"
fi

# === Test: session with non-Go files only skips ktn-linter ===
TESTS_RUN=$((TESTS_RUN + 1))
stderr_out=$(run_stop_with_tracker "test-nongo-$$" "/workspace/README.md
/workspace/docs/guide.md")
if ! echo "$stderr_out" | grep -q "ktn-linter (session-scoped"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "non-Go session skips ktn-linter lint"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "non-Go session skips ktn-linter lint"
    printf "    Expected no ktn-linter lint, got: %.100s\n" "$stderr_out"
    FAILURES_LOG="${FAILURES_LOG}\n  - non-Go session skips ktn-linter lint"
fi

# === Test: default SESSION_ID skips ktn-linter ===
TESTS_RUN=$((TESTS_RUN + 1))
tmp_stderr=$(mktemp)
printf '{}' | CLAUDE_PROJECT_DIR="/workspace" bash "$STOP_SCRIPT" 2>"$tmp_stderr" >/dev/null
stderr_out=$(cat "$tmp_stderr" 2>/dev/null)
rm -f "$tmp_stderr"
if ! echo "$stderr_out" | grep -q "ktn-linter (session-scoped"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "default session_id skips ktn-linter"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "default session_id skips ktn-linter"
    FAILURES_LOG="${FAILURES_LOG}\n  - default session_id skips ktn-linter"
fi

# === Test: debug mode logs skip reason ===
TESTS_RUN=$((TESTS_RUN + 1))
tmp_stderr=$(mktemp)
printf '{"session_id":"test-debug-%s"}' "$$" | \
    CLAUDE_PROJECT_DIR="/workspace" CLAUDE_HOOK_DEBUG=1 bash "$STOP_SCRIPT" 2>"$tmp_stderr" >/dev/null
stderr_out=$(cat "$tmp_stderr" 2>/dev/null)
rm -f "$tmp_stderr"
if echo "$stderr_out" | grep -q "skipped (session read-only)"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "debug mode shows skip reason"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "debug mode shows skip reason"
    printf "    Expected 'skipped (session read-only)', got: %.200s\n" "$stderr_out"
    FAILURES_LOG="${FAILURES_LOG}\n  - debug mode shows skip reason"
fi

# === Test: empty tracker (0 bytes) skips ktn-linter ===
TESTS_RUN=$((TESTS_RUN + 1))
EMPTY_SID="test-empty-$$"
touch "/tmp/.claude-edited-files-${EMPTY_SID}"
tmp_stderr=$(mktemp)
printf '{"session_id":"%s"}' "$EMPTY_SID" | \
    CLAUDE_PROJECT_DIR="/workspace" bash "$STOP_SCRIPT" 2>"$tmp_stderr" >/dev/null
stderr_out=$(cat "$tmp_stderr" 2>/dev/null)
rm -f "$tmp_stderr" "/tmp/.claude-edited-files-${EMPTY_SID}" "/tmp/.claude-edited-files-${EMPTY_SID}.lock"
if ! echo "$stderr_out" | grep -q "ktn-linter (session-scoped"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: %s\n" "empty tracker skips ktn-linter"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: %s\n" "empty tracker skips ktn-linter"
    FAILURES_LOG="${FAILURES_LOG}\n  - empty tracker skips ktn-linter"
fi

print_summary "on-stop.sh session tracker"
