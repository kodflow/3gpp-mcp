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

print_banner "Perl Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Perl Development Environment"
    echo "========================================="
}

# Install Perl and cpanminus
echo -e "${YELLOW}Installing Perl and cpanminus...${NC}"
sudo apt-get update && sudo apt-get install -y \
    perl \
    cpanminus \
    make \
    gcc

PERL_INSTALLED=$(perl --version | head -n 2 | tail -n 1)
echo -e "${GREEN}${PERL_INSTALLED}${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install Perl development modules
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Perl::Tidy...${NC}"
if sudo cpanm --notest Perl::Tidy 2>/dev/null; then
    echo -e "${GREEN}Perl::Tidy installed${NC}"
else
    echo -e "${RED}Perl::Tidy failed to install${NC}"
fi

echo -e "${YELLOW}Installing Perl::Critic...${NC}"
if sudo cpanm --notest Perl::Critic 2>/dev/null; then
    echo -e "${GREEN}Perl::Critic installed${NC}"
else
    echo -e "${RED}Perl::Critic failed to install${NC}"
fi

print_success_banner "Perl environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Perl environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${PERL_INSTALLED}"
echo "  - cpanminus (package manager)"
echo ""
echo "Development modules:"
echo "  - Perl::Tidy (formatter)"
echo "  - Perl::Critic (linter)"
echo ""
