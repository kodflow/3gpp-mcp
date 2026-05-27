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

print_banner "COBOL Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing COBOL Development Environment"
    echo "========================================="
}

# Install GnuCOBOL
echo -e "${YELLOW}Installing GnuCOBOL...${NC}"
sudo apt-get update && sudo apt-get install -y gnucobol

COBC_VERSION=$(cobc --version | head -n 1)
echo -e "${GREEN}+ ${COBC_VERSION} installed${NC}"

print_success_banner "COBOL environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}COBOL environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${COBC_VERSION}"
echo ""
