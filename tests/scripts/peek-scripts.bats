#!/usr/bin/env bats
# ============================================================================
# Tests for peek scripts (project-peek, dev-peek, infra-peek, ops-peek)
# Pattern: fail-open (exit 0), valid JSON output, correct keys
# ============================================================================

REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
SCRIPTS_DIR="${SCRIPTS_DIR:-$REPO_ROOT/.devcontainer/images/.claude/scripts}"
PROJECT_DIR="$REPO_ROOT"

# --- Helpers ---

json_valid() {
    echo "$1" | jq empty 2>/dev/null
}

json_has_key() {
    echo "$1" | jq -e ".$2" >/dev/null 2>&1
}

json_type() {
    echo "$1" | jq -r ".$2 | type" 2>/dev/null
}

# ============================================================================
# project-peek.sh
# ============================================================================

@test "project-peek: exits 0" {
    run bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR
    [ "$status" -eq 0 ]
}

@test "project-peek: outputs valid JSON" {
    run bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR
    json_valid "$output"
}

@test "project-peek: has all top-level keys" {
    output=$(bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR)
    json_has_key "$output" "claude_hierarchy"
    json_has_key "$output" "project"
    json_has_key "$output" "git"
    json_has_key "$output" "features"
    json_has_key "$output" "template"
    json_has_key "$output" "local_docs"
    json_has_key "$output" "file_counts"
}

@test "project-peek: claude_hierarchy is array" {
    output=$(bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR)
    [ "$(json_type "$output" "claude_hierarchy")" = "array" ]
}

@test "project-peek: detects devcontainer-template type" {
    output=$(bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR)
    type=$(echo "$output" | jq -r '.project.type')
    [ "$type" = "devcontainer-template" ]
}

@test "project-peek: detects git org/repo" {
    output=$(bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR)
    org=$(echo "$output" | jq -r '.git.org')
    repo=$(echo "$output" | jq -r '.git.repo')
    [ -n "$org" ]
    [ -n "$repo" ]
}

@test "project-peek: file_counts are numbers" {
    output=$(bash "$SCRIPTS_DIR/project-peek.sh" $PROJECT_DIR)
    [ "$(json_type "$output" "file_counts.shell")" = "number" ]
    [ "$(json_type "$output" "file_counts.markdown")" = "number" ]
}

@test "project-peek: exits 0 with nonexistent dir" {
    run bash "$SCRIPTS_DIR/project-peek.sh" /nonexistent
    [ "$status" -eq 0 ]
}

# ============================================================================
# dev-peek.sh
# ============================================================================

@test "dev-peek: exits 0" {
    run bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR
    [ "$status" -eq 0 ]
}

@test "dev-peek: outputs valid JSON" {
    run bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR
    json_valid "$output"
}

@test "dev-peek: has all top-level keys" {
    output=$(bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR)
    json_has_key "$output" "changed_files"
    json_has_key "$output" "languages"
    json_has_key "$output" "linters"
    json_has_key "$output" "makefile"
    json_has_key "$output" "test_frameworks"
    json_has_key "$output" "playwright"
    json_has_key "$output" "features"
}

@test "dev-peek: changed_files.vs_base is array" {
    output=$(bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR)
    [ "$(json_type "$output" "changed_files.vs_base")" = "array" ]
}

@test "dev-peek: languages are booleans" {
    output=$(bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR)
    [ "$(json_type "$output" "languages.go")" = "boolean" ]
    [ "$(json_type "$output" "languages.shell")" = "boolean" ]
}

@test "dev-peek: linters are booleans" {
    output=$(bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR)
    [ "$(json_type "$output" "linters.shellcheck")" = "boolean" ]
}

@test "dev-peek: makefile.test reflects Makefile" {
    output=$(bash "$SCRIPTS_DIR/dev-peek.sh" main $PROJECT_DIR)
    has_test=$(echo "$output" | jq -r '.makefile.test')
    if grep -qw "test:" $PROJECT_DIR/Makefile 2>/dev/null; then
        [ "$has_test" = "true" ]
    fi
}

@test "dev-peek: exits 0 with nonexistent base branch" {
    run bash "$SCRIPTS_DIR/dev-peek.sh" nonexistent-branch $PROJECT_DIR
    [ "$status" -eq 0 ]
}

# ============================================================================
# infra-peek.sh
# ============================================================================

@test "infra-peek: exits 0" {
    run bash "$SCRIPTS_DIR/infra-peek.sh" $PROJECT_DIR
    [ "$status" -eq 0 ]
}

@test "infra-peek: outputs valid JSON" {
    run bash "$SCRIPTS_DIR/infra-peek.sh" $PROJECT_DIR
    json_valid "$output"
}

@test "infra-peek: has all top-level keys" {
    output=$(bash "$SCRIPTS_DIR/infra-peek.sh" $PROJECT_DIR)
    json_has_key "$output" "tool"
    json_has_key "$output" "workspace"
    json_has_key "$output" "state"
    json_has_key "$output" "variables"
    json_has_key "$output" "modules"
    json_has_key "$output" "backend"
    json_has_key "$output" "secrets_1password"
}

@test "infra-peek: tool detection works" {
    output=$(bash "$SCRIPTS_DIR/infra-peek.sh" $PROJECT_DIR)
    tool=$(echo "$output" | jq -r '.tool.name')
    [ "$tool" != "" ]
}

@test "infra-peek: workspace.list is array" {
    output=$(bash "$SCRIPTS_DIR/infra-peek.sh" $PROJECT_DIR)
    [ "$(json_type "$output" "workspace.list")" = "array" ]
}

@test "infra-peek: exits 0 with nonexistent dir" {
    run bash "$SCRIPTS_DIR/infra-peek.sh" /nonexistent
    [ "$status" -eq 0 ]
}

# ============================================================================
# ops-peek.sh
# ============================================================================

@test "ops-peek: exits 0" {
    run bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR
    [ "$status" -eq 0 ]
}

@test "ops-peek: outputs valid JSON" {
    run bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR
    json_valid "$output"
}

@test "ops-peek: has all top-level keys" {
    output=$(bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR)
    json_has_key "$output" "onepassword"
    json_has_key "$output" "vpn"
}

@test "ops-peek: vpn.clients are booleans" {
    output=$(bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR)
    [ "$(json_type "$output" "vpn.clients.openvpn")" = "boolean" ]
    [ "$(json_type "$output" "vpn.clients.wireguard")" = "boolean" ]
}

@test "ops-peek: vpn.state.connected is boolean" {
    output=$(bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR)
    [ "$(json_type "$output" "vpn.state.connected")" = "boolean" ]
}

@test "ops-peek: onepassword.token_set reflects env" {
    output=$(bash "$SCRIPTS_DIR/ops-peek.sh" $PROJECT_DIR)
    token_set=$(echo "$output" | jq -r '.onepassword.token_set')
    if [ -n "$OP_SERVICE_ACCOUNT_TOKEN" ]; then
        [ "$token_set" = "true" ]
    else
        [ "$token_set" = "false" ]
    fi
}

@test "ops-peek: exits 0 with nonexistent dir" {
    run bash "$SCRIPTS_DIR/ops-peek.sh" /nonexistent
    [ "$status" -eq 0 ]
}
