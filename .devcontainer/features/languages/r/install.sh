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

print_banner "R Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing R Development Environment"
    echo "========================================="
}

# Install R and development headers
echo -e "${YELLOW}Installing R base and development packages...${NC}"
sudo apt-get update && sudo apt-get install -y \
    r-base \
    r-base-dev \
    libcurl4-openssl-dev \
    libssl-dev \
    libxml2-dev \
    libfontconfig1-dev \
    libharfbuzz-dev \
    libfribidi-dev

R_INSTALLED=$(R --version | head -n 1)
echo -e "${GREEN}${R_INSTALLED} installed${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install R development packages
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing R packages (lintr, styler, testthat)...${NC}"

install_r_package() {
    local pkg=$1
    echo -e "${YELLOW}Installing ${pkg}...${NC}"
    if Rscript -e "install.packages('${pkg}', repos='https://cloud.r-project.org', quiet=TRUE)"; then
        echo -e "${GREEN}${pkg} installed${NC}"
    else
        echo -e "${YELLOW}⚠ ${pkg} failed to install (non-blocking)${NC}"
    fi
}

install_r_package "lintr"
install_r_package "styler"
install_r_package "testthat"

print_success_banner "R environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}R environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${R_INSTALLED}"
echo ""
echo "Development packages:"
echo "  - lintr (linter)"
echo "  - styler (formatter)"
echo "  - testthat (testing)"
echo ""
echo "R_HOME: /usr/lib/R"
echo ""
