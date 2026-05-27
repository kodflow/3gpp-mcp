#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
}

print_banner "VB.NET Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing VB.NET Development Environment"
    echo "========================================="
}

# Note: VB.NET shares the .NET SDK/runtime with C#.
# If the C# feature is also installed, this is a no-op for the SDK.

if command -v dotnet &> /dev/null; then
    echo -e "${YELLOW}Existing .NET SDK detected, skipping SDK install...${NC}"
else
    # Install prerequisites
    echo -e "${YELLOW}Installing prerequisites...${NC}"
    sudo apt-get update && sudo apt-get install -y \
        curl wget apt-transport-https ca-certificates

    # Add Microsoft package repository (detect Debian vs Ubuntu)
    echo -e "${YELLOW}Adding Microsoft package repository...${NC}"
    DISTRO_ID=$(. /etc/os-release && echo "$ID")
    DISTRO_VERSION=$(. /etc/os-release && echo "$VERSION_ID")
    if [[ "$DISTRO_ID" == "ubuntu" ]]; then
        REPO_URL="https://packages.microsoft.com/config/ubuntu/${DISTRO_VERSION}/packages-microsoft-prod.deb"
    else
        REPO_URL="https://packages.microsoft.com/config/debian/${DISTRO_VERSION}/packages-microsoft-prod.deb"
    fi
    wget -q --retry-connrefused --tries=3 "$REPO_URL" -O /tmp/packages-microsoft-prod.deb
    sudo dpkg -i /tmp/packages-microsoft-prod.deb
    rm -f /tmp/packages-microsoft-prod.deb

    # Install .NET SDK
    echo -e "${YELLOW}Installing .NET SDK...${NC}"
    sudo apt-get update && sudo apt-get install -y dotnet-sdk-9.0
fi

# Set up environment
export DOTNET_ROOT="/usr/share/dotnet"
export PATH="$DOTNET_ROOT:$PATH"

DOTNET_VERSION=$(dotnet --version)
echo -e "${GREEN}+ .NET SDK ${DOTNET_VERSION} installed${NC}"

print_success_banner "VB.NET environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}VB.NET environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - .NET SDK ${DOTNET_VERSION}"
echo "  - VB.NET compiler (vbc via dotnet build)"
echo "  - DOTNET_ROOT: $DOTNET_ROOT"
echo "  Note: Shares .NET runtime with C#."
echo ""
