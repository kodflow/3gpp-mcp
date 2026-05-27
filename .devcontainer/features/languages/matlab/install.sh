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

print_banner "MATLAB/Octave Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing MATLAB/Octave Development Environment"
    echo "========================================="
}

# Install GNU Octave
echo -e "${YELLOW}Installing GNU Octave...${NC}"
sudo apt-get update && sudo apt-get install -y octave

# Optionally install octave-signal if available
echo -e "${YELLOW}Attempting to install octave-signal...${NC}"
if sudo apt-get install -y octave-signal 2>/dev/null; then
    echo -e "${GREEN}octave-signal installed${NC}"
else
    echo -e "${YELLOW}octave-signal not available, skipping${NC}"
fi

OCTAVE_VERSION=$(octave --version | head -n 1)

print_success_banner "MATLAB/Octave environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}MATLAB/Octave environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${OCTAVE_VERSION}"
echo ""
