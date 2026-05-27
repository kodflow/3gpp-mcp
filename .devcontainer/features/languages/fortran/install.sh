#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
    get_github_latest_version() {
        local repo="$1" version auth_args=()
        [[ -n "${GITHUB_TOKEN:-}" ]] && auth_args=(-H "Authorization: token ${GITHUB_TOKEN}")
        local attempt
        for attempt in 1 2 3; do
            version=$(curl -fsS --connect-timeout 5 --max-time 10 \
                "${auth_args[@]}" \
                "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
                | sed -n 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/p' | head -n 1)
            [[ -n "$version" ]] && break
            sleep $((attempt * 2))
        done
        if [[ -z "$version" ]]; then
            echo -e "${RED}✗ Failed to resolve latest version for ${repo}${NC}" >&2
            return 1
        fi
        echo "$version"
    }
    get_github_latest_version_or_empty() {
        get_github_latest_version "$1" 2>/dev/null || echo ""
    }
}

print_banner "Fortran Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Fortran Development Environment"
    echo "========================================="
}

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GH_ARCH="x86_64" ;;
    aarch64) GH_ARCH="aarch64" ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

# Install gfortran
echo -e "${YELLOW}Installing gfortran...${NC}"
sudo apt-get update && sudo apt-get install -y \
    gfortran \
    make \
    curl \
    python3-pip

GFORTRAN_VERSION=$(gfortran --version | head -n 1)
echo -e "${GREEN}✓ ${GFORTRAN_VERSION} installed${NC}"

# Install fprettify (formatter) via pip
echo -e "${YELLOW}Installing fprettify (formatter)...${NC}"
if pip3 install fprettify --break-system-packages 2>/dev/null || pip3 install fprettify; then
    echo -e "${GREEN}✓ fprettify $(fprettify --version 2>&1 | head -n 1) installed${NC}"
else
    echo -e "${YELLOW}⚠ fprettify installation failed${NC}"
fi

# Install fpm (Fortran Package Manager) from GitHub releases
echo -e "${YELLOW}Installing fpm (Fortran Package Manager)...${NC}"
FPM_VERSION=$(get_github_latest_version_or_empty "fortran-lang/fpm")
if [ -z "$FPM_VERSION" ]; then
    echo -e "${YELLOW}⚠ Failed to resolve fpm version, skipping${NC}"
else
    FPM_URL="https://github.com/fortran-lang/fpm/releases/download/v${FPM_VERSION}/fpm-${FPM_VERSION}-linux-${GH_ARCH}"
    if curl -fsSL --connect-timeout 10 --max-time 60 -o /tmp/fpm "$FPM_URL"; then
        sudo mv /tmp/fpm /usr/local/bin/fpm
        sudo chmod +x /usr/local/bin/fpm
        echo -e "${GREEN}✓ fpm ${FPM_VERSION} installed${NC}"
    else
        echo -e "${YELLOW}⚠ fpm download failed${NC}"
    fi
fi

print_success_banner "Fortran environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Fortran environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${GFORTRAN_VERSION}"
echo ""
echo "Development tools:"
echo "  - fprettify (formatter)"
if command -v fpm &>/dev/null; then
    echo "  - fpm (Fortran Package Manager)"
else
    echo "  - fpm (skipped — not available)"
fi
echo ""
