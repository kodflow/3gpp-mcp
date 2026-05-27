#!/bin/bash
# ============================================================================
# dev-peek.sh - Collect dev context for lint/test/feature in a single JSON call
# Usage: dev-peek.sh [base_branch] [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces ~10 sequential tool calls with 1 script call.
# Used by: /lint (Phase 1), /test (Phase 1), /feature (Phase 1)
# ============================================================================

set +e

BASE_BRANCH="${1:-main}"
PROJECT_DIR="${2:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- Changed files vs base ---
CHANGED_VS_BASE="[]"
if git rev-parse --verify "origin/$BASE_BRANCH" >/dev/null 2>&1; then
    CHANGED_VS_BASE=$(git diff "origin/$BASE_BRANCH"...HEAD --name-only 2>/dev/null | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
elif git rev-parse --verify "$BASE_BRANCH" >/dev/null 2>&1; then
    CHANGED_VS_BASE=$(git diff "$BASE_BRANCH"...HEAD --name-only 2>/dev/null | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
fi

UNSTAGED=$(git diff --name-only 2>/dev/null | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")

# --- Language detection by changed files ---
GO_DETECTED=false; RS_DETECTED=false; TS_DETECTED=false; PY_DETECTED=false; SH_DETECTED=false
JAVA_DETECTED=false; RB_DETECTED=false; PHP_DETECTED=false; EX_DETECTED=false; DART_DETECTED=false

ALL_CHANGED=$(echo "$CHANGED_VS_BASE $UNSTAGED" | jq -s 'add | unique' 2>/dev/null || echo "[]")

if echo "$ALL_CHANGED" | grep -q '\.go"'; then GO_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.rs"'; then RS_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -qE '\.(ts|tsx|js|jsx)"'; then TS_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.py"'; then PY_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.sh"'; then SH_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.java"'; then JAVA_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.rb"'; then RB_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.php"'; then PHP_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.ex"'; then EX_DETECTED=true; fi
if echo "$ALL_CHANGED" | grep -q '\.dart"'; then DART_DETECTED=true; fi

# --- Count by language ---
count_ext() { local c; c=$(echo "$ALL_CHANGED" | jq -r '.[]' 2>/dev/null | grep -cE "$1" 2>/dev/null); echo "${c:-0}"; }
GO_COUNT=$(count_ext '\.go$')
RS_COUNT=$(count_ext '\.rs$')
TS_COUNT=$(count_ext '\.(ts|tsx|js|jsx)$')
PY_COUNT=$(count_ext '\.py$')
SH_COUNT=$(count_ext '\.sh$')

# --- Available linters ---
check_cmd() { command -v "$1" >/dev/null 2>&1 && echo "true" || echo "false"; }

GOLANGCI=$(check_cmd golangci-lint)
SHELLCHECK=$(check_cmd shellcheck)
ESLINT=$(check_cmd eslint)
RUFF=$(check_cmd ruff)
CLIPPY=$(check_cmd cargo)
RUBOCOP=$(check_cmd rubocop)
PHPSTAN=$(check_cmd phpstan)
MYPY=$(check_cmd mypy)

# --- Makefile targets ---
MAKE_LINT=false; MAKE_TEST=false; MAKE_FMT=false; MAKE_BUILD=false; MAKE_TYPECHECK=false
if [ -f "$PROJECT_DIR/Makefile" ]; then
    MAKE_TARGETS=$(grep -oE '^[a-zA-Z_-]+:' "$PROJECT_DIR/Makefile" 2>/dev/null | tr -d ':')
    echo "$MAKE_TARGETS" | grep -qw "lint" && MAKE_LINT=true
    echo "$MAKE_TARGETS" | grep -qw "test" && MAKE_TEST=true
    echo "$MAKE_TARGETS" | grep -qwE "fmt|format" && MAKE_FMT=true
    echo "$MAKE_TARGETS" | grep -qw "build" && MAKE_BUILD=true
    echo "$MAKE_TARGETS" | grep -qwE "typecheck|types" && MAKE_TYPECHECK=true
fi

# --- Test frameworks ---
BATS_AVAILABLE=$(check_cmd bats)
PYTEST_AVAILABLE=$(check_cmd pytest)
VITEST_AVAILABLE=false
if [ -f "$PROJECT_DIR/node_modules/.bin/vitest" ] || command -v vitest >/dev/null 2>&1; then
    VITEST_AVAILABLE=true
fi
GO_TEST_AVAILABLE=false
if [ -f "$PROJECT_DIR/go.mod" ]; then GO_TEST_AVAILABLE=true; fi
CARGO_TEST_AVAILABLE=false
if [ -f "$PROJECT_DIR/Cargo.toml" ]; then CARGO_TEST_AVAILABLE=true; fi

# --- Playwright status ---
PW_INSTALLED=false
PW_MCP=false
PW_BROWSER="not_installed"
if [ -f "$PROJECT_DIR/node_modules/.bin/playwright" ] || command -v playwright >/dev/null 2>&1; then
    PW_INSTALLED=true
    PW_BROWSER="installed"
fi
if grep -q "playwright" "$HOME/.claude/mcp.json" 2>/dev/null || grep -q "playwright" "$PROJECT_DIR/mcp.json" 2>/dev/null; then
    PW_MCP=true
fi

# --- Features state ---
FEAT_VERSION=0; FEAT_COUNT=0
if [ -f "$PROJECT_DIR/.claude/features.json" ]; then
    FEAT_VERSION=$(jq -r '.version // 0' "$PROJECT_DIR/.claude/features.json" 2>/dev/null || echo 0)
    FEAT_COUNT=$(jq -r '.features | length // 0' "$PROJECT_DIR/.claude/features.json" 2>/dev/null || echo 0)
fi

# --- Output JSON ---
jq -n \
    --argjson changed_vs_base "$CHANGED_VS_BASE" \
    --argjson unstaged "$UNSTAGED" \
    --arg base "$BASE_BRANCH" \
    --argjson go "$GO_COUNT" --argjson rs "$RS_COUNT" --argjson ts "$TS_COUNT" --argjson py "$PY_COUNT" --argjson sh "$SH_COUNT" \
    --argjson go_detected "$GO_DETECTED" --argjson rs_detected "$RS_DETECTED" --argjson ts_detected "$TS_DETECTED" \
    --argjson py_detected "$PY_DETECTED" --argjson sh_detected "$SH_DETECTED" \
    --argjson java_detected "$JAVA_DETECTED" --argjson rb_detected "$RB_DETECTED" \
    --argjson php_detected "$PHP_DETECTED" --argjson ex_detected "$EX_DETECTED" --argjson dart_detected "$DART_DETECTED" \
    --argjson golangci "$GOLANGCI" --argjson shellcheck_avail "$SHELLCHECK" --argjson eslint_avail "$ESLINT" \
    --argjson ruff_avail "$RUFF" --argjson clippy_avail "$CLIPPY" --argjson rubocop_avail "$RUBOCOP" \
    --argjson phpstan_avail "$PHPSTAN" --argjson mypy_avail "$MYPY" \
    --argjson make_lint "$MAKE_LINT" --argjson make_test "$MAKE_TEST" --argjson make_fmt "$MAKE_FMT" \
    --argjson make_build "$MAKE_BUILD" --argjson make_typecheck "$MAKE_TYPECHECK" \
    --argjson bats "$BATS_AVAILABLE" --argjson pytest "$PYTEST_AVAILABLE" --argjson vitest "$VITEST_AVAILABLE" \
    --argjson go_test "$GO_TEST_AVAILABLE" --argjson cargo_test "$CARGO_TEST_AVAILABLE" \
    --argjson pw_installed "$PW_INSTALLED" --argjson pw_mcp "$PW_MCP" --arg pw_browser "$PW_BROWSER" \
    --argjson feat_ver "$FEAT_VERSION" --argjson feat_count "$FEAT_COUNT" \
    '{
        changed_files: {vs_base: $changed_vs_base, unstaged: $unstaged, base_branch: $base, by_language: {go: $go, rust: $rs, typescript: $ts, python: $py, shell: $sh}},
        languages: {go: $go_detected, rust: $rs_detected, typescript: $ts_detected, python: $py_detected, shell: $sh_detected, java: $java_detected, ruby: $rb_detected, php: $php_detected, elixir: $ex_detected, dart: $dart_detected},
        linters: {golangci_lint: $golangci, shellcheck: $shellcheck_avail, eslint: $eslint_avail, ruff: $ruff_avail, clippy: $clippy_avail, rubocop: $rubocop_avail, phpstan: $phpstan_avail, mypy: $mypy_avail},
        makefile: {exists: ($make_lint or $make_test or $make_fmt or $make_build), lint: $make_lint, test: $make_test, fmt: $make_fmt, build: $make_build, typecheck: $make_typecheck},
        test_frameworks: {bats: $bats, pytest: $pytest, vitest: $vitest, go_test: $go_test, cargo_test: $cargo_test},
        playwright: {installed: $pw_installed, mcp_available: $pw_mcp, browser_status: $pw_browser},
        features: {version: $feat_ver, count: $feat_count}
    }'
