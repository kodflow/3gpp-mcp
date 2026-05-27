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

print_banner "Assembly Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Assembly Development Environment"
    echo "========================================="
}

# Install assembly tools
echo -e "${YELLOW}Installing NASM, binutils, and GDB...${NC}"
sudo apt-get update && sudo apt-get install -y nasm binutils gdb

NASM_VERSION=$(nasm --version)
GDB_VERSION=$(gdb --version | head -n 1)

print_success_banner "Assembly environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Assembly environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${NASM_VERSION}"
echo "  - binutils (as, ld, objdump, readelf)"
echo "  - ${GDB_VERSION}"
echo ""
