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

print_banner "Scala Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Scala Development Environment"
    echo "========================================="
}

# Environment variables
export SDKMAN_DIR="${SDKMAN_DIR:-/home/vscode/.cache/sdkman}"
export COURSIER_CACHE="${COURSIER_CACHE:-/home/vscode/.cache/coursier}"

# Check if SDKMAN is installed (requires Java feature)
if [ ! -d "$SDKMAN_DIR" ]; then
    echo -e "${RED}Error: SDKMAN not found. Please install the Java feature first.${NC}"
    exit 1
fi

# Source SDKMAN
source "$SDKMAN_DIR/bin/sdkman-init.sh"

# Install Scala via SDKMAN
echo -e "${YELLOW}Installing Scala...${NC}"
sdk install scala
SCALA_VERSION=$(scala -version 2>&1 | head -n 1)
echo -e "${GREEN}✓ ${SCALA_VERSION} installed${NC}"

# Install sbt via SDKMAN
echo -e "${YELLOW}Installing sbt...${NC}"
sdk install sbt
SBT_VERSION=$(sbt --version 2>&1 | grep "sbt script" | head -n 1 || echo "sbt installed")
echo -e "${GREEN}✓ ${SBT_VERSION}${NC}"

# Install Coursier (Scala artifact fetcher)
echo -e "${YELLOW}Installing Coursier...${NC}"
# Detect architecture for Coursier binary
ARCH=$(uname -m)
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    CS_ARCH="aarch64-pc-linux"
else
    CS_ARCH="x86_64-pc-linux"
fi
curl -fL "https://github.com/coursier/launchers/raw/master/cs-${CS_ARCH}.gz" | gzip -d > /tmp/cs
chmod +x /tmp/cs
sudo mv /tmp/cs /usr/local/bin/cs
echo -e "${GREEN}✓ Coursier installed${NC}"

# Install common Scala tools via Coursier — parallel
echo -e "${YELLOW}Installing Scala tools (scala-cli, metals, scalafmt)...${NC}"
(cs install scala-cli && echo -e "${GREEN}✓ Scala CLI installed${NC}") &
(cs install metals && echo -e "${GREEN}✓ Metals installed${NC}") &
(cs install scalafmt && echo -e "${GREEN}✓ scalafmt installed${NC}") &
wait

# Create cache directories
mkdir -p "$COURSIER_CACHE"
mkdir -p /home/vscode/.cache/sbt

# Add Coursier bin to PATH
COURSIER_BIN="$HOME/.local/share/coursier/bin"
if ! grep -q "COURSIER" /home/vscode/.zshrc 2>/dev/null; then
    echo "" >> /home/vscode/.zshrc
    echo "# Coursier" >> /home/vscode/.zshrc
    echo "export PATH=\"\$PATH:$COURSIER_BIN\"" >> /home/vscode/.zshrc
fi

print_success_banner "Scala environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Scala environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${SCALA_VERSION}"
echo "  - sbt (Scala Build Tool)"
echo "  - Coursier (artifact fetcher)"
echo "  - Scala CLI"
echo "  - Metals (LSP for IDE support)"
echo "  - scalafmt (formatter)"
echo ""
echo "Cache directories:"
echo "  - Coursier: $COURSIER_CACHE"
echo "  - sbt: /home/vscode/.cache/sbt"
echo ""
echo "Bazel integration:"
echo "  - Use rules_scala: https://github.com/bazelbuild/rules_scala"
echo ""
