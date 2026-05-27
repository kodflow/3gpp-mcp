#!/usr/bin/env bats
# Tests for install.sh rtk install path.
# Layer 2 of plan 2026-05-06-rtk-mandatory-install-and-claude-memory.
#
# RTK install is MANDATORY in this Linux devcontainer template. Any failure
# of the install path must propagate non-zero to the install.sh entrypoint
# so the build / setup is rejected immediately.

setup() {
    load '../helpers/setup'
    common_setup

    SCRIPT="${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    export HOME_DIR="$TEST_TMPDIR/sandbox-home"
    mkdir -p "$HOME_DIR/.local/bin"
}

teardown() {
    common_teardown
}

# Helper: run only the rtk-install slice of download_tools() with controlled env.
# We extract the function via sed and write a runner script to a temp file —
# this avoids the complex quoting / line-numbering issues that can surface
# when bats wraps `bash -c "..."` invocations on different runners.
run_install_rtk() {
    local arch="$1" extra="${2:-}"
    local runner="$TEST_TMPDIR/rtk-runner-$$.sh"
    {
        echo "#!/bin/bash"
        echo "export HOME_DIR='$HOME_DIR'"
        echo "export ARCH='$arch'"
        echo "export OS=linux"
        # /usr/local/bin stripped → 'command -v rtk' fails → install path runs.
        echo "export PATH=/usr/bin:/bin"
        if [ -n "$extra" ]; then
            echo "export $extra"
        fi
        # Stub log helpers used by install.sh.
        echo 'ok()   { echo "$@"; }'
        echo 'info() { echo "$@"; }'
        echo 'warn() { echo "$@"; }'
        echo 'log()  { echo "$@"; }'
        # Slice the download_tools function and call it.
        sed -n '651,747p' "$SCRIPT"
        echo "tool_count=0"
        echo "download_tools"
    } > "$runner"
    chmod +x "$runner"
    # Merge stderr into stdout: install.sh writes user-facing failures to
    # >&2 by design, but bats `run` only captures stdout.
    bash "$runner" 2>&1
    local rc=$?
    rm -f "$runner"
    return $rc
}

# === amd64 + arm64: simulated download failure must propagate exit-non-zero ===

@test "amd64: simulated curl failure exits non-zero (build hard-fail)" {
    if ! command -v curl >/dev/null 2>&1; then
        skip "curl not available"
    fi
    run run_install_rtk amd64 "RTK_INSTALL_TEST_FAIL=1"
    [ "$status" -ne 0 ]
    if [[ "$output" != *"download/extract failed"* ]] && \
       [[ "$output" != *"required"* ]]; then
        echo "DEBUG output: $output" >&2
        false
    fi
}

@test "arm64: simulated curl failure exits non-zero (build hard-fail)" {
    if ! command -v curl >/dev/null 2>&1; then
        skip "curl not available"
    fi
    run run_install_rtk arm64 "RTK_INSTALL_TEST_FAIL=1"
    [ "$status" -ne 0 ]
    if [[ "$output" != *"download/extract failed"* ]] && \
       [[ "$output" != *"required"* ]]; then
        echo "DEBUG output: $output" >&2
        false
    fi
}

# === Unsupported arch (devcontainer expects amd64/arm64): hard-fail too ===

@test "unsupported arch (riscv64): hard-fail with explicit message" {
    run run_install_rtk riscv64 ""
    [ "$status" -ne 0 ]
    if [[ "$output" != *"unsupported architecture"* ]] && \
       [[ "$output" != *"unsupported"* ]]; then
        echo "DEBUG output: $output" >&2
        false
    fi
}

# === Code-path invariants (lock the contract against future regressions) ===

@test "code path: download failure calls return 1 with 'required' message" {
    grep -E 'rtk: download/extract failed.*required' "${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
}

@test "code path: RTK_INSTALL_TEST_FAIL hook is wired" {
    grep -F 'RTK_INSTALL_TEST_FAIL' "${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
}

@test "code path: no '(optional)' fallbacks remain in the rtk install block" {
    # The previous fail-open behavior is gone. Any '(optional)' marker in the
    # rtk slice would be a regression toward the old silent-degradation path.
    # Narrow the slice to JUST the rtk-install block (656..715); status-line
    # at 716+ is intentionally still advisory and lives outside this contract.
    run sed -n '656,715p' "${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    [ "$status" -eq 0 ]
    [[ "$output" != *"(optional"* ]]
}

# === Pin invariants (issue #348 — no more dynamic 'latest' lottery) ===

@test "pin: install.sh declares RTK_PINNED_VERSION matching /^v[0-9]+\\.[0-9]+\\.[0-9]+$/" {
    local install_sh="${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    run grep -E '^[[:space:]]+local RTK_PINNED_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"' "$install_sh"
    [ "$status" -eq 0 ]
}

@test "pin: install.sh no longer resolves rtk via /releases/latest API" {
    # The dynamic 'latest' resolution is the bug class issue #348 ships against:
    # any upstream release lands in the next image rebuild without smoke-test.
    # If this assertion ever flips, someone re-introduced the lottery.
    local install_sh="${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    run grep -F 'rtk-ai/rtk/releases/latest' "$install_sh"
    [ "$status" -ne 0 ]
}

@test "arch: arm64 uses aarch64-unknown-linux-gnu (rtk ships no musl variant for arm64)" {
    # Issue #348: install.sh used to request aarch64-unknown-linux-musl,
    # which rtk-ai/rtk has never published as an asset (verified for
    # v0.38.0 + v0.39.0). arm64 standalone host install was therefore
    # broken silently before PR #346 made install hard-fail. The Dockerfile
    # already used the correct gnu variant; now install.sh matches.
    local install_sh="${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    run grep -E 'arm64\)[[:space:]]*rtk_rust_arch="aarch64-unknown-linux-gnu"' "$install_sh"
    [ "$status" -eq 0 ]
    # And no leftover musl reference for arm64.
    run grep -E 'arm64\).*aarch64-unknown-linux-musl' "$install_sh"
    [ "$status" -ne 0 ]
}

@test "pin: drift guard — install.sh and Dockerfile pin the SAME rtk version" {
    # Single source of truth would be cleaner, but the two files have
    # different lifecycles (host install vs image build) and must each
    # carry the version. This test catches the moment a future bump
    # touches one file but not the other.
    local install_sh="${BATS_TEST_DIRNAME}/../../.devcontainer/install.sh"
    local dockerfile="${BATS_TEST_DIRNAME}/../../.devcontainer/images/Dockerfile"
    [ -f "$install_sh" ]
    [ -f "$dockerfile" ]

    local install_pin
    install_pin=$(grep -oE 'RTK_PINNED_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"' "$install_sh" \
        | head -1 | cut -d'"' -f2)
    local dockerfile_pin
    dockerfile_pin=$(grep -oE 'ARG RTK_VERSION=v[0-9]+\.[0-9]+\.[0-9]+' "$dockerfile" \
        | head -1 | cut -d= -f2)

    [ -n "$install_pin" ]
    [ -n "$dockerfile_pin" ]
    [ "$install_pin" = "$dockerfile_pin" ]
}
