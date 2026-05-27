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

print_banner "C Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing C Development Environment"
    echo "========================================="
}

# Detect architecture
ARCH=$(uname -m)
echo -e "${YELLOW}Detected architecture: ${ARCH}${NC}"

# Install C development packages
# Support version parameter from devcontainer-feature.json (GCC version)
GCC_VERSION="${VERSION:-latest}"
echo -e "${YELLOW}Installing C compilers and development tools...${NC}"
if [ "$GCC_VERSION" != "latest" ]; then
    echo -e "${YELLOW}Requested GCC version: ${GCC_VERSION}${NC}"
    GCC_PKG="gcc-${GCC_VERSION}"
else
    GCC_PKG="gcc"
fi
sudo apt-get update && sudo apt-get install -y \
    ${GCC_PKG} \
    clang \
    clang-format \
    clang-tidy \
    valgrind \
    gdb \
    cmake \
    make \
    pkg-config \
    build-essential

print_success_banner "C environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}C environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - gcc          $(gcc --version | head -n 1)"
echo "  - clang        $(clang --version | head -n 1)"
echo "  - clang-format $(clang-format --version)"
echo "  - clang-tidy   $(clang-tidy --version | head -n 1)"
echo "  - valgrind     $(valgrind --version)"
echo "  - gdb          $(gdb --version | head -n 1)"
echo "  - cmake        $(cmake --version | head -n 1)"
echo "  - make         $(make --version | head -n 1)"
echo "  - pkg-config   $(pkg-config --version)"
echo ""
