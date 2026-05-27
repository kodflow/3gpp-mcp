#!/usr/bin/env bats
# Static validation tests for sync-toolchains.sh — issue #329.
# The previous `go install ${tool}@latest` was broken for tools that are not
# bare module names (golangci-lint, gosec, gofumpt, gotestsum) — failures were
# masked by `|| true`, so a stale volume kept v1.64.8 forever after v2 EOL.
# These tests pin the new structural guarantees:
#   - per-tool full module-path map exists for every Go module-installed tool
#   - golangci-lint and gosec are auto-refreshed against upstream (drift-resistant)
#   - ktn-linter still uses the binary download (not a Go module)

setup() {
    load '../helpers/setup'
    common_setup
    SYNC_SH="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/lifecycle/sync-toolchains.sh"
}

teardown() {
    common_teardown
}

@test "sync-toolchains.sh syntax is valid" {
    bash -n "$SYNC_SH"
}

@test "sync_go declares full module path for golangci-lint v2" {
    grep -qE '\[golangci-lint\]="github.com/golangci/golangci-lint/v2/cmd/golangci-lint"' "$SYNC_SH"
}

@test "sync_go declares full module path for gosec v2" {
    grep -qE '\[gosec\]="github.com/securego/gosec/v2/cmd/gosec"' "$SYNC_SH"
}

@test "sync_go declares full module path for gofumpt, gotestsum, goimports" {
    grep -qE '\[gofumpt\]="mvdan.cc/gofumpt"' "$SYNC_SH"
    grep -qE '\[gotestsum\]="gotest.tools/gotestsum"' "$SYNC_SH"
    grep -qE '\[goimports\]="golang.org/x/tools/cmd/goimports"' "$SYNC_SH"
}

@test "sync_go auto-refreshes golangci-lint against upstream (no static major pin)" {
    grep -qE '\[golangci-lint\]="golangci/golangci-lint"' "$SYNC_SH"
}

@test "sync_go auto-refreshes gosec against upstream (no static major pin)" {
    grep -qE '\[gosec\]="securego/gosec"' "$SYNC_SH"
}

@test "sync_go probes upstream via api.github.com releases endpoint" {
    # Major-pinned tools use ?per_page=30 + grep filter; unpinned tools use
    # /releases/latest. Both endpoints must be present (CodeRabbit, #330).
    grep -qF 'api.github.com/repos/${repo}/releases/latest' "$SYNC_SH"
    grep -qF 'api.github.com/repos/${repo}/releases?per_page=30' "$SYNC_SH"
}

@test "sync_go pins drift check to module's major version (no infinite v3 loop)" {
    # CodeRabbit major: when GO_TOOL_MODULES has /vN, the upstream search
    # MUST be restricted to that major branch. Without this, a future v3
    # release would cause permanent reinstall of v2.x on every onCreate.
    grep -q 'module_major_pin' "$SYNC_SH"
    grep -qE "sed -n 's\|.\*/v\\\\\(\[0-9\]" "$SYNC_SH"
}

@test "sync_go returns non-zero when critical tools are missing" {
    # CodeRabbit critical + Qodo bug: failure to install a critical tool
    # must NOT update the toolchain-version marker, otherwise every future
    # onCreate skips the sync entirely (Qodo, CodeRabbit, #330).
    grep -q 'local failed=0' "$SYNC_SH"
    grep -q 'failed=1' "$SYNC_SH"
    grep -qE 'return "?\$failed"?' "$SYNC_SH"
}

@test "sync_lang skips marker write when handler returns non-zero" {
    grep -q 'sync incomplete (rc=' "$SYNC_SH"
    # The marker write must be guarded by the rc check.
    grep -B1 'toolchain-version-' "$SYNC_SH" | grep -q '"$rc" -ne 0' || \
    grep -B6 -F '"$image_ver" > "${CACHE_DIR}/.toolchain-version-${lang}"' "$SYNC_SH" | grep -q 'rc.*-ne 0'
}

@test "sync_go honors GITHUB_TOKEN for upstream probe (rate-limit mitigation)" {
    grep -qF 'Authorization: token ${GITHUB_TOKEN}' "$SYNC_SH"
}

@test "sync_go does NOT use the broken bare-name 'go install \${tool}@latest'" {
    # Regression guard: the original ${tool}@latest call silently failed for
    # every non-module-name tool. New code routes through GO_TOOL_MODULES.
    ! grep -qE 'go install "\$\{?tool\}?@latest"' "$SYNC_SH"
}

@test "sync_go installs ktn-linter from the goreleaser release tarball (with go install fallback)" {
    # The hyphenated raw-binary URL never existed as a published asset and
    # 404'd every container build (#371). Asset is goreleaser-style:
    # `ktn-linter_linux_<arch>.tar.gz` with the binary at the tarball root.
    # A `go install …/cmd/ktn-linter` fallback covers CDN blips / asset
    # rename drift so a single 404 cannot abort the entire Go feature.
    grep -qF 'github.com/kodflow/ktn-linter/releases/latest/download/ktn-linter_linux_' "$SYNC_SH"
    grep -qF '.tar.gz' "$SYNC_SH"
    grep -qF 'github.com/kodflow/ktn-linter/cmd/ktn-linter@latest' "$SYNC_SH"
    ! grep -qF 'ktn-linter-linux-' "$SYNC_SH"
}

@test "sync_go auto-refreshes ktn-linter against upstream (no skip-if-installed)" {
    # Drift guard: the previous `if ! command -v ktn-linter` short-circuit
    # masked a stale volume-cached binary even when a newer release shipped.
    # The new code probes upstream tag and reinstalls on mismatch — symmetric
    # with the GO_TOOL_REPOS path used for golangci-lint/gosec.
    grep -qF 'upstream_latest_version "kodflow/ktn-linter"' "$SYNC_SH"
    grep -q 'installed=\$(installed_tool_version ktn-linter)' "$SYNC_SH"
    # Drift branch: `elif [ "$installed" != "$upstream" ]` triggers reinstall.
    grep -qE 'elif \[ "\$installed" != "\$upstream" \]' "$SYNC_SH"
    # Regression guard: the old `if ! command -v ktn-linter` short-circuit
    # is gone. The remaining `command -v ktn-linter` reference must live in
    # the final critical-tool gate (different intent: post-sync verification).
    ! grep -qE 'if ! command -v ktn-linter &>/dev/null; then' "$SYNC_SH"
}

@test "sync_go falls back to install-only-if-missing when ktn-linter probe fails" {
    # Network/rate-limit safety: an unreachable api.github.com must NOT clobber
    # a working binary. Reinstall only when the tool is genuinely absent.
    grep -B1 -A4 'ktn-linter: upstream probe failed' "$SYNC_SH" \
        | grep -qE 'if \[ -z "\$installed" \]'
}

@test "sync_go writes ktn-linter binary atomically (tarball + extract + sibling rename)" {
    # Without a temp+extract+rename, a curl killed mid-stream leaves a
    # half-written executable that subsequent runs treat as "installed" —
    # permanent drift. New shape: download tarball to .download.tgz, extract
    # to .download, chmod +x, then `mv -f` onto the final target.
    grep -qE 'tmp_tgz="\$\{?target\}?\.download\.tgz"' "$SYNC_SH"
    grep -qE 'tmp_bin="\$\{?target\}?\.download"' "$SYNC_SH"
    grep -qF 'tar -xzf "$tmp_tgz"' "$SYNC_SH"
    grep -qE 'mv -f "\$tmp_bin" "\$target"' "$SYNC_SH"
}

@test "sync_go preserves the existing tarball/binary-cache layout" {
    # GOPATH/bin is the install target; build-time installs land here too.
    grep -qE 'mkdir -p "\$\{GOPATH\}/bin"' "$SYNC_SH"
}

@test "sync_go skips reinstall when upstream probe fails AND tool is present" {
    # Network failure must NOT clobber a working binary. The branch that
    # handles `[ -z "$upstream" ]` only installs when the tool is missing.
    grep -A4 'upstream probe failed' "$SYNC_SH" | grep -q 'install_go_tool_latest'
    grep -B1 'upstream probe failed' "$SYNC_SH" | grep -q 'if \[ -z "\$installed" \]'
}
