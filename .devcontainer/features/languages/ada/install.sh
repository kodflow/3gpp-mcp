#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
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

print_banner "Ada Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Ada Development Environment"
    echo "========================================="
}

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ALR_ARCH="x86_64" ;;
    aarch64) ALR_ARCH="aarch64" ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

# Install GNAT and GPRbuild
echo -e "${YELLOW}Installing GNAT and GPRbuild...${NC}"
sudo apt-get update && sudo apt-get install -y \
    gnat \
    gprbuild \
    curl \
    unzip

GNAT_VERSION=$(gnat --version | head -n 1)
GPRBUILD_VERSION=$(gprbuild --version | head -n 1)
echo -e "${GREEN}✓ ${GNAT_VERSION} installed${NC}"
echo -e "${GREEN}✓ ${GPRBUILD_VERSION} installed${NC}"

# Install Alire (alr) from GitHub releases
echo -e "${YELLOW}Installing Alire (package manager)...${NC}"
ALR_VERSION=$(get_github_latest_version_or_empty "alire-project/alire")
if [ -z "$ALR_VERSION" ]; then
    echo -e "${YELLOW}⚠ Failed to resolve Alire version, skipping${NC}"
else
    ALR_URL="https://github.com/alire-project/alire/releases/download/v${ALR_VERSION}/alr-${ALR_VERSION}-bin-${ALR_ARCH}-linux.zip"
    if curl -fsSL --connect-timeout 10 --max-time 60 -o /tmp/alr.zip "$ALR_URL" && \
       sudo unzip -o /tmp/alr.zip -d /tmp/alr-extract && \
       sudo mv /tmp/alr-extract/bin/alr /usr/local/bin/alr && \
       sudo chmod +x /usr/local/bin/alr; then
        rm -rf /tmp/alr.zip /tmp/alr-extract
        echo -e "${GREEN}✓ Alire ${ALR_VERSION} installed${NC}"
    else
        rm -rf /tmp/alr.zip /tmp/alr-extract
        echo -e "${YELLOW}⚠ Alire install failed${NC}"
    fi
fi

print_success_banner "Ada environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Ada environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${GNAT_VERSION}"
echo "  - ${GPRBUILD_VERSION}"
echo ""
echo "Development tools:"
if command -v alr &>/dev/null; then
    echo "  - Alire (package manager)"
else
    echo "  - Alire (skipped — not available)"
fi
echo ""
