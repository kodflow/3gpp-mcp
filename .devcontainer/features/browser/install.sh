#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../languages/shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../languages/shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
    err() { echo -e "${RED}✗${NC} $*" >&2; }
    install_mcp_fragment() {
        local feature_name="$1" json_content="$2"
        if [ -n "$json_content" ]; then
            mkdir -p /etc/mcp/features
            printf '%s\n' "$json_content" > "/etc/mcp/features/${feature_name}.mcp.json"
            echo -e "${GREEN}✓${NC} MCP fragment installed for $feature_name"
        fi
    }
    print_banner() { echo -e "\n${GREEN}=========================================${NC}"; echo -e "${GREEN}  $1${NC}"; echo -e "${GREEN}=========================================${NC}\n"; }
    print_success() { echo -e "\n${GREEN}=========================================${NC}"; echo -e "${GREEN}  $1 installed successfully!${NC}"; echo -e "${GREEN}=========================================${NC}\n"; }
}

# =============================================================================
# Browser Feature - Playwright + Chromium
# =============================================================================

print_banner "Installing Browser (Playwright + Chromium)"

# Playwright requires Node.js (installed by nodejs feature)
if ! command -v node &>/dev/null; then
    err "Node.js is required for Playwright. Enable the nodejs feature first."
    exit 1
fi

# Install Playwright CLI and Chromium browser with system dependencies
echo "Installing Playwright and Chromium..."
npx -y playwright install --with-deps chromium 2>&1 | tail -5

# Verify installation
if npx -y playwright --version &>/dev/null; then
    ok "Playwright $(npx -y playwright --version 2>/dev/null) installed"
else
    warn "Playwright installation could not be verified"
fi

# Install MCP fragment (enables Playwright MCP server)
# JSON is inlined because OCI feature artifacts don't include mcp.json files
install_mcp_fragment "browser" '{
  "servers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest", "--headless", "--caps", "core,pdf,testing,tracing"],
      "requires_binary": "playwright"
    }
  }
}'

print_success "Browser (Playwright + Chromium)"
