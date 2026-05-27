#!/usr/bin/env bats
# Tests for pre-commit-quality.sh — golangci-lint config gating (issue #342)

setup() {
    load '../helpers/setup'
    common_setup

    SCRIPT="${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/scripts/pre-commit-quality.sh"

    REPO="$TEST_TMPDIR/repo"
    mkdir -p "$REPO"
    git -C "$REPO" init -q -b main
    git -C "$REPO" config user.email "test@test.com"
    git -C "$REPO" config user.name "Test"
    # Valid go.mod is required when `go` is on PATH (ubuntu-latest CI):
    # the script's parallel `run_test` calls `go test ./` which fails on an
    # empty go.mod with "unexpected EOF, expecting module statement".
    cat > "$REPO/go.mod" <<EOF
module example.com/test

go 1.21
EOF
    git -C "$REPO" add . && git -C "$REPO" commit -q -m "init"
    git -C "$REPO" checkout -q -b feature

    GOLANGCI_BIN_DIR="$TEST_TMPDIR/bin"
    GOLANGCI_SENTINEL="$TEST_TMPDIR/golangci-called"
    mkdir -p "$GOLANGCI_BIN_DIR"
    cat > "$GOLANGCI_BIN_DIR/golangci-lint" <<EOF
#!/bin/bash
echo "\$@" > "$GOLANGCI_SENTINEL"
exit 0
EOF
    chmod +x "$GOLANGCI_BIN_DIR/golangci-lint"
    export PATH="$GOLANGCI_BIN_DIR:$PATH"

    cat > "$REPO/main.go" <<'EOF'
package main
func main() {}
EOF
    git -C "$REPO" add main.go
}

teardown() {
    common_teardown
}

@test "pre-commit-quality skips golangci-lint when no .golangci config" {
    rm -f "$GOLANGCI_SENTINEL"
    # Merge stderr into stdout so the visible-skip-message contract can be
    # asserted: the script writes the skip reason to stderr (>&2) so users
    # see it even when the lint output buffer (success path) is silent.
    run bash -c "cd '$REPO' && CLAUDE_PROJECT_DIR='$REPO' bash '$SCRIPT' main 2>&1"
    [ "$status" -eq 0 ]
    [ ! -f "$GOLANGCI_SENTINEL" ]
    [[ "$output" == *"golangci-lint skipped"* ]]
}

@test "pre-commit-quality runs golangci-lint with --config when .golangci.yml present" {
    cat > "$REPO/.golangci.yml" <<'EOF'
linters:
  enable: []
EOF
    git -C "$REPO" add .golangci.yml

    rm -f "$GOLANGCI_SENTINEL"
    run bash -c "cd '$REPO' && CLAUDE_PROJECT_DIR='$REPO' bash '$SCRIPT' main"
    [ "$status" -eq 0 ]
    [ -f "$GOLANGCI_SENTINEL" ]
    grep -q -- "--config" "$GOLANGCI_SENTINEL"
    grep -q -- ".golangci.yml" "$GOLANGCI_SENTINEL"
}

@test "pre-commit-quality accepts .golangci.yaml as alternative config name" {
    cat > "$REPO/.golangci.yaml" <<'EOF'
linters:
  enable: []
EOF
    git -C "$REPO" add .golangci.yaml

    rm -f "$GOLANGCI_SENTINEL"
    run bash -c "cd '$REPO' && CLAUDE_PROJECT_DIR='$REPO' bash '$SCRIPT' main"
    [ "$status" -eq 0 ]
    [ -f "$GOLANGCI_SENTINEL" ]
    grep -q -- ".golangci.yaml" "$GOLANGCI_SENTINEL"
}

# Sanity: the helper itself should round-trip absent/present cases.
@test "find_golangci_config returns 1 when no config" {
    load_common
    run find_golangci_config "$TEST_TMPDIR"
    [ "$status" -eq 1 ]
}

@test "find_golangci_config echoes path when .golangci.toml present" {
    load_common
    touch "$TEST_TMPDIR/.golangci.toml"
    run find_golangci_config "$TEST_TMPDIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$TEST_TMPDIR/.golangci.toml" ]
}

# --- Issue #363: detect_languages bind-safety under set -u ---
#
# WHY env -u BASH_ENV: kcov v43 sets BASH_ENV to a tracing script that
# references variables not bound under set -u. Any `bash -c` subshell
# inherits BASH_ENV and trips immediately on the first command,
# regardless of what the test itself does. Unsetting BASH_ENV for the
# subshell isolates this test from the coverage harness so it actually
# verifies the production code (which is set -u safe) instead of the
# kcov plumbing (which is not).
@test "issue #363: detect_languages with no markers does not abort under set -u" {
    local empty_dir="$TEST_TMPDIR/empty-repo"
    mkdir -p "$empty_dir"
    run env -u BASH_ENV bash -c "set -euo pipefail
        source '${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/scripts/pre-commit-checks.sh'
        detect_languages '$empty_dir'
        echo \"count=\${#DETECTED_LANGUAGES[@]}\""
    if [ "$status" -ne 0 ]; then
        echo "STATUS=$status"
        echo "OUTPUT=$output"
    fi
    [ "$status" -eq 0 ]
    [[ "$output" == *"count=0"* ]]
}
