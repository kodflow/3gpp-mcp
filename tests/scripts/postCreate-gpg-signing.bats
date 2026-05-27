#!/usr/bin/env bats
# Tests for postCreate.sh::step_gpg_signing (#366 — three-tier resolution).

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
    source_function_from "$POSTCREATE" step_gpg_signing
}

teardown() {
    common_teardown
}

@test "#366 mode 1: GPG_SIGNINGKEY in .env with secret → commit.gpgsign=true" {
    make_test_gpg_keyring with-secret
    local key_id
    key_id=$(gpg_key_id_for "test-with-secret@example.com")
    [ -n "$key_id" ]
    set_env_file "GPG_SIGNINGKEY=$key_id"
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [ "$(git config --global user.signingkey)" = "$key_id" ]
    [ "$(git config --global commit.gpgsign)" = "true" ]
    [[ "$output" == *"declared via GPG_SIGNINGKEY"* ]]
}

@test "#366 mode 1: pub-only key → user.signingkey set, commit.gpgsign unset, log_info" {
    make_test_gpg_keyring pub-only
    local key_id
    key_id=$(gpg_key_id_for "test-pub-only@example.com")
    [ -n "$key_id" ]
    set_env_file "GPG_SIGNINGKEY=$key_id"
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [ "$(git config --global user.signingkey)" = "$key_id" ]
    # commit.gpgsign must NOT be set
    run git config --global commit.gpgsign
    [ "$status" -ne 0 ]
}

@test "#366 mode 1: pub-only key → log output mentions 'pub only'" {
    make_test_gpg_keyring pub-only
    local key_id
    key_id=$(gpg_key_id_for "test-pub-only@example.com")
    [ -n "$key_id" ]
    set_env_file "GPG_SIGNINGKEY=$key_id"
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [[ "$output" == *"pub only"* ]]
}

@test "#366 mode 2: pre-configured user.signingkey + secret → commit.gpgsign=true" {
    make_test_gpg_keyring with-secret
    local key_id
    key_id=$(gpg_key_id_for "test-with-secret@example.com")
    git config --global user.signingkey "$key_id"
    set_env_file 'GIT_USER="x"'   # no GPG_SIGNINGKEY in env
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [ "$(git config --global commit.gpgsign)" = "true" ]
    [[ "$output" == *"pre-configured user.signingkey"* ]]
}

@test "#366 mode 3: legacy email-match still works" {
    make_test_gpg_keyring with-secret
    set_env_file 'GIT_EMAIL="test-with-secret@example.com"'
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [ "$(git config --global commit.gpgsign)" = "true" ]
    [[ "$output" == *"auto-matched"* ]]
}

@test "#366 mode 3: no match → log_info NOT log_warning, actionable hint present" {
    make_test_gpg_keyring with-secret
    set_env_file 'GIT_EMAIL="no-match@example.com"'
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [[ "$output" != *"[WARNING]"* ]]
    [[ "$output" == *"declare GPG_SIGNINGKEY"* ]]
}

@test "#366: no keys in keyring → graceful skip" {
    # Empty GNUPGHOME, no fixtures imported.
    export GNUPGHOME="$TEST_TMPDIR/.gnupg-empty"
    mkdir -p "$GNUPGHOME"
    chmod 700 "$GNUPGHOME"
    set_env_file 'GIT_EMAIL="test@example.com"'
    run step_gpg_signing
    [ "$status" -eq 0 ]
    [[ "$output" == *"No GPG keys available"* ]]
}
