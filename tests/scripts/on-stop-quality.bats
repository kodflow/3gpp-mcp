#!/usr/bin/env bats
# Tests for on-stop-quality.sh - project type detection logic

SCRIPT=""

setup() {
    load '../helpers/setup'
    load '../helpers/mocks'
    common_setup
    SCRIPT="$SCRIPTS_DIR/on-stop-quality.sh"

    # Source common.sh so detect_project_type is testable
    source "$SCRIPTS_DIR/common.sh"

    # Source the detect_project_type function from on-stop-quality.sh
    # We extract and eval it to test in isolation
    eval "$(sed -n '/^detect_project_type/,/^}/p' "$SCRIPT")"
}

teardown() {
    common_teardown
}

# --- Project type detection ---

@test "detects Go project (go.mod present)" {
    mkdir -p "$TEST_TMPDIR/project"
    touch "$TEST_TMPDIR/project/go.mod"
    touch "$TEST_TMPDIR/project/main.go"

    result=$(detect_project_type "$TEST_TMPDIR/project")
    [ "$result" = "code" ]
}

@test "detects Node.js project (package.json present)" {
    mkdir -p "$TEST_TMPDIR/project/src"
    touch "$TEST_TMPDIR/project/package.json"
    touch "$TEST_TMPDIR/project/src/index.js"

    result=$(detect_project_type "$TEST_TMPDIR/project")
    [ "$result" = "code" ]
}

@test "detects IaC project (main.tf present) returns iac" {
    mkdir -p "$TEST_TMPDIR/project"
    touch "$TEST_TMPDIR/project/main.tf"

    result=$(detect_project_type "$TEST_TMPDIR/project")
    [ "$result" = "iac" ]
}

@test "returns none for empty directory" {
    mkdir -p "$TEST_TMPDIR/empty"

    result=$(detect_project_type "$TEST_TMPDIR/empty")
    [ "$result" = "none" ]
}

@test "code takes priority over iac when both present" {
    mkdir -p "$TEST_TMPDIR/project"
    touch "$TEST_TMPDIR/project/main.tf"
    touch "$TEST_TMPDIR/project/main.go"
    touch "$TEST_TMPDIR/project/go.mod"

    result=$(detect_project_type "$TEST_TMPDIR/project")
    [ "$result" = "code" ]
}

# --- Timeout configuration ---

@test "uses 300s timeout for test target, 90s for lint" {
    # Verify the timeout values are set correctly in the script
    run grep -E 'TARGET_TIMEOUT=300' "$SCRIPT"
    [ "$status" -eq 0 ]

    run grep -E 'TARGET_TIMEOUT=90' "$SCRIPT"
    [ "$status" -eq 0 ]

    # Verify test gets 300s specifically
    run grep -B1 'TARGET_TIMEOUT=300' "$SCRIPT"
    echo "$output" | grep -q 'target.*=.*test\|"test"'
}
