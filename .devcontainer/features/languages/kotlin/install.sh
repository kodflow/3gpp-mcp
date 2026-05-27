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

print_banner "Kotlin Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Kotlin Development Environment"
    echo "========================================="
}

# Detect architecture
ARCH=$(uname -m)
echo -e "${YELLOW}Detected architecture: ${ARCH}${NC}"

# Install prerequisites
echo -e "${YELLOW}Installing prerequisites...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    zip \
    unzip \
    default-jdk

# ─────────────────────────────────────────────────────────────────────────────
# Install SDKMAN and Kotlin
# ─────────────────────────────────────────────────────────────────────────────
# Align with java feature and docker-compose.yml (SDKMAN_DIR=/home/vscode/.cache/sdkman)
export SDKMAN_DIR="${SDKMAN_DIR:-/home/vscode/.cache/sdkman}"

if [ ! -d "$SDKMAN_DIR" ]; then
    echo -e "${YELLOW}Installing SDKMAN...${NC}"
    SDKMAN_SCRIPT=$(mktemp)
    if curl -fsSL --retry 3 --retry-delay 5 -o "$SDKMAN_SCRIPT" "https://get.sdkman.io?rcupdate=false"; then
        bash "$SDKMAN_SCRIPT"
    else
        echo -e "${RED}Failed to download SDKMAN installer${NC}"
        rm -f "$SDKMAN_SCRIPT"
        exit 1
    fi
    rm -f "$SDKMAN_SCRIPT"
fi

# Source SDKMAN
set +e
source "$SDKMAN_DIR/bin/sdkman-init.sh"
set -e

echo -e "${YELLOW}Installing Kotlin via SDKMAN...${NC}"
sdk install kotlin
echo -e "${GREEN}+ Kotlin installed${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install ktlint via GitHub releases
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing ktlint...${NC}"
mkdir -p /home/vscode/.local/bin

KTLINT_VERSION=$(get_github_latest_version_or_empty "pinterest/ktlint")

if [ -z "$KTLINT_VERSION" ]; then
    echo -e "${YELLOW}⚠ Failed to resolve ktlint version, skipping${NC}"
elif curl -fsSL --connect-timeout 10 --max-time 120 \
    "https://github.com/pinterest/ktlint/releases/download/${KTLINT_VERSION}/ktlint" \
    -o /home/vscode/.local/bin/ktlint; then
    chmod +x /home/vscode/.local/bin/ktlint
    echo -e "${GREEN}+ ktlint ${KTLINT_VERSION} installed${NC}"
else
    echo -e "${YELLOW}⚠ ktlint download failed${NC}"
fi

export PATH="$SDKMAN_DIR/candidates/kotlin/current/bin:/home/vscode/.local/bin:$PATH"

print_success_banner "Kotlin environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Kotlin environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - java    $(java -version 2>&1 | head -n 1)"
echo "  - kotlin  $(kotlin -version 2>&1)"
echo "  - ktlint  $(ktlint --version 2>/dev/null || echo 'not available')"
echo ""
echo "Environment:"
echo "  - SDKMAN_DIR: $SDKMAN_DIR"
echo "  - ktlint:     /home/vscode/.local/bin/ktlint"
echo ""
