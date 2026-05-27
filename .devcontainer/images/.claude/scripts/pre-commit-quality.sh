#!/bin/bash
# ============================================================================
# pre-commit-quality.sh - Incremental quality gate for /git --commit
# Runs lint + test in PARALLEL, scoped to changed files/packages only.
#
# Usage: pre-commit-quality.sh [base_branch]
#   base_branch: branch to diff against (default: main)
#
# Exit 0 = all checks passed (or no checks needed)
# Exit 1 = failures detected (output as JSON for Claude)
#
# Supports: Go, Rust, TypeScript/JavaScript, Python, Shell, Java, C/C++,
#           Ruby, PHP, Elixir, Dart, Scala, Kotlin, Swift
# ============================================================================

set +e  # Fail-open: never block unexpectedly

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

BASE="${1:-main}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
[ -d "$PROJECT_DIR" ] || exit 0

PROJECT_ROOT=$(find_project_root "$PROJECT_DIR" "$PROJECT_DIR" 2>/dev/null)
PROJECT_ROOT="${PROJECT_ROOT:-$PROJECT_DIR}"

# === Get changed files (vs base branch + unstaged) ===
CHANGED_FILES=$(
    {
        git diff --name-only "$BASE"...HEAD 2>/dev/null
        git diff --name-only 2>/dev/null
        git diff --cached --name-only 2>/dev/null
    } | sort -u
)

[ -z "$CHANGED_FILES" ] && exit 0

# === Detect which languages have changes ===
# shellcheck disable=SC2034  # Variables reserved for future language checkers
HAS_GO=false HAS_RUST=false HAS_NODE=false HAS_PYTHON=false HAS_SHELL=false
HAS_JAVA=false HAS_CPP=false HAS_RUBY=false HAS_PHP=false HAS_ELIXIR=false
HAS_DART=false HAS_SCALA=false HAS_KOTLIN=false HAS_SWIFT=false

while IFS= read -r f; do
    case "$f" in
        *.go)                       HAS_GO=true ;;
        *.rs)                       HAS_RUST=true ;;
        *.ts|*.tsx|*.js|*.jsx|*.mjs) HAS_NODE=true ;;
        *.py)                       HAS_PYTHON=true ;;
        *.sh|*.bash)                HAS_SHELL=true ;;
        *.java)                     HAS_JAVA=true ;;
        *.c|*.cpp|*.cc|*.h|*.hpp)   HAS_CPP=true ;;
        *.rb)                       HAS_RUBY=true ;;
        *.php)                      HAS_PHP=true ;;
        *.ex|*.exs)                 HAS_ELIXIR=true ;;
        *.dart)                     HAS_DART=true ;;
        *.scala)                    HAS_SCALA=true ;;
        *.kt|*.kts)                 HAS_KOTLIN=true ;;
        *.swift)                    HAS_SWIFT=true ;;
    esac
done <<< "$CHANGED_FILES"

# === Extract changed packages/directories for scoped checks ===
# shellcheck disable=SC2034  # Used by future language checkers that scope by directory
CHANGED_DIRS=$(echo "$CHANGED_FILES" | xargs -I{} dirname {} | sort -u | head -20)

# === Build parallel check commands ===
cd "$PROJECT_ROOT" || exit 0

LINT_PID="" TEST_PID=""
LINT_OUT=$(mktemp) TEST_OUT=$(mktemp)
trap 'rm -f "$LINT_OUT" "$TEST_OUT"' EXIT

# --- Lint (background) ---
run_lint() {
    local out="$1"
    local exit_code=0

    # SCOPED-FIRST: Always lint only changed files for incremental speed.
    # Makefile targets run on the entire project → too slow for pre-commit.
    if $HAS_GO && command -v golangci-lint &>/dev/null; then
        local cfg
        cfg=$(find_golangci_config "$PROJECT_ROOT") || cfg=""
        if [ -n "$cfg" ]; then
            local go_pkgs
            go_pkgs=$(echo "$CHANGED_FILES" | grep '\.go$' | xargs -I{} dirname {} | sort -u | sed 's|^|./|' | paste -sd' ')
            # shellcheck disable=SC2086
            # Intentional word splitting on go_pkgs (space-separated list of dirs).
            golangci-lint run --config "$cfg" $go_pkgs >> "$out" 2>&1 || exit_code=1
        else
            # Emit on stderr so the skip reason is visible on the success path:
            # "$out" is only printed back when lint fails, hiding the message otherwise.
            echo "[pre-commit-quality] golangci-lint skipped (no .golangci.{yml,yaml,toml})" >&2
        fi
    fi
    if $HAS_RUST && command -v cargo &>/dev/null; then
        cargo clippy -- -D warnings >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_NODE; then
        if [ -f "$PROJECT_ROOT/package.json" ] && command -v npx &>/dev/null; then
            npx eslint $(echo "$CHANGED_FILES" | grep -E '\.(ts|tsx|js|jsx)$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
        fi
    fi
    if $HAS_PYTHON && command -v ruff &>/dev/null; then
        ruff check $(echo "$CHANGED_FILES" | grep '\.py$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_SHELL && command -v shellcheck &>/dev/null; then
        shellcheck -x $(echo "$CHANGED_FILES" | grep -E '\.(sh|bash)$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_JAVA && has_makefile_target "lint" "$PROJECT_ROOT"; then
        make lint >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_RUBY && command -v rubocop &>/dev/null; then
        rubocop $(echo "$CHANGED_FILES" | grep '\.rb$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_PHP && command -v phpstan &>/dev/null; then
        phpstan analyse $(echo "$CHANGED_FILES" | grep '\.php$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_ELIXIR && command -v mix &>/dev/null; then
        mix credo --strict >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_DART && command -v dart &>/dev/null; then
        dart analyze $(echo "$CHANGED_FILES" | grep '\.dart$' | tr '\n' ' ') >> "$out" 2>&1 || exit_code=1
    fi

    return $exit_code
}

# --- Test (background) ---
run_test() {
    local out="$1"

    # SCOPED-FIRST: Run tests only for changed packages/files.
    # Cascade for Go: Makefile → Bazel → `go test`. Honours consumer intent
    # and avoids the false-negative timeout on Bazel-driven repos (issue #350).
    local exit_code=0
    if $HAS_GO; then
        local go_pkgs
        go_pkgs=$(echo "$CHANGED_FILES" | grep '\.go$' | xargs -I{} dirname {} | sort -u | sed 's|^|./|' | paste -sd' ')

        if has_makefile_target "test" "$PROJECT_ROOT"; then
            # Makefile-first: lets the project decide (bazel test, gotestsum, ...).
            (cd "$PROJECT_ROOT" && make test) >> "$out" 2>&1 || exit_code=1
        elif has_bazel_workspace "$PROJECT_ROOT"; then
            # Bazel-direct (when no Makefile but the project ships Bazel).
            local bazel_cmd
            if bazel_cmd="$(bazel_bin)"; then
                # Build labels via bazel_label_for_dir so root-level Go files
                # produce //... (canonical) instead of //./... (broken). The
                # helper also normalises symlinks and falls back gracefully.
                local bazel_labels=""
                local _dir _label
                while IFS= read -r _dir; do
                    [ -z "$_dir" ] && continue
                    _label="$(bazel_label_for_dir "$PROJECT_ROOT/$_dir" "$PROJECT_ROOT")"
                    bazel_labels+="${_label}"$'\n'
                done < <(echo "$CHANGED_FILES" | grep '\.go$' | xargs -I{} dirname {} | sort -u)
                bazel_labels=$(printf '%s' "$bazel_labels" | sort -u | paste -sd' ')
                [ -z "$bazel_labels" ] && bazel_labels="//..."
                # shellcheck disable=SC2086
                (cd "$PROJECT_ROOT" && "$bazel_cmd" test --test_output=errors $bazel_labels) >> "$out" 2>&1 || exit_code=1
            elif command -v go &>/dev/null; then
                # Bazel workspace detected but neither bazelisk nor bazel is
                # installed — fall through to `go test` rather than silently
                # passing the gate (false-positive). Same flags as the bare
                # `go test` last-resort below.
                # shellcheck disable=SC2086
                (cd "$PROJECT_ROOT" && go test -race -timeout 180s $go_pkgs) >> "$out" 2>&1 || exit_code=1
            fi
        elif command -v go &>/dev/null; then
            # Last resort: drop -count=1 so Go's own test cache stays warm;
            # -timeout 180s keeps the watchdog without pegging at 60s.
            # shellcheck disable=SC2086
            # Intentional word splitting on go_pkgs (space-separated list of dirs).
            (cd "$PROJECT_ROOT" && go test -race -timeout 180s $go_pkgs) >> "$out" 2>&1 || exit_code=1
        fi
    fi
    if $HAS_RUST && command -v cargo &>/dev/null; then
        cargo test >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_NODE && [ -f "$PROJECT_ROOT/package.json" ]; then
        if command -v npx &>/dev/null; then
            npx vitest run --reporter=verbose >> "$out" 2>&1 || \
            npx jest --passWithNoTests >> "$out" 2>&1 || exit_code=1
        fi
    fi
    if $HAS_PYTHON && command -v pytest &>/dev/null; then
        pytest --tb=short -q >> "$out" 2>&1 || exit_code=1
    fi
    if $HAS_SHELL; then
        if [ -x "tests/hooks/run-all.sh" ]; then
            bash tests/hooks/run-all.sh >> "$out" 2>&1 || exit_code=1
        elif command -v bats &>/dev/null && ls tests/**/*.bats &>/dev/null 2>&1; then
            bats tests/**/*.bats >> "$out" 2>&1 || exit_code=1
        fi
    fi
    if $HAS_ELIXIR && command -v mix &>/dev/null; then
        mix test >> "$out" 2>&1 || exit_code=1
    fi

    return $exit_code
}

# Override seam: source ~/.claude/scripts/pre-commit-quality.local.sh if present.
# Loaded after run_lint/run_test definitions so consumer overrides win.
load_local_override "${BASH_SOURCE[0]}"

# === Run lint + test in PARALLEL ===
run_lint "$LINT_OUT" &
LINT_PID=$!

run_test "$TEST_OUT" &
TEST_PID=$!

# Wait for both
wait "$LINT_PID" 2>/dev/null
LINT_EXIT=$?

wait "$TEST_PID" 2>/dev/null
TEST_EXIT=$?

# === Report ===
ISSUES=""
if [ "$LINT_EXIT" -ne 0 ]; then
    LINT_CONTENT=$(tail -50 "$LINT_OUT" 2>/dev/null)
    [ ${#LINT_CONTENT} -gt 2000 ] && LINT_CONTENT="${LINT_CONTENT:0:2000}...(truncated)"
    ISSUES+="LINT FAILED (exit $LINT_EXIT):"$'\n'"$LINT_CONTENT"$'\n\n'
fi

if [ "$TEST_EXIT" -ne 0 ]; then
    TEST_CONTENT=$(tail -50 "$TEST_OUT" 2>/dev/null)
    [ ${#TEST_CONTENT} -gt 2000 ] && TEST_CONTENT="${TEST_CONTENT:0:2000}...(truncated)"
    ISSUES+="TEST FAILED (exit $TEST_EXIT):"$'\n'"$TEST_CONTENT"$'\n\n'
fi

if [ -n "$ISSUES" ]; then
    echo "--- Pre-commit Quality Gate FAILED ---" >&2
    echo "$ISSUES" >&2
    echo "--- End Quality Gate ---" >&2
    exit 1
else
    echo "--- Pre-commit Quality Gate PASSED (lint + test) ---" >&2
    exit 0
fi
