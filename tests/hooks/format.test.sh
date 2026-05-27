#!/bin/bash
# ============================================================================
# format.test.sh - Tests for format.sh hook
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test-utils.sh"

HOOK="$HOME/.claude/scripts/format.sh"

echo "Testing: format.sh"
echo "───────────────────────────────────────────────"

# Create temp directory for test fixtures
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# === Basic behavior ===
bash "$HOOK" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Exit 0 with no arguments\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Should exit 0 with no arguments (got: %d)\n" "$EXIT_CODE"
fi

bash "$HOOK" "/nonexistent/file.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Exit 0 with nonexistent file\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Should exit 0 with nonexistent file (got: %d)\n" "$EXIT_CODE"
fi

# === Makefile delegation ===
mkdir -p "$TMPDIR/fmt-project"
cat > "$TMPDIR/fmt-project/Makefile" << 'MAKEFILE'
fmt:
	@echo "Makefile fmt called"
MAKEFILE
echo "x = 1" > "$TMPDIR/fmt-project/test.py"

bash "$HOOK" "$TMPDIR/fmt-project/test.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Makefile fmt target is used when available\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Makefile fmt target should be used (exit: %d)\n" "$EXIT_CODE"
fi

# Test 'format' target fallback
mkdir -p "$TMPDIR/format-project"
cat > "$TMPDIR/format-project/Makefile" << 'MAKEFILE'
format:
	@echo "Makefile format called"
MAKEFILE
echo "x = 1" > "$TMPDIR/format-project/test.py"

bash "$HOOK" "$TMPDIR/format-project/test.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Makefile 'format' target fallback works\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Makefile 'format' target should work (exit: %d)\n" "$EXIT_CODE"
fi

# === Fail-open: unknown extension ===
mkdir -p "$TMPDIR/no-fmt"
echo "content" > "$TMPDIR/no-fmt/test.xyz"

bash "$HOOK" "$TMPDIR/no-fmt/test.xyz" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Fail-open: unknown extension exits 0\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Fail-open: unknown extension should exit 0 (got: %d)\n" "$EXIT_CODE"
fi

# === Language-specific formatting ===
echo "x=1" > "$TMPDIR/no-fmt/test.py"
bash "$HOOK" "$TMPDIR/no-fmt/test.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Python file formatted without error\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Python formatting should not fail (got: %d)\n" "$EXIT_CODE"
fi

# === Summary ===
print_summary "format.sh"
exit $?
