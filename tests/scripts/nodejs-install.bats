#!/usr/bin/env bats
# Static regression guards for the Node.js feature install.sh — issue #354.
# Mirrors tests/scripts/go-install.bats. Network and apt are NOT exercised
# (impossible without root + connectivity). We assert the structural
# invariants that prevent the rate-limit fatal regression from creeping back.

setup() {
    load '../helpers/setup'
    common_setup
    INSTALL_SH="${BATS_TEST_DIRNAME}/../../.devcontainer/features/languages/nodejs/install.sh"
    META="${BATS_TEST_DIRNAME}/../../.devcontainer/features/languages/nodejs/devcontainer-feature.json"
}

teardown() {
    common_teardown
}

# --- syntax & shell hygiene ---

@test "install.sh syntax is valid" {
    bash -n "$INSTALL_SH"
}

@test "install.sh enables set -e" {
    grep -qE '^set -e' "$INSTALL_SH"
}

# --- 3-tier NVM resolution invariants (issue #354) ---

@test "tier 1: git ls-remote against nvm-sh/nvm exists" {
    grep -q 'git ls-remote --tags --refs --sort=-v:refname' "$INSTALL_SH"
    grep -q 'github.com/nvm-sh/nvm.git' "$INSTALL_SH"
}

@test "tier 2: shared helper get_github_latest_version_or_empty is used for nvm-sh/nvm" {
    grep -q 'get_github_latest_version_or_empty "nvm-sh/nvm"' "$INSTALL_SH"
}

@test "tier 3: pinned NVM_FALLBACK_VERSION constant is defined with a v-prefixed tag" {
    grep -qE '^NVM_FALLBACK_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"' "$INSTALL_SH"
}

@test "no fatal exit when latest NVM version cannot be resolved" {
    # Lock-in: the old fatal branch ('Failed to resolve latest NVM version' + exit 1)
    # must never come back — tier 3 guarantees a usable fallback.
    ! grep -q 'Failed to resolve latest NVM version after retries' "$INSTALL_SH"
    # awk: END unconditionally overrode previous exit() — set a flag instead so
    # exit code reflects whether the forbidden pattern was actually found.
    ! awk '/Failed to resolve latest NVM/{flag=1} flag && /exit 1/{found=1} END{exit(found?0:1)}' \
        "$INSTALL_SH"
}

@test "NVM_LATEST is normalized to a v-prefixed tag before use" {
    grep -qE '\[\[ "\$NVM_LATEST" != v\* \]\] && NVM_LATEST="v\$\{NVM_LATEST\}"' "$INSTALL_SH"
}

@test "duplicated _nvm_auth array is removed (shared helper handles GITHUB_TOKEN)" {
    ! grep -q '_nvm_auth=' "$INSTALL_SH"
}

# --- devcontainer-feature.json: version gate ---

@test "feature version is bumped to >= 1.0.3 (CI version-gate.yml)" {
    local v
    v=$(grep -oE '"version": *"[0-9]+\.[0-9]+\.[0-9]+"' "$META" | head -1 \
        | sed -E 's/.*"([0-9]+\.[0-9]+\.[0-9]+)"/\1/')
    [ -n "$v" ]
    # Sort-merged comparison: $v must be >= 1.0.3
    local lowest
    lowest=$(printf '%s\n%s\n' "1.0.3" "$v" | sort -V | head -n1)
    [ "$lowest" = "1.0.3" ]
}
