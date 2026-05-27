#!/bin/bash
# ============================================================================
# sync-toolchains.sh — Sync toolchains from image to volume on version mismatch
# Called by onCreateCommand (volumes already mounted)
#
# Problem: Features install to ~/.cache/ during build. Volume mounts OVER that
# path at runtime. On rebuild, the volume has OLD tools.
#
# Solution: Features write version markers to /opt/devcontainer-versions/.
# This script compares with volume markers and re-installs only on mismatch.
# ============================================================================

set -e

VERSIONS_DIR="/opt/devcontainer-versions"
CACHE_DIR="${HOME}/.cache"

[ -d "$VERSIONS_DIR" ] || exit 0

sync_lang() {
    local lang="$1"
    local image_ver volume_ver

    image_ver=$(cat "${VERSIONS_DIR}/${lang}" 2>/dev/null) || return 0
    volume_ver=$(cat "${CACHE_DIR}/.toolchain-version-${lang}" 2>/dev/null || echo "")

    if [ "$image_ver" = "$volume_ver" ]; then
        echo "  ${lang}: up to date (volume cache hit)"
        return 0
    fi

    echo "  ${lang}: version mismatch (image=${image_ver} volume=${volume_ver:-none})"
    echo "  ${lang}: syncing from image..."

    # Capture the handler's exit status so a partial sync doesn't poison the
    # volume marker. Without this, a transient network failure during cold
    # install would still bump `.toolchain-version-${lang}`, causing every
    # subsequent onCreate to short-circuit on the version match and skip the
    # sync forever — leaving critical tools permanently absent (Qodo +
    # CodeRabbit, #330).
    local rc=0
    set +e
    case "$lang" in
        rust)   sync_rust ;;
        go)     sync_go ;;
        python) sync_python ;;
        node)   sync_node ;;
        *)
            echo "  ${lang}: no sync handler, skipping"
            set -e
            return 0
            ;;
    esac
    rc=$?
    set -e

    if [ "$rc" -ne 0 ]; then
        echo "  ${lang}: sync incomplete (rc=${rc}) — marker NOT updated, will retry next onCreate"
        return 0
    fi

    echo "$image_ver" > "${CACHE_DIR}/.toolchain-version-${lang}"
    echo "  ${lang}: synced"
}

sync_rust() {
    export CARGO_HOME="${CARGO_HOME:-${CACHE_DIR}/cargo}"
    export RUSTUP_HOME="${RUSTUP_HOME:-${CACHE_DIR}/rustup}"
    export PATH="${CARGO_HOME}/bin:${PATH}"

    if ! command -v rustup &>/dev/null; then
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --no-modify-path 2>/dev/null
    fi

    [ -f "${CARGO_HOME}/env" ] && source "${CARGO_HOME}/env"
    rustup toolchain install stable --profile minimal 2>/dev/null
    rustup default stable 2>/dev/null
    rustup component add rust-analyzer clippy rustfmt 2>/dev/null

    if command -v cargo-binstall &>/dev/null || {
        curl -L --proto '=https' --tlsv1.2 -sSf \
            https://raw.githubusercontent.com/cargo-bins/cargo-binstall/main/install-from-binstall-release.sh | bash 2>/dev/null
    }; then
        local tools=(cargo-watch cargo-nextest cargo-deny)
        for tool in "${tools[@]}"; do
            command -v "$tool" &>/dev/null || cargo binstall --no-confirm --locked "$tool" 2>/dev/null || true
        done
        local binstall_only=(cargo-outdated cargo-tarpaulin wasm-bindgen-cli cargo-expand)
        for tool in "${binstall_only[@]}"; do
            command -v "$tool" &>/dev/null || cargo binstall --no-confirm --disable-strategies compile "$tool" 2>/dev/null || true
        done
    fi
}

sync_go() {
    export GOROOT="${GOROOT:-/usr/local/go}"
    export GOPATH="${GOPATH:-${CACHE_DIR}/go}"
    export GOCACHE="${GOCACHE:-${CACHE_DIR}/go-build}"
    export PATH="${GOROOT}/bin:${GOPATH}/bin:${PATH}"

    mkdir -p "${GOPATH}/bin"

    if ! command -v go &>/dev/null; then
        echo "  go: binary missing (requires rebuild)"
        return 1
    fi

    local ARCH
    ARCH=$(uname -m)
    local GO_ARCH
    case "$ARCH" in
        x86_64) GO_ARCH="amd64" ;;
        aarch64|arm64) GO_ARCH="arm64" ;;
        *) GO_ARCH="amd64" ;;
    esac

    # Tool → full Go module path. The previous `go install ${tool}@latest`
    # was broken: `golangci-lint`, `gosec`, `gofumpt`, `gotestsum` are not
    # bare module names and silently failed (suffixed `|| true`). That mask
    # is why issue #329 (v1.64.8 stuck after v2.0.0 EOL) survived rebuilds.
    declare -A GO_TOOL_MODULES=(
        [golangci-lint]="github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
        [gosec]="github.com/securego/gosec/v2/cmd/gosec"
        [gofumpt]="mvdan.cc/gofumpt"
        [gotestsum]="gotest.tools/gotestsum"
        [goimports]="golang.org/x/tools/cmd/goimports"
    )
    # Repo for upstream-version probe. Tools listed here are auto-refreshed
    # to upstream `latest` on every sync — drift-resistant by design, so a
    # stale volume can't pin us to an EOL major branch (issue #329). The set
    # is deliberately small: only tools whose upstream has a history of
    # major-version EOL transitions.
    declare -A GO_TOOL_REPOS=(
        [golangci-lint]="golangci/golangci-lint"
        [gosec]="securego/gosec"
    )

    install_go_tool_latest() {
        local tool="$1"
        local module="${GO_TOOL_MODULES[$tool]:-}"
        if [ -z "$module" ]; then
            echo "    ${tool}: no module path mapping (skipped)"
            return 1
        fi
        go install "${module}@latest" 2>/dev/null
    }

    # Extract the major version pinned in the Go module path (the `/vN` suffix
    # — Go module convention for v2+). Returns empty for v0/v1 modules that
    # don't carry a suffix. Without this, a /v2 module path against a repo
    # whose `releases/latest` is on v3 produces a permanent installed≠upstream
    # mismatch and an infinite reinstall loop on every onCreate. (CodeRabbit, #330)
    module_major_pin() {
        local tool="$1"
        local module="${GO_TOOL_MODULES[$tool]:-}"
        echo "$module" | sed -n 's|.*/v\([0-9]\+\)/.*|\1|p'
    }

    upstream_latest_version() {
        local repo="$1"
        local major="${2:-}"
        local auth=()
        [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: token ${GITHUB_TOKEN}")
        # X.Y.Z triple only — symmetric with installed_tool_version. When a
        # major is supplied, restrict the search to releases on that major
        # (paginated list, not /latest, so we can filter). v3+ releases of a
        # /v2-pinned tool will never trigger a reinstall — we stay on v2.x
        # latest until the module path is intentionally bumped.
        local url
        if [ -n "$major" ]; then
            url="https://api.github.com/repos/${repo}/releases?per_page=30"
        else
            url="https://api.github.com/repos/${repo}/releases/latest"
        fi
        local raw
        raw=$(curl -fsS --connect-timeout 3 --max-time 8 "${auth[@]}" "$url" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"v\?\([0-9]\+\.[0-9]\+\.[0-9]\+\).*/\1/p')
        if [ -n "$major" ]; then
            echo "$raw" | grep -E "^${major}\." | head -n 1
        else
            echo "$raw" | head -n 1
        fi
    }

    installed_tool_version() {
        local tool="$1"
        command -v "$tool" >/dev/null 2>&1 || { echo ""; return; }
        # The first NN.NN.NN triple in `<tool> version` output works for
        # golangci-lint ("has version v2.11.4 ..."), gosec ("Version: 2.22.5"),
        # gofumpt, gotestsum — the format is intentionally lax.
        "$tool" version 2>&1 \
            | sed -n 's/.*\bv\?\([0-9]\+\.[0-9]\+\.[0-9]\+\)\b.*/\1/p' \
            | head -n 1
    }

    # Per-iteration failure flag. Treat absent critical tools as a hard sync
    # failure so sync_lang's marker write is preempted by `set -e` and the
    # next onCreate retries (Qodo + CodeRabbit, #330). Without this, a
    # transient network failure during the cold-install path would update
    # the marker with tools missing, causing every future onCreate to
    # short-circuit and skip Go sync entirely.
    local failed=0
    local tool installed upstream major_pin
    # Atomic install for ktn-linter (goreleaser tarball, not a Go module path).
    # Writes to a sibling .download path then renames — protects against a
    # half-fetched payload if curl is interrupted mid-stream. Falls back to
    # `go install …/cmd/ktn-linter` if the release CDN 404s (asset-naming
    # drift) or times out. Returns non-zero only when both paths fail.
    install_ktn_linter() {
        local target="${GOPATH}/bin/ktn-linter"
        local tmp_tgz="${target}.download.tgz"
        local tmp_bin="${target}.download"
        if curl -fsSL --connect-timeout 10 --max-time 60 \
                "https://github.com/kodflow/ktn-linter/releases/latest/download/ktn-linter_linux_${GO_ARCH}.tar.gz" \
                -o "$tmp_tgz" 2>/dev/null \
            && tar -xzf "$tmp_tgz" -C "$(dirname "$tmp_bin")" ktn-linter 2>/dev/null \
            && mv -f "$(dirname "$tmp_bin")/ktn-linter" "$tmp_bin" \
            && chmod +x "$tmp_bin" \
            && mv -f "$tmp_bin" "$target"; then
            rm -f "$tmp_tgz"
            return 0
        fi
        rm -f "$tmp_tgz" "$tmp_bin"
        if command -v go >/dev/null 2>&1 \
            && go install "github.com/kodflow/ktn-linter/cmd/ktn-linter@latest" 2>/dev/null; then
            return 0
        fi
        return 1
    }

    for tool in golangci-lint gosec gofumpt gotestsum goimports ktn-linter; do
        case "$tool" in
            ktn-linter)
                # ktn-linter is published as a release binary on GitHub, NOT as a Go
                # module — `go install ktn-linter@latest` silently fails (not a valid
                # module path). Must download the release asset directly, and do it
                # HERE at runtime so the write lands in the already-mounted
                # package-cache volume (build-time writes under $GOPATH/bin are
                # masked by the volume at container start).
                #
                # Auto-refresh: probe upstream tag, reinstall on drift. Without
                # this guard, an old ktn-linter cached in the volume survived
                # every container rebuild — `command -v` short-circuited the
                # install path even when a newer release was available. Mirrors
                # the GO_TOOL_REPOS pattern (#330) but with direct asset download.
                installed=$(installed_tool_version ktn-linter)
                upstream=$(upstream_latest_version "kodflow/ktn-linter")
                if [ -z "$upstream" ]; then
                    # Network/rate-limit failure: keep an existing binary, only
                    # install when truly missing (best-effort cold-start path).
                    if [ -z "$installed" ]; then
                        echo "    ktn-linter: upstream probe failed and tool missing — installing latest (best-effort)..."
                        install_ktn_linter || failed=1
                    fi
                elif [ "$installed" != "$upstream" ]; then
                    echo "    ktn-linter: ${installed:-none} → ${upstream}"
                    install_ktn_linter || failed=1
                fi
                ;;
            *)
                if [ -n "${GO_TOOL_REPOS[$tool]:-}" ]; then
                    # Auto-refresh path: probe upstream within the pinned
                    # major branch, reinstall on drift. Network/rate-limit
                    # failure leaves the existing binary alone (only installs
                    # if truly missing, in which case we mark sync as failed).
                    installed=$(installed_tool_version "$tool")
                    major_pin=$(module_major_pin "$tool")
                    upstream=$(upstream_latest_version "${GO_TOOL_REPOS[$tool]}" "$major_pin")
                    if [ -z "$upstream" ]; then
                        if [ -z "$installed" ]; then
                            echo "    ${tool}: upstream probe failed and tool missing — installing latest (best-effort)..."
                            install_go_tool_latest "$tool" || failed=1
                        fi
                        continue
                    fi
                    if [ "$installed" != "$upstream" ]; then
                        echo "    ${tool}: ${installed:-none} → ${upstream}"
                        install_go_tool_latest "$tool" || failed=1
                    fi
                else
                    # No repo mapping → install only if missing (gofumpt,
                    # gotestsum, goimports — none have shipped an EOL major).
                    if ! command -v "$tool" &>/dev/null; then
                        echo "    installing ${tool}..."
                        install_go_tool_latest "$tool" || failed=1
                    fi
                fi
                ;;
        esac
    done

    # Final critical-tool gate: even if every install attempt above appeared
    # to succeed, refuse to claim sync success if the lint hooks would still
    # break for lack of a binary. Mirrors install.sh's CRITICAL_TOOLS set.
    for tool in golangci-lint gofumpt goimports ktn-linter; do
        if ! command -v "$tool" >/dev/null 2>&1; then
            echo "    ${tool}: still missing after sync — marking failure"
            failed=1
        fi
    done

    return "$failed"
}

sync_python() {
    if ! command -v python3 &>/dev/null; then
        echo "  python: binary missing (requires rebuild)"
        return 1
    fi
    local tools=(ruff pylint mypy bandit pytest)
    for tool in "${tools[@]}"; do
        command -v "$tool" &>/dev/null || python3 -m pip install --quiet "$tool" 2>/dev/null || true
    done
}

sync_node() {
    if ! command -v node &>/dev/null; then
        echo "  node: binary missing (requires rebuild)"
        return 1
    fi
}

echo "Syncing toolchains (image → volume)..."
for marker in "${VERSIONS_DIR}"/*; do
    [ -f "$marker" ] || continue
    lang=$(basename "$marker")
    sync_lang "$lang"
done
echo "Toolchain sync complete."
