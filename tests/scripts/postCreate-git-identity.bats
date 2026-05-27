#!/usr/bin/env bats
# Tests for postCreate.sh::step_git_identity (#365 — propagate .env into git config).

setup() {
    load '../helpers/setup'
    common_setup

    POSTCREATE="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/lifecycle/postCreate.sh"
    UTILS="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/shared/utils.sh"

    export HOME="$TEST_TMPDIR/home"
    mkdir -p "$HOME"
    export GIT_CONFIG_GLOBAL="$HOME/.gitconfig"
    : > "$GIT_CONFIG_GLOBAL"

    # shellcheck source=/dev/null
    source "$UTILS"
    source_function_from "$POSTCREATE" step_git_identity
}

teardown() {
    common_teardown
}

@test "#365: no .env → idempotent skip" {
    export ENV_FILE="$TEST_TMPDIR/no-such-file"
    run step_git_identity
    [ "$status" -eq 0 ]
    [[ "$output" == *"skipping git identity"* ]]
}

@test "#365: .env declares both → both git config keys updated" {
    set_env_file 'GIT_USER="kodflow"' 'GIT_EMAIL="+kodflow@users.noreply.github.com"'
    git config --global user.email "stale@example.com"
    run step_git_identity
    [ "$status" -eq 0 ]
    [ "$(git config --global user.name)" = "kodflow" ]
    [ "$(git config --global user.email)" = "+kodflow@users.noreply.github.com" ]
}

@test "#365: .env declares only GIT_EMAIL → only email updated" {
    set_env_file 'GIT_EMAIL="only-email@example.com"'
    git config --global user.name "preserved-name"
    git config --global user.email "stale@example.com"
    run step_git_identity
    [ "$status" -eq 0 ]
    [ "$(git config --global user.name)" = "preserved-name" ]
    [ "$(git config --global user.email)" = "only-email@example.com" ]
}

@test "#365: already-aligned values → idempotent (no SUCCESS log)" {
    set_env_file 'GIT_USER="kodflow"' 'GIT_EMAIL="+kodflow@users.noreply.github.com"'
    git config --global user.name "kodflow"
    git config --global user.email "+kodflow@users.noreply.github.com"
    run step_git_identity
    [ "$status" -eq 0 ]
    [[ "$output" != *"set from"* ]]
}

@test "#365: supports 'export GIT_USER=foo' shape" {
    set_env_file 'export GIT_USER="exported"' 'export GIT_EMAIL="exp@x.y"'
    run step_git_identity
    [ "$status" -eq 0 ]
    [ "$(git config --global user.name)" = "exported" ]
    [ "$(git config --global user.email)" = "exp@x.y" ]
}

@test "#365: empty values in .env → idempotent skip" {
    set_env_file 'GIT_USER=""' 'GIT_EMAIL=""'
    git config --global user.name "preserved-name"
    run step_git_identity
    [ "$status" -eq 0 ]
    [ "$(git config --global user.name)" = "preserved-name" ]
}
