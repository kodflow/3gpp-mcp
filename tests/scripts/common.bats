#!/usr/bin/env bats
# Tests for common.sh shared utilities

setup() {
    load '../helpers/setup'
    load '../helpers/mocks'
    common_setup
    load_common
}

teardown() {
    common_teardown
}

# --- find_project_root ---

@test "find_project_root finds go.mod project" {
    mkdir -p "$TEST_TMPDIR/myproject/src/pkg"
    touch "$TEST_TMPDIR/myproject/go.mod"

    result=$(find_project_root "$TEST_TMPDIR/myproject/src/pkg")
    [ "$result" = "$TEST_TMPDIR/myproject" ]
}

@test "find_project_root finds package.json project" {
    mkdir -p "$TEST_TMPDIR/app/src/components"
    touch "$TEST_TMPDIR/app/package.json"

    result=$(find_project_root "$TEST_TMPDIR/app/src/components")
    [ "$result" = "$TEST_TMPDIR/app" ]
}

@test "find_project_root returns fallback when no markers" {
    mkdir -p "$TEST_TMPDIR/empty/deep/path"

    result=$(find_project_root "$TEST_TMPDIR/empty/deep/path" "$TEST_TMPDIR/fallback")
    [ "$result" = "$TEST_TMPDIR/fallback" ]
}

# --- has_makefile_target ---

@test "has_makefile_target returns 0 for existing target" {
    mock_makefile "$TEST_TMPDIR" "lint" "test"

    run has_makefile_target "lint" "$TEST_TMPDIR"
    [ "$status" -eq 0 ]
}

@test "has_makefile_target returns 1 for missing target" {
    mock_makefile "$TEST_TMPDIR" "lint" "test"

    run has_makefile_target "build" "$TEST_TMPDIR"
    [ "$status" -eq 1 ]
}

@test "has_makefile_target returns 1 when no Makefile" {
    run has_makefile_target "lint" "$TEST_TMPDIR"
    [ "$status" -eq 1 ]
}

# --- makefile_supports_file ---

@test "makefile_supports_file detects FILE variable" {
    mock_makefile_with_file "$TEST_TMPDIR" "fmt" "lint"

    run makefile_supports_file "$TEST_TMPDIR"
    [ "$status" -eq 0 ]
}

@test "makefile_supports_file returns 1 without FILE variable" {
    mock_makefile "$TEST_TMPDIR" "fmt" "lint"

    run makefile_supports_file "$TEST_TMPDIR"
    [ "$status" -eq 1 ]
}
