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

print_banner "Pascal Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Pascal Development Environment"
    echo "========================================="
}

# Install Free Pascal
echo -e "${YELLOW}Installing Free Pascal...${NC}"
sudo apt-get update && sudo apt-get install -y fpc fp-units-base

FPC_VERSION=$(fpc -iV 2>/dev/null || echo "unknown")
echo -e "${GREEN}+ Free Pascal ${FPC_VERSION} installed${NC}"

print_success_banner "Pascal environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Pascal environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - Free Pascal ${FPC_VERSION}"
echo "  - fp-units-base"
echo ""
