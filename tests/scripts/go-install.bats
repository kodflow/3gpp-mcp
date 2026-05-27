#!/usr/bin/env bats
# Static validation tests for the Go feature install.sh — issue #324.
# Running the actual install requires root + network, so we assert the
# structural guarantees that prevent silent regressions:
#   - strict mode is on
#   - ERR trap surfaces the failing command
#   - every critical tool is declared
#   - the MCP fragment is written before optional toolchains
#   - parallel jobs are tracked per-PID (no bare `wait`)

setup() {
    load '../helpers/setup'
    common_setup
    INSTALL_SH="${BATS_TEST_DIRNAME}/../../.devcontainer/features/languages/go/install.sh"
    POST_START="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/lifecycle/postStart.sh"
}

teardown() {
    common_teardown
}

# --- install.sh: strict mode & error trap ---

@test "install.sh enables set -Eeuo pipefail" {
    grep -qE '^set -Eeuo pipefail$' "$INSTALL_SH"
}

@test "install.sh defines an ERR trap that exits with the failing code" {
    grep -q "trap 'on_error" "$INSTALL_SH"
    grep -q "on_error()" "$INSTALL_SH"
}

@test "install.sh syntax is valid" {
    bash -n "$INSTALL_SH"
}

# --- install.sh: critical tools enforcement ---

@test "install.sh declares CRITICAL_TOOLS with ktn-linter" {
    grep -q 'CRITICAL_TOOLS=' "$INSTALL_SH"
    grep -E 'CRITICAL_TOOLS=\([^)]*ktn-linter' "$INSTALL_SH"
}

@test "install.sh declares CRITICAL_TOOLS with golangci-lint, gofumpt, goimports" {
    grep -E 'CRITICAL_TOOLS=\([^)]*golangci-lint' "$INSTALL_SH"
    grep -E 'CRITICAL_TOOLS=\([^)]*gofumpt' "$INSTALL_SH"
    grep -E 'CRITICAL_TOOLS=\([^)]*goimports' "$INSTALL_SH"
}

@test "install.sh exits with error when critical tools are missing" {
    grep -q 'Feature installation INCOMPLETE' "$INSTALL_SH"
    # The exit 1 lives inside the `if ((${#missing_critical[@]} > 0))` block.
    grep -A12 'missing_critical\[@\]} > 0' "$INSTALL_SH" | grep -q '^    exit 1$'
}

# --- install.sh: parallel job tracking ---

@test "install.sh tracks individual PIDs for parallel tool installs" {
    grep -q 'declare -A TOOL_PIDS' "$INSTALL_SH"
    grep -q 'TOOL_PIDS\[.*\]=\$!' "$INSTALL_SH"
}

@test "install.sh collects exit codes per-PID via wait <pid>" {
    grep -qF 'wait "${TOOL_PIDS[$tool]}"' "$INSTALL_SH"
}

@test "install.sh does NOT rely on bare 'wait' to collect failures" {
    # Bare `wait` (no argument) always returns 0 — regression from #324.
    # Only non-critical optional subshells (Wails/TinyGo) may use `wait <pid>`;
    # the main tool loop must use keyed waits.
    ! grep -E '^wait$' "$INSTALL_SH"
}

# --- install.sh: MCP fragment ordering ---

@test "install.sh writes MCP fragment before optional toolchains (Wails/TinyGo)" {
    local frag_line wails_line
    frag_line=$(grep -n 'install_mcp_fragment "go"' "$INSTALL_SH" | head -1 | cut -d: -f1)
    wails_line=$(grep -n 'Installing Wails v2' "$INSTALL_SH" | head -1 | cut -d: -f1)
    [ -n "$frag_line" ]
    [ -n "$wails_line" ]
    [ "$frag_line" -lt "$wails_line" ]
}

@test "install.sh MCP fragment references ktn-linter binary" {
    grep -A6 'install_mcp_fragment "go"' "$INSTALL_SH" | grep -q '"ktn-linter"'
    grep -A6 'install_mcp_fragment "go"' "$INSTALL_SH" | grep -q '"requires_binary"'
}

# --- install.sh: structured step markers ---

@test "install.sh emits structured [INSTALL-GO] step markers" {
    grep -q '\[INSTALL-GO\] step=' "$INSTALL_SH"
}

# --- install.sh: golangci-lint v1 rejection (issue #329) ---

@test "install.sh rejects a v1 golangci-lint binary post-install (issue #329)" {
    # The smoke test parses the installed binary's major version and exits 1
    # if it is not 2. Guards against stale 'latest' tags resolved at build.
    grep -q 'GOLANGCI_INSTALLED_MAJOR=' "$INSTALL_SH"
    grep -qF 'v2.x required (issue #329)' "$INSTALL_SH"
}

@test "install.sh smoke test runs after the critical-tools check" {
    # Order matters: a missing binary should fail with the existing
    # "Feature installation INCOMPLETE" path, not silently skip the smoke test.
    local critical_line smoke_line
    critical_line=$(grep -n 'Feature installation INCOMPLETE' "$INSTALL_SH" | head -1 | cut -d: -f1)
    smoke_line=$(grep -n 'GOLANGCI_INSTALLED_MAJOR=' "$INSTALL_SH" | head -1 | cut -d: -f1)
    [ -n "$critical_line" ]
    [ -n "$smoke_line" ]
    [ "$critical_line" -lt "$smoke_line" ]
}

# --- postStart.sh: observability ---

@test "postStart.sh has syntax valid" {
    bash -n "$POST_START"
}

@test "postStart.sh warns when a feature trigger binary exists but fragment is missing" {
    grep -q 'expected_fragments' "$POST_START"
    grep -q 'may have failed' "$POST_START"
}

@test "postStart.sh logs a merge summary listing active MCP servers" {
    grep -q 'MCP merge summary' "$POST_START"
}

@test "postStart.sh warns (not info) when a feature fragment requires a missing binary" {
    # Regression guard: the former behavior was `log_info` which gets lost in
    # the noise. Feature-level skips must be WARN to be spotted.
    grep -A3 'fragment"\? == /etc/mcp/features/' "$POST_START" | grep -q 'log_warning'
}
