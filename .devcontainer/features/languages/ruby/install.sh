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

print_banner "Ruby Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Ruby Development Environment"
    echo "========================================="
}

# Environment variables
export RUBY_VERSION="${RUBY_VERSION:-4.0}"
export GEM_HOME="${GEM_HOME:-$HOME/.cache/gems}"
export BUNDLE_PATH="${BUNDLE_PATH:-$HOME/.cache/bundle}"
export RBENV_ROOT="${RBENV_ROOT:-$HOME/.cache/rbenv}"

# Install base dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    software-properties-common \
    curl \
    git

# ─────────────────────────────────────────────────────────────────────────────
# Install Ruby (prefer prebuilt from Brightbox PPA, fallback to rbenv compile)
# ─────────────────────────────────────────────────────────────────────────────
RUBY_INSTALLED=""

# Strategy 1: Brightbox PPA (prebuilt, fast ~10s)
install_ruby_brightbox() {
    echo -e "${YELLOW}Trying Brightbox PPA (prebuilt)...${NC}"

    # Add Brightbox PPA
    if sudo add-apt-repository -y ppa:brightbox/ruby-ng 2>/dev/null; then
        sudo apt-get update

        # Ruby version format: 3.3 -> ruby3.3
        local APT_RUBY_PKG="ruby${RUBY_VERSION}"
        local APT_RUBY_DEV="ruby${RUBY_VERSION}-dev"

        if sudo apt-get install -y "$APT_RUBY_PKG" "$APT_RUBY_DEV" 2>/dev/null; then
            RUBY_INSTALLED=$(ruby --version)
            echo -e "${GREEN}✓ ${RUBY_INSTALLED} installed (prebuilt)${NC}"
            return 0
        fi
    fi

    return 1
}

# Strategy 2: rbenv (compile from source, slow ~8-15min)
install_ruby_rbenv() {
    echo -e "${YELLOW}Fallback to rbenv (compile from source)...${NC}"

    # Install rbenv build dependencies
    sudo apt-get install -y \
        build-essential \
        libssl-dev \
        libreadline-dev \
        zlib1g-dev \
        autoconf \
        bison \
        libyaml-dev \
        libncurses5-dev \
        libffi-dev \
        libgdbm-dev

    # Install rbenv
    git clone https://github.com/rbenv/rbenv.git "$RBENV_ROOT"
    git clone https://github.com/rbenv/ruby-build.git "$RBENV_ROOT/plugins/ruby-build"

    # Setup rbenv
    export PATH="$RBENV_ROOT/bin:$PATH"
    eval "$(rbenv init -)"

    # Install Ruby (latest stable of specified major.minor)
    LATEST_RUBY=$(rbenv install --list | grep -E "^\s*${RUBY_VERSION}\.[0-9]+$" | tail -1 | xargs)
    rbenv install "$LATEST_RUBY"
    rbenv global "$LATEST_RUBY"

    RUBY_INSTALLED=$(ruby --version)
    echo -e "${GREEN}✓ ${RUBY_INSTALLED} installed (compiled)${NC}"
    return 0
}

# Try prebuilt first, fallback to compile
if ! install_ruby_brightbox; then
    install_ruby_rbenv
fi

# Update RubyGems
echo -e "${YELLOW}Updating RubyGems...${NC}"
gem update --system 2>/dev/null || sudo gem update --system 2>/dev/null || true
GEM_VERSION=$(gem --version)
echo -e "${GREEN}✓ RubyGems ${GEM_VERSION} installed${NC}"

# Install Bundler
echo -e "${YELLOW}Installing Bundler...${NC}"
gem install bundler 2>/dev/null || sudo gem install bundler 2>/dev/null
if [ -d "$RBENV_ROOT" ]; then rbenv rehash 2>/dev/null || true; fi
BUNDLER_VERSION=$(bundler --version)
echo -e "${GREEN}✓ ${BUNDLER_VERSION} installed${NC}"

# Create cache directories
mkdir -p "$GEM_HOME"
mkdir -p "$BUNDLE_PATH"

# ─────────────────────────────────────────────────────────────────────────────
# Install Ruby Development Tools — batched for speed
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Ruby development tools...${NC}"

# Batch install all gems in a single call
if gem install rubocop simplecov solargraph ruby-lsp 2>/dev/null || \
   sudo gem install rubocop simplecov solargraph ruby-lsp 2>/dev/null; then
    if [ -d "$RBENV_ROOT" ]; then rbenv rehash 2>/dev/null || true; fi
    echo -e "${GREEN}✓ rubocop, simplecov, solargraph, ruby-lsp installed${NC}"
else
    echo -e "${YELLOW}⚠ Some gems failed to install${NC}"
fi

echo -e "${GREEN}✓ Ruby development tools installed${NC}"

# Setup shell integration for rbenv (if installed)
if [ -d "$RBENV_ROOT" ]; then
    # shellcheck disable=SC2016 # Intentional: $HOME must expand at runtime, not install time
    RBENV_INIT='export RBENV_ROOT="$HOME/.cache/rbenv"
export PATH="$RBENV_ROOT/bin:$PATH"
eval "$(rbenv init -)"'

    for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
        if [ -f "$rc_file" ] && ! grep -q "rbenv init" "$rc_file"; then
            {
                echo ""
                echo "# Rbenv initialization"
                echo "$RBENV_INIT"
            } >> "$rc_file"
        fi
    done
fi

print_success_banner "Ruby environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Ruby environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${RUBY_INSTALLED}"
echo "  - RubyGems ${GEM_VERSION}"
echo "  - ${BUNDLER_VERSION}"
echo ""
echo "Development tools:"
echo "  - RuboCop (linter/formatter)"
echo "  - SimpleCov (coverage)"
echo "  - Solargraph (language server)"
echo "  - Ruby LSP (language server)"
echo ""
echo "Cache directories:"
echo "  - gems: $GEM_HOME"
echo "  - bundler: $BUNDLE_PATH"
echo ""
