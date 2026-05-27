#!/usr/bin/env bash
set -euo pipefail

# =============================================================================
# Rust Development Environment Installer
# =============================================================================
# Optimized for DevContainer: heavy libs (WebKitGTK) are in base image
# This script installs: rustup, toolchain, components, cargo tools
# =============================================================================

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
    err() { echo -e "${RED}✗${NC} $*" >&2; }
}

# Environment
export CARGO_HOME="${CARGO_HOME:-$HOME/.cache/cargo}"
export RUSTUP_HOME="${RUSTUP_HOME:-$HOME/.cache/rustup}"

print_banner "Rust Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Rust Development Environment"
    echo "========================================="
}

# =============================================================================
# Minimal System Dependencies (libs are in base image)
# =============================================================================
echo -e "${YELLOW}Installing minimal dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y --no-install-recommends \
    curl build-essential gcc make cmake pkg-config libssl-dev
ok "Dependencies ready"

# =============================================================================
# Rustup Installation (idempotent)
# =============================================================================
if command -v rustup &>/dev/null; then
    ok "rustup already installed"
else
    echo -e "${YELLOW}Installing rustup...${NC}"
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --no-modify-path
    ok "rustup installed"
fi

export PATH="$CARGO_HOME/bin:$PATH"
# shellcheck source=/dev/null
[[ -f "$CARGO_HOME/env" ]] && source "$CARGO_HOME/env"

# =============================================================================
# Toolchain & Components
# =============================================================================
echo -e "${YELLOW}Setting up stable toolchain...${NC}"
rustup toolchain install stable --profile minimal
rustup default stable
rustup component add rust-analyzer clippy rustfmt
ok "Toolchain: $(rustc --version)"

# =============================================================================
# Targets (idempotent installation)
# =============================================================================
ensure_target() {
    local target="$1" mode="${2:-required}"
    if rustup target list --installed 2>/dev/null | grep -q "^${target}$"; then
        ok "${target} (cached)"
        return 0
    fi
    if rustup target add "$target" 2>/dev/null; then
        ok "${target}"
    elif [[ "$mode" == "optional" ]]; then
        warn "${target} not available"
    else
        err "${target} failed" && return 1
    fi
}

echo -e "${YELLOW}Installing compilation targets...${NC}"

# Host target (always required)
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
    x86_64)  ensure_target "x86_64-unknown-linux-gnu" ;;
    aarch64) ensure_target "aarch64-unknown-linux-gnu" ;;
esac

# WASM targets (lightweight, useful for web dev)
ensure_target "wasm32-unknown-unknown" "optional"
ensure_target "wasm32-wasip1" "optional"
ensure_target "wasm32-wasip2" "optional"

# =============================================================================
# Cargo-binstall (fast binary installer)
# =============================================================================
echo -e "${YELLOW}Installing cargo-binstall...${NC}"
if command -v cargo-binstall &>/dev/null; then
    ok "cargo-binstall (cached)"
else
    curl -L --proto '=https' --tlsv1.2 -sSf \
        https://raw.githubusercontent.com/cargo-bins/cargo-binstall/main/install-from-binstall-release.sh | bash
    ok "cargo-binstall installed"
fi

# =============================================================================
# Cargo Tools — Parallel binstall with sequential fallback
# =============================================================================
# Removed: cargo-edit (cargo add/rm built-in since Rust 1.62)
# Removed: cargo-audit (cargo-deny covers advisories + licenses)
FAILED_TOOLS=()

echo -e "${YELLOW}Installing development tools...${NC}"

# Core tools (always installed)
CORE_TOOLS=(
    cargo-watch       # Auto-rebuild on file changes
    cargo-nextest     # Fast test runner
    cargo-deny        # Dependency security + license checker
)

BINSTALL_ONLY_TOOLS=(
    cargo-outdated    # Dependency update checker (skip if no binary — 3min compile)
    cargo-tarpaulin   # Code coverage tool (skip if no binary — 5min compile)
    wasm-bindgen-cli  # JS bindings generator (skip if no binary — 3min compile)
)

# Phase 1: Parallel binstall (fast binary downloads)
BINSTALL_PIDS=()
BINSTALL_TOOLS=()
for tool in "${CORE_TOOLS[@]}"; do
    if command -v "${tool}" &>/dev/null 2>&1; then
        ok "${tool} (cached)"
        continue
    fi
    (
        cargo binstall --no-confirm --locked "$tool" &>/dev/null
    ) &
    BINSTALL_PIDS+=("$!")
    BINSTALL_TOOLS+=("$tool")
done

# Collect results
TOOLS_TO_RETRY=()
for i in "${!BINSTALL_PIDS[@]}"; do
    if ! wait "${BINSTALL_PIDS[$i]}" 2>/dev/null; then
        TOOLS_TO_RETRY+=("${BINSTALL_TOOLS[$i]}")
    else
        ok "${BINSTALL_TOOLS[$i]} (binary)"
    fi
done

# Phase 2: Sequential fallback for CORE tools only (cargo install)
for tool in "${TOOLS_TO_RETRY[@]}"; do
    echo -e "${YELLOW}Compiling ${tool} (no binary available)...${NC}"
    if cargo install --locked "$tool" 2>/dev/null; then
        ok "${tool} (compiled)"
    else
        warn "${tool} failed"
        FAILED_TOOLS+=("$tool")
    fi
done

# Phase 3: Binary-only tools (skip compilation — too slow for devcontainer builds)
for tool in "${BINSTALL_ONLY_TOOLS[@]}"; do
    if command -v "${tool}" &>/dev/null 2>&1; then
        ok "${tool} (cached)"
        continue
    fi
    if cargo binstall --no-confirm --disable-strategies compile "$tool" 2>/dev/null; then
        ok "${tool} (binary)"
    else
        warn "${tool}: no pre-built binary, install with: cargo install ${tool}"
    fi
done

# cargo-expand: binary-only (skip compilation, it's huge)
if ! command -v cargo-expand &>/dev/null 2>&1; then
    echo -e "${YELLOW}Installing cargo-expand...${NC}"
    if cargo binstall --no-confirm --disable-strategies compile cargo-expand 2>/dev/null; then
        ok "cargo-expand (binary)"
    else
        warn "cargo-expand: no binary available, skipped"
    fi
else
    ok "cargo-expand (cached)"
fi

# =============================================================================
# Desktop/WASM tools — optimized install methods
# =============================================================================
echo -e "${YELLOW}Installing Desktop & WASM tools...${NC}"

# wasm-pack: use official installer (much faster than cargo)
if ! command -v wasm-pack &>/dev/null; then
    echo -e "${YELLOW}Installing wasm-pack...${NC}"
    if curl https://rustwasm.github.io/wasm-pack/installer/init.sh -sSf | sh 2>/dev/null; then
        ok "wasm-pack (installer)"
    else
        warn "wasm-pack failed"
        FAILED_TOOLS+=("wasm-pack")
    fi
else
    ok "wasm-pack (cached)"
fi

# tauri-cli: prefer npm (seconds vs minutes compiling)
if ! command -v cargo-tauri &>/dev/null 2>&1; then
    echo -e "${YELLOW}Installing tauri-cli...${NC}"
    if command -v npm &>/dev/null; then
        if npm install -g @tauri-apps/cli 2>/dev/null; then
            ok "tauri-cli (npm)"
        else
            warn "tauri-cli npm install failed, trying cargo"
            cargo binstall --no-confirm --locked tauri-cli 2>/dev/null && ok "tauri-cli (binary)" || warn "tauri-cli unavailable"
        fi
    else
        cargo binstall --no-confirm --locked tauri-cli 2>/dev/null && ok "tauri-cli (binary)" || warn "tauri-cli unavailable"
    fi
else
    ok "tauri-cli (cached)"
fi

# =============================================================================
# MCP server (installed last — may compile from source)
# =============================================================================
echo -e "${YELLOW}Installing rust-analyzer-mcp...${NC}"
if command -v rust-analyzer-mcp &>/dev/null 2>&1; then
    ok "rust-analyzer-mcp (cached)"
elif cargo binstall --no-confirm --locked rust-analyzer-mcp 2>/dev/null; then
    ok "rust-analyzer-mcp (binary)"
elif cargo install --locked rust-analyzer-mcp 2>/dev/null; then
    ok "rust-analyzer-mcp (compiled)"
else
    warn "rust-analyzer-mcp failed"
    FAILED_TOOLS+=("rust-analyzer-mcp")
fi

# =============================================================================
# Shell Integration
# =============================================================================
echo -e "${YELLOW}Configuring shell integration...${NC}"
CARGO_ENV='[[ -f "$HOME/.cache/cargo/env" ]] && source "$HOME/.cache/cargo/env"'
for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
    if [[ -f "$rc" ]] && ! grep -q "cargo/env" "$rc"; then
        echo -e "\n# Rust/Cargo environment\n$CARGO_ENV" >> "$rc"
    fi
done
ok "Shell integration configured"

# =============================================================================
# Summary
# =============================================================================
print_success_banner "Rust environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Rust environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - rustup (Rust toolchain manager)"
echo "  - $(rustc --version)"
echo "  - $(cargo --version)"
echo ""
echo "Development tools:"
echo "  - cargo-binstall (fast binary installer)"
echo "  - rust-analyzer (LSP)"
echo "  - clippy (linter)"
echo "  - rustfmt (formatter)"
echo "  - cargo-watch, cargo-nextest, cargo-deny"
echo "  - cargo-expand, cargo-outdated, cargo-tarpaulin"
echo ""
echo "Desktop & WASM tools:"
echo "  - tauri-cli (desktop apps)"
echo "  - wasm-pack, wasm-bindgen-cli"
echo "  - wasm32-unknown-unknown, wasm32-wasip1/2"
echo ""
echo "Cache directories:"
echo "  - CARGO_HOME: $CARGO_HOME"
echo "  - RUSTUP_HOME: $RUSTUP_HOME"
echo ""
echo "Note: WebKitGTK/Tauri libs are pre-installed in base image"
echo ""

# Write version marker for volume sync (Phase 2 acceleration)
sudo mkdir -p /opt/devcontainer-versions
{ rustc --version 2>/dev/null; cargo --version 2>/dev/null; } | sudo tee /opt/devcontainer-versions/rust >/dev/null

# Install MCP fragment (rust-analyzer MCP server)
# JSON is inlined because OCI feature artifacts don't include mcp.json files
install_mcp_fragment "rust" '{
  "servers": {
    "rust-analyzer": {
      "command": "/home/vscode/.cache/cargo/bin/rust-analyzer-mcp",
      "args": [],
      "env": {},
      "requires_binary": "/home/vscode/.cache/cargo/bin/rust-analyzer-mcp"
    }
  }
}'

if [[ ${#FAILED_TOOLS[@]} -gt 0 ]]; then
    warn "Some tools failed to install: ${FAILED_TOOLS[*]}"
fi
