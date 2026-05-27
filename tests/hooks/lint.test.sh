#!/bin/bash
# ============================================================================
# lint.test.sh - Tests for lint.sh hook
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test-utils.sh"

HOOK="$HOME/.claude/scripts/lint.sh"

echo "Testing: lint.sh"
echo "───────────────────────────────────────────────"

# Create temp directory for test fixtures
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# === Basic behavior ===
test_hook "Exit 0 with no arguments" \
    "$HOOK" \
    "" \
    0

test_hook "Exit 0 with nonexistent file" \
    "$HOOK" \
    "/nonexistent/file.py" \
    0

# === Makefile delegation ===
# Create a mock project with Makefile
mkdir -p "$TMPDIR/makefile-project"
cat > "$TMPDIR/makefile-project/Makefile" << 'MAKEFILE'
lint:
	@echo "Makefile lint called"
MAKEFILE
echo "print('hello')" > "$TMPDIR/makefile-project/test.py"

# Test that Makefile is preferred (lint.sh takes file as $1, not stdin)
bash "$HOOK" "$TMPDIR/makefile-project/test.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Makefile lint target is used when available\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Makefile lint target is used when available (exit: %d)\n" "$EXIT_CODE"
fi

# === Fail-open behavior ===
# Create a file with no linter and no Makefile
mkdir -p "$TMPDIR/no-tools"
echo "some content" > "$TMPDIR/no-tools/test.xyz"

bash "$HOOK" "$TMPDIR/no-tools/test.xyz" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Fail-open: unknown extension exits 0\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Fail-open: unknown extension should exit 0 (got: %d)\n" "$EXIT_CODE"
fi

# === Extension detection ===
# Create Python file to verify ruff is called (if available)
echo "x=1" > "$TMPDIR/no-tools/test.py"
bash "$HOOK" "$TMPDIR/no-tools/test.py" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Python file handled without error\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Python file should not fail (got: %d)\n" "$EXIT_CODE"
fi

# Create Go file
echo "package main" > "$TMPDIR/no-tools/test.go"
bash "$HOOK" "$TMPDIR/no-tools/test.go" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: Go file handled without error\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: Go file should not fail (got: %d)\n" "$EXIT_CODE"
fi

# === golangci-lint config gate (issue #342) ===
# The new tests run against the source script directly so they validate the
# in-tree change even before the modified scripts are deployed to ~/.claude/.
HOOK_SRC="/workspace/.devcontainer/images/.claude/scripts/lint.sh"

# Mock golangci-lint so we can detect whether it was called without depending
# on the real binary. The mock writes its argv to a sentinel and exits 0.
GOLANGCI_BIN_DIR="$TMPDIR/bin"
GOLANGCI_SENTINEL="$TMPDIR/golangci-called"
mkdir -p "$GOLANGCI_BIN_DIR"
cat > "$GOLANGCI_BIN_DIR/golangci-lint" <<EOF
#!/bin/bash
echo "\$@" > "$GOLANGCI_SENTINEL"
exit 0
EOF
chmod +x "$GOLANGCI_BIN_DIR/golangci-lint"

# Case A: Go file with .golangci.yml present → golangci-lint must be invoked
mkdir -p "$TMPDIR/go-with-config"
touch "$TMPDIR/go-with-config/go.mod"
echo "linters:" > "$TMPDIR/go-with-config/.golangci.yml"
echo "  enable: []" >> "$TMPDIR/go-with-config/.golangci.yml"
echo "package main" > "$TMPDIR/go-with-config/test.go"

rm -f "$GOLANGCI_SENTINEL"
PATH="$GOLANGCI_BIN_DIR:$PATH" bash "$HOOK_SRC" "$TMPDIR/go-with-config/test.go" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ] && [ -f "$GOLANGCI_SENTINEL" ] && grep -q -- "--config" "$GOLANGCI_SENTINEL" && grep -q -- ".golangci.yml" "$GOLANGCI_SENTINEL"; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: golangci-lint invoked with --config when .golangci.yml present\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: golangci-lint should run with --config (exit=%d sentinel=%s)\n" \
        "$EXIT_CODE" "$([ -f "$GOLANGCI_SENTINEL" ] && cat "$GOLANGCI_SENTINEL" || echo '<missing>')"
fi

# Case B: Go file without any .golangci.* → golangci-lint must NOT be invoked
mkdir -p "$TMPDIR/go-without-config"
touch "$TMPDIR/go-without-config/go.mod"
echo "package main" > "$TMPDIR/go-without-config/test.go"

rm -f "$GOLANGCI_SENTINEL"
PATH="$GOLANGCI_BIN_DIR:$PATH" bash "$HOOK_SRC" "$TMPDIR/go-without-config/test.go" >/dev/null 2>&1
EXIT_CODE=$?
TESTS_RUN=$((TESTS_RUN + 1))
if [ "$EXIT_CODE" -eq 0 ] && [ ! -f "$GOLANGCI_SENTINEL" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: golangci-lint skipped silently when no config present\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: golangci-lint should be skipped (exit=%d sentinel=%s)\n" \
        "$EXIT_CODE" "$([ -f "$GOLANGCI_SENTINEL" ] && cat "$GOLANGCI_SENTINEL" || echo '<missing>')"
fi

# === Summary ===
print_summary "lint.sh"
exit $?
