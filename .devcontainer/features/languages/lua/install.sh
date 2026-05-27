#!/bin/bash
set -e

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

print_banner "Lua Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Lua Development Environment"
    echo "========================================="
}

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GH_ARCH="x86_64" ;;
    aarch64) GH_ARCH="aarch64" ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

# Install Lua and LuaRocks
echo -e "${YELLOW}Installing Lua 5.4 and LuaRocks...${NC}"
sudo apt-get update && sudo apt-get install -y \
    lua5.4 \
    liblua5.4-dev \
    luarocks \
    curl

echo -e "${GREEN}✓ Lua $(lua5.4 -v 2>&1 | head -n 1) installed${NC}"
echo -e "${GREEN}✓ LuaRocks $(luarocks --version | head -n 1) installed${NC}"

# Install StyLua (formatter) from GitHub releases
echo -e "${YELLOW}Installing StyLua (formatter)...${NC}"
STYLUA_VERSION=$(get_github_latest_version_or_empty "JohnnyMorganz/StyLua")
if [ -z "$STYLUA_VERSION" ]; then
    echo -e "${YELLOW}⚠ Failed to resolve StyLua version, skipping${NC}"
fi

if [ -n "$STYLUA_VERSION" ]; then
STYLUA_URL="https://github.com/JohnnyMorganz/StyLua/releases/download/v${STYLUA_VERSION}/stylua-v${STYLUA_VERSION}-linux-${GH_ARCH}.zip"
if curl -fsSL --connect-timeout 10 --max-time 60 -o /tmp/stylua.zip "$STYLUA_URL"; then
    sudo unzip -o /tmp/stylua.zip -d /usr/local/bin/
    sudo chmod +x /usr/local/bin/stylua
    rm -f /tmp/stylua.zip
    echo -e "${GREEN}✓ StyLua ${STYLUA_VERSION} installed${NC}"
else
    echo -e "${YELLOW}⚠ StyLua download failed${NC}"
fi
fi

# Install Luacheck (linter) via LuaRocks
echo -e "${YELLOW}Installing Luacheck (linter)...${NC}"
if sudo luarocks install luacheck; then
    echo -e "${GREEN}✓ Luacheck installed${NC}"
else
    echo -e "${YELLOW}⚠ Luacheck installation failed${NC}"
fi

print_success_banner "Lua environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Lua environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - $(lua5.4 -v 2>&1 | head -n 1)"
echo "  - $(luarocks --version | head -n 1)"
echo ""
echo "Development tools:"
echo "  - StyLua (formatter)"
echo "  - Luacheck (linter)"
echo ""
