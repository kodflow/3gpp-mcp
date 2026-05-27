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

print_banner "C# / .NET Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing C# / .NET Development Environment"
    echo "========================================="
}

# Cleanup on failure
cleanup() { rm -f /tmp/packages-microsoft-prod.deb; }
trap cleanup EXIT

# Detect architecture
ARCH=$(uname -m)
echo -e "${YELLOW}Detected architecture: ${ARCH}${NC}"

# Install prerequisites
echo -e "${YELLOW}Installing prerequisites...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    wget \
    apt-transport-https \
    ca-certificates

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

# Set up environment
export DOTNET_ROOT="/usr/share/dotnet"
export PATH="$DOTNET_ROOT:/home/vscode/.dotnet/tools:$PATH"

# Create tools directory
mkdir -p /home/vscode/.dotnet/tools

# Install global dotnet tools
echo -e "${YELLOW}Installing dotnet global tools...${NC}"

dotnet tool install -g dotnet-format 2>/dev/null && \
    echo -e "${GREEN}+ dotnet-format installed${NC}" || \
    echo -e "${YELLOW}! dotnet-format may be bundled with SDK${NC}"

dotnet tool install -g dotnet-outdated-tool && \
    echo -e "${GREEN}+ dotnet-outdated installed${NC}" || \
    echo -e "${YELLOW}! dotnet-outdated failed to install${NC}"

print_success_banner "C# / .NET environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}C# / .NET environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - dotnet $(dotnet --version)"
echo ""
echo "Global tools:"
dotnet tool list -g 2>/dev/null || echo "  (none)"
echo ""
echo "Environment:"
echo "  - DOTNET_ROOT: $DOTNET_ROOT"
echo "  - Tools path:  /home/vscode/.dotnet/tools"
echo ""
