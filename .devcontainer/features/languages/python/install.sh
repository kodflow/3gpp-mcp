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

print_banner "Python Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Python Development Environment"
    echo "========================================="
}

# Environment variables
export PYTHON_VERSION="${VERSION:-3.14}"
export PIP_CACHE_DIR="${PIP_CACHE_DIR:-$HOME/.cache/pip}"
export PYENV_ROOT="${PYENV_ROOT:-$HOME/.cache/pyenv}"

# Install base dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    software-properties-common \
    curl \
    git

# ─────────────────────────────────────────────────────────────────────────────
# Install Python (prefer prebuilt from deadsnakes PPA, fallback to pyenv)
# ─────────────────────────────────────────────────────────────────────────────
PYTHON_INSTALLED=""

# Strategy 1: deadsnakes PPA (prebuilt, fast ~10s)
install_python_deadsnakes() {
    echo -e "${YELLOW}Trying deadsnakes PPA (prebuilt)...${NC}"

    # Add deadsnakes PPA
    if sudo add-apt-repository -y ppa:deadsnakes/ppa 2>/dev/null; then
        sudo apt-get update

        # Install Python core packages (no || true — we need the real exit code)
        if sudo apt-get install -y \
            "python${PYTHON_VERSION}" \
            "python${PYTHON_VERSION}-venv" \
            "python${PYTHON_VERSION}-dev" 2>/dev/null; then

            # Optional: distutils (removed in Python 3.12+)
            sudo apt-get install -y "python${PYTHON_VERSION}-distutils" 2>/dev/null || true

            # Verify the binary actually exists before claiming success
            if ! command -v "python${PYTHON_VERSION}" &>/dev/null; then
                echo -e "${YELLOW}⚠ Package installed but binary not found${NC}"
                return 1
            fi

            # Set as default python3
            sudo update-alternatives --install /usr/bin/python3 python3 "/usr/bin/python${PYTHON_VERSION}" 1 2>/dev/null || true
            sudo update-alternatives --install /usr/bin/python python "/usr/bin/python${PYTHON_VERSION}" 1 2>/dev/null || true

            PYTHON_INSTALLED=$("python${PYTHON_VERSION}" --version)
            echo -e "${GREEN}✓ ${PYTHON_INSTALLED} installed (prebuilt)${NC}"
            return 0
        fi
    fi

    return 1
}

# Strategy 2: pyenv (compile from source, slow ~2-5min)
install_python_pyenv() {
    echo -e "${YELLOW}Fallback to pyenv (compile from source)...${NC}"

    # Install pyenv build dependencies
    sudo apt-get install -y \
        build-essential \
        libssl-dev \
        zlib1g-dev \
        libbz2-dev \
        libreadline-dev \
        libsqlite3-dev \
        libncursesw5-dev \
        xz-utils \
        tk-dev \
        libxml2-dev \
        libxmlsec1-dev \
        libffi-dev \
        liblzma-dev

    # Install pyenv
    curl https://pyenv.run | bash

    # Setup pyenv
    export PATH="$PYENV_ROOT/bin:$PATH"
    eval "$(pyenv init -)"
    eval "$(pyenv virtualenv-init -)"

    # Install Python (latest stable of specified major.minor)
    LATEST_PYTHON=$(pyenv install --list | grep -E "^\s*${PYTHON_VERSION}\.[0-9]+$" | tail -1 | xargs)
    pyenv install "$LATEST_PYTHON"
    pyenv global "$LATEST_PYTHON"

    PYTHON_INSTALLED=$(python --version)
    echo -e "${GREEN}✓ ${PYTHON_INSTALLED} installed (compiled)${NC}"
    return 0
}

# Try prebuilt first, fallback to compile
if ! install_python_deadsnakes; then
    install_python_pyenv
fi

# Ensure pip is available and upgraded
echo -e "${YELLOW}Setting up pip...${NC}"

# Get the right python binary
if command -v "python${PYTHON_VERSION}" &>/dev/null; then
    PYTHON_BIN="python${PYTHON_VERSION}"
elif command -v python3 &>/dev/null; then
    PYTHON_BIN="python3"
else
    PYTHON_BIN="python"
fi

# Install/upgrade pip
# ensurepip is disabled on Debian/Ubuntu for deadsnakes Python — fall back to get-pip.py
if ! $PYTHON_BIN -m ensurepip --upgrade 2>/dev/null; then
    echo -e "${YELLOW}ensurepip unavailable, using get-pip.py...${NC}"
    curl -sSL https://bootstrap.pypa.io/get-pip.py | $PYTHON_BIN 2>/dev/null || true
fi
$PYTHON_BIN -m pip install --upgrade pip 2>/dev/null || \
$PYTHON_BIN -m pip install --break-system-packages --upgrade pip 2>/dev/null || true

PIP_VERSION=$($PYTHON_BIN -m pip --version)
echo -e "${GREEN}✓ ${PIP_VERSION}${NC}"

# Create cache directory
mkdir -p "$PIP_CACHE_DIR"

# ─────────────────────────────────────────────────────────────────────────────
# Install Python Development Tools — batched for speed
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Python development tools...${NC}"

# Install tools with appropriate flags
pip_install() {
    $PYTHON_BIN -m pip install --quiet "$@" 2>/dev/null || \
    $PYTHON_BIN -m pip install --quiet --break-system-packages "$@" 2>/dev/null || \
    echo -e "${YELLOW}⚠ Failed to install: $*${NC}"
}

# Quality, linting, type checking, security, testing — single batch
pip_install ruff pylint mypy bandit pytest pytest-cov

echo -e "${GREEN}✓ Python development tools installed${NC}"

# Setup shell integration for pyenv (if installed)
if [ -d "$PYENV_ROOT" ]; then
    # shellcheck disable=SC2016 # Intentional: variables must expand at runtime, not install time
    # Use the same default as the script for consistency
    PYENV_INIT='export PYENV_ROOT="${PYENV_ROOT:-$HOME/.cache/pyenv}"
export PATH="$PYENV_ROOT/bin:$PATH"
eval "$(pyenv init -)"'

    for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
        if [ -f "$rc_file" ] && ! grep -q "pyenv init" "$rc_file"; then
            echo "" >> "$rc_file"
            echo "# Pyenv initialization" >> "$rc_file"
            echo "$PYENV_INIT" >> "$rc_file"
        fi
    done
fi

print_success_banner "Python environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Python environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${PYTHON_INSTALLED}"
echo "  - pip (upgraded)"
echo ""
echo "Development tools:"
echo "  - ruff (linter/formatter)"
echo "  - pylint (linter)"
echo "  - mypy (type checker)"
echo "  - bandit (security scanner)"
echo "  - pytest + pytest-cov (testing)"
echo ""
echo "Cache directory:"
echo "  - pip: $PIP_CACHE_DIR"
echo ""
