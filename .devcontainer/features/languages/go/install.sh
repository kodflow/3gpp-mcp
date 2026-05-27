#!/bin/bash
# =============================================================================
# Go Development Environment — DevContainer Feature
# =============================================================================
# Strict mode: a silent failure here breaks downstream MCP tooling (ktn-linter)
# and lint hooks. We'd rather fail loud and visible than ship a half-installed
# container. See GitHub issue #324 for the original silent-skip regression.
# =============================================================================
set -Eeuo pipefail

# Global error trap — surfaces the exact line + command that failed so the
# devcontainer build log pinpoints the regression instead of dying silently.
on_error() {
    local code=$1 line=$2 cmd=$3
    echo -e "\033[0;31m✗ install.sh (go feature) FAILED at line ${line} (exit=${code}): ${cmd}\033[0m" >&2
    exit "$code"
}
trap 'on_error $? $LINENO "$BASH_COMMAND"' ERR

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
    err() { echo -e "${RED}✗${NC} $*" >&2; }
    get_github_latest_version() {
        local repo="$1" version auth_args=()
        [[ -n "${GITHUB_TOKEN:-}" ]] && auth_args=(-H "Authorization: token ${GITHUB_TOKEN}")
        local attempt
        for attempt in 1 2 3; do
            version=$(curl -fsS --connect-timeout 5 --max-time 10 \
                "${auth_args[@]}" \
                "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
                | sed -n 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/p' | head -n 1)
            [[ -n "$version" ]] && break
            sleep $((attempt * 2))
        done
        if [[ -z "$version" ]]; then
            echo -e "${RED}✗ Failed to resolve latest version for ${repo}${NC}" >&2
            return 1
        fi
        echo "$version"
    }
    get_github_latest_version_or_empty() {
        get_github_latest_version "$1" 2>/dev/null || echo ""
    }
}

# Structured step marker — lets users grep the devcontainer build log to see
# exactly where installation stopped. Keeping the format machine-parseable
# intentionally (space-delimited key=value).
step() { echo "[INSTALL-GO] step=$1 status=$2${3:+ detail=$3}"; }

print_banner "Go Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Go Development Environment"
    echo "========================================="
}

# Environment variables
export GO_VERSION="${GO_VERSION:-latest}"
export GOROOT="${GOROOT:-/usr/local/go}"
export GOPATH="${GOPATH:-/home/vscode/.cache/go}"
export GOCACHE="${GOCACHE:-/home/vscode/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-/home/vscode/.cache/go/pkg/mod}"
export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"

# Install dependencies
# Includes Wails/WebKitGTK dependencies for Linux desktop apps
step apt-deps begin
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    git \
    make \
    gcc \
    build-essential \
    pkg-config \
    libgtk-3-dev \
    libwebkit2gtk-4.1-dev \
    libglib2.0-dev
step apt-deps ok

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)
        GO_ARCH="amd64"
        ;;
    aarch64|arm64)
        GO_ARCH="arm64"
        ;;
    armv7l)
        GO_ARCH="armv6l"
        ;;
    *)
        echo -e "${RED}✗ Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

# Get latest Go version if requested
if [ "$GO_VERSION" = "latest" ]; then
    step go-version-resolve begin
    echo -e "${YELLOW}Fetching latest Go version...${NC}"
    GO_VERSION=$(curl -fsS --connect-timeout 5 --max-time 15 --retry 3 --retry-delay 2 \
        https://go.dev/VERSION?m=text | head -n 1 | sed 's/go//')
    if [[ -z "$GO_VERSION" ]]; then
        err "Failed to resolve latest Go version from go.dev — network or upstream issue."
        exit 1
    fi
    step go-version-resolve ok "$GO_VERSION"
fi

# Download and install Go
step go-toolchain begin "$GO_VERSION/$GO_ARCH"
echo -e "${YELLOW}Installing Go ${GO_VERSION} for ${GO_ARCH}...${NC}"
GO_TARBALL="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
GO_URL="https://dl.google.com/go/${GO_TARBALL}"

# Download Go tarball
curl -fsSL --retry 3 --retry-delay 5 -o "/tmp/${GO_TARBALL}" "$GO_URL"

# Remove any existing Go installation
if [ -d "$GOROOT" ]; then
    echo -e "${YELLOW}Removing existing Go installation...${NC}"
    sudo rm -rf "$GOROOT"
fi

# Extract to /usr/local
sudo tar -C /usr/local -xzf "/tmp/${GO_TARBALL}"

# Clean up
rm "/tmp/${GO_TARBALL}"

GO_INSTALLED=$(go version)
echo -e "${GREEN}✓ ${GO_INSTALLED} installed${NC}"
step go-toolchain ok

# Create necessary directories
mkdir -p "$GOPATH/bin"
mkdir -p "$GOPATH/pkg"
mkdir -p "$GOPATH/src"
mkdir -p "$GOCACHE"
mkdir -p "$GOMODCACHE"

# ─────────────────────────────────────────────────────────────────────────────
# Install Go Development Tools (prebuilt binaries from GitHub Releases)
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Go development tools...${NC}"
mkdir -p "$HOME/.local/bin"

# Helper function: download from GitHub Releases, fallback to go install.
# Returns non-zero on failure so the caller's wait <pid> can detect it.
install_go_tool() {
    local name=$1
    local url=$2
    local go_pkg=$3
    local extract_type=$4  # "tar.gz", "zip", or "binary"

    echo -e "${YELLOW}Installing ${name}...${NC}"

    local tmp_file="/tmp/${name}-download"
    local installed=0

    if curl -fsSL --connect-timeout 10 --max-time 120 "$url" -o "$tmp_file" 2>/dev/null; then
        case "$extract_type" in
            tar.gz)
                if tar -xzf "$tmp_file" -C "$GOPATH/bin/" "$name" 2>/dev/null || \
                   tar -xzf "$tmp_file" --wildcards --strip-components=1 -C "$GOPATH/bin/" "*/$name" 2>/dev/null || \
                   { tar -xzf "$tmp_file" -C "/tmp/" && mv "/tmp/$name" "$GOPATH/bin/" 2>/dev/null; }; then
                    installed=1
                fi
                ;;
            zip)
                if unzip -o "$tmp_file" "$name" -d "$GOPATH/bin/" 2>/dev/null || \
                   { unzip -o "$tmp_file" -d "/tmp/${name}-extracted" 2>/dev/null && mv "/tmp/${name}-extracted/$name" "$GOPATH/bin/" 2>/dev/null; }; then
                    installed=1
                fi
                ;;
            binary)
                if mv "$tmp_file" "$GOPATH/bin/$name" 2>/dev/null; then
                    installed=1
                fi
                ;;
        esac
        if [[ "$installed" == "1" ]]; then
            chmod +x "$GOPATH/bin/$name"
        fi
        rm -f "$tmp_file" "/tmp/${name}-extracted" 2>/dev/null || true
    fi

    if [[ "$installed" == "1" ]]; then
        echo -e "${GREEN}✓ ${name} installed (binary)${NC}"
        return 0
    fi

    if [ -n "$go_pkg" ]; then
        echo -e "${YELLOW}  ${name}: binary download failed, falling back to go install...${NC}"
        if go install "${go_pkg}@latest"; then
            echo -e "${GREEN}✓ ${name} installed (compiled)${NC}"
            return 0
        fi
        err "${name}: both binary download and 'go install' failed"
        return 1
    fi

    err "${name}: binary download failed and no 'go install' fallback configured"
    return 1
}

# Parallel-install helper: runs install_go_tool in a subshell that MUST exit
# non-zero on failure. Our ERR trap is suppressed inside these subshells (the
# caller's `wait <pid>` is what surfaces the failure).
spawn_tool() {
    local name=$1; shift
    # Subshell disables the inherited ERR trap so failures of individual tools
    # don't crash the whole script before we had a chance to collect them.
    set +e
    ( trap - ERR; install_go_tool "$name" "$@" ) &
    TOOL_PIDS[$name]=$!
    set -e
}

# Fetch latest versions (non-fatal: empty string on rate-limit/timeout, retries 3x)
step fetch-tool-versions begin
GOLANGCI_VERSION=$(get_github_latest_version_or_empty "golangci/golangci-lint")
GOSEC_VERSION=$(get_github_latest_version_or_empty "securego/gosec")
GOFUMPT_VERSION=$(get_github_latest_version_or_empty "mvdan/gofumpt")
GOTESTSUM_VERSION=$(get_github_latest_version_or_empty "gotestyourself/gotestsum")
# buildifier + buildozer ship from the same bazelbuild/buildtools release.
BUILDTOOLS_VERSION=$(get_github_latest_version_or_empty "bazelbuild/buildtools")

# gofumpt uses 'v' prefix in URLs
[[ -n "$GOFUMPT_VERSION" && "$GOFUMPT_VERSION" != v* ]] && GOFUMPT_VERSION="v${GOFUMPT_VERSION}"
step fetch-tool-versions ok

# Track each parallel install so we can collect individual exit codes. Without
# per-PID tracking a silent `&`+`wait` loses failures — the original bug.
declare -A TOOL_PIDS=()
declare -A TOOL_STATUS=()

step install-tools begin
if [[ -n "$GOLANGCI_VERSION" ]]; then
    spawn_tool "golangci-lint" \
        "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}/golangci-lint-${GOLANGCI_VERSION}-linux-${GO_ARCH}.tar.gz" \
        "github.com/golangci/golangci-lint/v2/cmd/golangci-lint" \
        "tar.gz"
else
    set +e
    ( trap - ERR; \
      echo -e "${YELLOW}golangci-lint: version unavailable, building from source...${NC}" && \
      go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest && \
      echo -e "${GREEN}✓ golangci-lint installed (from source)${NC}" ) &
    TOOL_PIDS[golangci-lint]=$!
    set -e
fi

if [[ -n "$GOSEC_VERSION" ]]; then
    spawn_tool "gosec" \
        "https://github.com/securego/gosec/releases/download/v${GOSEC_VERSION}/gosec_${GOSEC_VERSION}_linux_${GO_ARCH}.tar.gz" \
        "github.com/securego/gosec/v2/cmd/gosec" \
        "tar.gz"
else
    set +e
    ( trap - ERR; \
      echo -e "${YELLOW}gosec: version unavailable, building from source...${NC}" && \
      go install github.com/securego/gosec/v2/cmd/gosec@latest && \
      echo -e "${GREEN}✓ gosec installed (from source)${NC}" ) &
    TOOL_PIDS[gosec]=$!
    set -e
fi

if [[ -n "$GOFUMPT_VERSION" ]]; then
    spawn_tool "gofumpt" \
        "https://github.com/mvdan/gofumpt/releases/download/${GOFUMPT_VERSION}/gofumpt_${GOFUMPT_VERSION}_linux_${GO_ARCH}" \
        "mvdan.cc/gofumpt" \
        "binary"
else
    set +e
    ( trap - ERR; \
      echo -e "${YELLOW}gofumpt: version unavailable, building from source...${NC}" && \
      go install mvdan.cc/gofumpt@latest && \
      echo -e "${GREEN}✓ gofumpt installed (from source)${NC}" ) &
    TOOL_PIDS[gofumpt]=$!
    set -e
fi

if [[ -n "$GOTESTSUM_VERSION" ]]; then
    spawn_tool "gotestsum" \
        "https://github.com/gotestyourself/gotestsum/releases/download/v${GOTESTSUM_VERSION}/gotestsum_${GOTESTSUM_VERSION}_linux_${GO_ARCH}.tar.gz" \
        "gotest.tools/gotestsum" \
        "tar.gz"
else
    set +e
    ( trap - ERR; \
      echo -e "${YELLOW}gotestsum: version unavailable, building from source...${NC}" && \
      go install gotest.tools/gotestsum@latest && \
      echo -e "${GREEN}✓ gotestsum installed (from source)${NC}" ) &
    TOOL_PIDS[gotestsum]=$!
    set -e
fi

# goimports has no GitHub release — always compile from source.
set +e
( trap - ERR; \
  echo -e "${YELLOW}Installing goimports...${NC}" && \
  go install golang.org/x/tools/cmd/goimports@latest && \
  echo -e "${GREEN}✓ goimports installed${NC}" ) &
TOOL_PIDS[goimports]=$!
set -e

# Release asset is goreleaser-style `ktn-linter_linux_<arch>.tar.gz` containing
# the binary at the tarball root. The legacy hyphenated raw-binary URL never
# existed as a published asset — that 404 broke every container build because
# no go-install fallback was wired (issue #324 follow-up). The `cmd/ktn-linter`
# fallback covers a Releases-CDN blip or future asset-naming skew.
spawn_tool "ktn-linter" \
    "https://github.com/kodflow/ktn-linter/releases/latest/download/ktn-linter_linux_${GO_ARCH}.tar.gz" \
    "github.com/kodflow/ktn-linter/cmd/ktn-linter" \
    "tar.gz"

# Bazel tooling — same release ships both binaries.
if [[ -n "$BUILDTOOLS_VERSION" ]]; then
    spawn_tool "buildifier" \
        "https://github.com/bazelbuild/buildtools/releases/download/v${BUILDTOOLS_VERSION}/buildifier-linux-${GO_ARCH}" \
        "github.com/bazelbuild/buildtools/buildifier" \
        "binary"
    spawn_tool "buildozer" \
        "https://github.com/bazelbuild/buildtools/releases/download/v${BUILDTOOLS_VERSION}/buildozer-linux-${GO_ARCH}" \
        "github.com/bazelbuild/buildtools/buildozer" \
        "binary"
else
    set +e
    ( trap - ERR; \
      echo -e "${YELLOW}buildifier/buildozer: version unavailable, building from source...${NC}" && \
      go install github.com/bazelbuild/buildtools/buildifier@latest && \
      go install github.com/bazelbuild/buildtools/buildozer@latest && \
      echo -e "${GREEN}✓ buildifier + buildozer installed (from source)${NC}" ) &
    TOOL_PIDS[buildifier]=$!
    TOOL_PIDS[buildozer]=$!
    set -e
fi

# Collect exit codes per tool. `wait <pid>` is the ONLY reliable way to get
# the child's real exit code after `&`; the bare `wait` returns 0 always.
for tool in "${!TOOL_PIDS[@]}"; do
    if wait "${TOOL_PIDS[$tool]}" 2>/dev/null; then
        TOOL_STATUS[$tool]="ok"
    else
        TOOL_STATUS[$tool]="fail"
    fi
done

# Classify tools. Critical tools are those the rest of the stack depends on —
# the Claude Code lint hooks (golangci-lint, gofumpt, goimports) and the MCP
# server binary (ktn-linter). A missing critical tool is a hard error: ship a
# broken container and we reproduce exactly issue #324.
CRITICAL_TOOLS=("golangci-lint" "gofumpt" "goimports" "ktn-linter")
OPTIONAL_TOOLS=("gosec" "gotestsum" "buildifier" "buildozer")

missing_critical=()
for tool in "${CRITICAL_TOOLS[@]}"; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        missing_critical+=("$tool")
    fi
done

missing_optional=()
for tool in "${OPTIONAL_TOOLS[@]}"; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        missing_optional+=("$tool")
    fi
done

if (( ${#missing_optional[@]} > 0 )); then
    warn "Optional Go tools missing (non-blocking): ${missing_optional[*]}"
fi

if (( ${#missing_critical[@]} > 0 )); then
    err "CRITICAL Go tools missing: ${missing_critical[*]}"
    err "Per-tool install status:"
    for tool in "${!TOOL_STATUS[@]}"; do
        err "  - $tool: ${TOOL_STATUS[$tool]}"
    done
    err "Feature installation INCOMPLETE — refusing to ship a half-installed Go toolchain."
    err "Fix the underlying install failure (network? GitHub rate limit? permissions?) and re-run."
    exit 1
fi

# Smoke test (issue #329): refuse to ship a v1 golangci-lint. v1 is EOL since
# 2025-03 and cannot parse `.golangci.yml` `version: "2"` configs. The build-
# time install resolves `latest` from the GitHub API at THIS moment — if that
# returns something other than v2.x for any reason (cache, rate-limited fallback
# to a stale tag, future major bump that needs a wiring update), fail loud here
# rather than ship a broken container. The runtime path (sync-toolchains.sh)
# auto-corrects on every onCreate, but that's the safety net, not the gate.
if command -v golangci-lint >/dev/null 2>&1; then
    GOLANGCI_INSTALLED_MAJOR=$(golangci-lint version 2>&1 \
        | sed -n 's/.*has version v\?\([0-9]\+\).*/\1/p' | head -n 1)
    if [[ "$GOLANGCI_INSTALLED_MAJOR" != "2" ]]; then
        err "golangci-lint v${GOLANGCI_INSTALLED_MAJOR:-?}.x detected — v2.x required (issue #329)."
        err "  v1 is EOL since 2025-03 and cannot parse 'version: \"2\"' configs."
        err "  This usually means the GitHub API returned a stale 'latest' at build time."
        err "  Re-run the build; if the error persists, wire the v2 module path explicitly."
        exit 1
    fi
fi
step install-tools ok

# Install ktn-linter MCP fragment IMMEDIATELY after the binary check passes.
# Previously this was at the very end of the script, so any later failure
# (e.g. Wails/TinyGo download) would leave /etc/mcp/features/ empty and the
# MCP server silently absent from the merged mcp.json — the root cause of #324.
step mcp-fragment begin
install_mcp_fragment "go" '{
  "servers": {
    "ktn-linter": {
      "command": "ktn-linter",
      "args": ["serve", "--port=7717"],
      "requires_binary": "ktn-linter"
    }
  }
}'
step mcp-fragment ok

# ─────────────────────────────────────────────────────────────────────────────
# Install Wails v2 + TinyGo in parallel (optional — desktop/WASM toolchain)
# ─────────────────────────────────────────────────────────────────────────────
step optional-tools begin

# Wails v2 (Desktop GUI Framework) — binary from GitHub Releases, fallback go install
set +e
(
    trap - ERR
    echo -e "${YELLOW}Installing Wails v2 (desktop GUI framework)...${NC}"
    WAILS_VERSION=$(get_github_latest_version_or_empty "wailsapp/wails")
    WAILS_INSTALLED=false
    if [[ -n "$WAILS_VERSION" ]]; then
        WAILS_URL="https://github.com/wailsapp/wails/releases/download/v${WAILS_VERSION}/wails-linux-${GO_ARCH}"
        if curl -fsSL --connect-timeout 10 --max-time 60 -o "$GOPATH/bin/wails" "$WAILS_URL" 2>/dev/null; then
            chmod +x "$GOPATH/bin/wails"
            echo -e "${GREEN}✓ Wails v${WAILS_VERSION} (binary)${NC}"
            WAILS_INSTALLED=true
        fi
    fi
    if [[ "$WAILS_INSTALLED" != "true" ]]; then
        echo -e "${YELLOW}  Fallback to go install...${NC}"
        if go install github.com/wailsapp/wails/v2/cmd/wails@latest; then
            echo -e "${GREEN}✓ Wails installed (compiled)${NC}"
        else
            warn "Wails installation failed (non-blocking)"
        fi
    fi
) &
WAILS_PID=$!
set -e

# TinyGo (WebAssembly Compiler)
set +e
(
    trap - ERR
    echo -e "${YELLOW}Installing TinyGo (WASM/embedded compiler)...${NC}"
    TINYGO_VERSION=$(get_github_latest_version_or_empty "tinygo-org/tinygo")
    if [[ -z "$TINYGO_VERSION" ]]; then
        warn "TinyGo version resolution failed (non-blocking)"
        exit 0
    fi
    case "$GO_ARCH" in
        amd64) TINYGO_PKG="tinygo_${TINYGO_VERSION}_amd64.deb" ;;
        arm64) TINYGO_PKG="tinygo_${TINYGO_VERSION}_arm64.deb" ;;
        *)     warn "TinyGo not available for ${GO_ARCH} (non-blocking)"; exit 0 ;;
    esac
    TINYGO_URL="https://github.com/tinygo-org/tinygo/releases/download/v${TINYGO_VERSION}/${TINYGO_PKG}"
    if curl -fsSL --connect-timeout 10 --max-time 120 -o "/tmp/${TINYGO_PKG}" "$TINYGO_URL" 2>/dev/null; then
        sudo dpkg -i "/tmp/${TINYGO_PKG}" 2>/dev/null || sudo apt-get install -f -y
        rm -f "/tmp/${TINYGO_PKG}"
        if command -v tinygo &> /dev/null; then
            TINYGO_INSTALLED=$(tinygo version 2>/dev/null | head -n 1)
            echo -e "${GREEN}✓ ${TINYGO_INSTALLED}${NC}"
        else
            echo -e "${GREEN}✓ TinyGo ${TINYGO_VERSION} installed${NC}"
        fi
    else
        warn "TinyGo download failed (non-blocking)"
    fi
) &
TINYGO_PID=$!
set -e

wait "$WAILS_PID" 2>/dev/null || warn "Wails subshell exit code non-zero (non-blocking)"
wait "$TINYGO_PID" 2>/dev/null || warn "TinyGo subshell exit code non-zero (non-blocking)"
step optional-tools ok

print_success_banner "Go environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Go environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${GO_INSTALLED}"
echo "  - Go Modules (package manager)"
echo ""
echo "Development tools:"
for _tool in golangci-lint gosec gofumpt goimports gotestsum ktn-linter buildifier buildozer; do
    if command -v "$_tool" &>/dev/null; then
        echo "  - $_tool (installed)"
    else
        echo "  - $_tool (skipped)"
    fi
done
echo ""
echo "Desktop & WASM tools:"
echo "  - wails (desktop GUI framework)"
echo "  - tinygo (WASM/embedded compiler)"
echo ""
echo "Cache directories:"
echo "  - GOPATH: $GOPATH"
echo "  - GOCACHE: $GOCACHE"
echo "  - GOMODCACHE: $GOMODCACHE"
echo ""

# Write version marker for volume sync (Phase 2 acceleration)
sudo mkdir -p /opt/devcontainer-versions
go version 2>/dev/null | sudo tee /opt/devcontainer-versions/go >/dev/null

step finish ok
