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

print_banner "Elixir Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Elixir Development Environment"
    echo "========================================="
}

# Environment variables (can be overridden)
export ERLANG_VERSION="${ERLANG_VERSION:-28}"
export ELIXIR_VERSION="${ELIXIR_VERSION:-1.19.5}"
export MIX_HOME="${MIX_HOME:-/home/vscode/.cache/mix}"
export HEX_HOME="${HEX_HOME:-/home/vscode/.cache/hex}"
export ASDF_DATA_DIR="${ASDF_DATA_DIR:-/home/vscode/.cache/asdf}"

# Resolve latest asdf version for fallback installations
ASDF_LATEST=""
_asdf_auth=()
[[ -n "${GITHUB_TOKEN:-}" ]] && _asdf_auth=(-H "Authorization: token ${GITHUB_TOKEN}")
for _attempt in 1 2 3; do
    ASDF_LATEST=$(curl -fsS --connect-timeout 5 --max-time 10 \
        "${_asdf_auth[@]}" \
        "https://api.github.com/repos/asdf-vm/asdf/releases/latest" 2>/dev/null \
        | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4) || true
    [[ -n "$ASDF_LATEST" ]] && break
    sleep $((_attempt * 2))
done
if [ -z "$ASDF_LATEST" ]; then
    echo -e "${RED}✗ Failed to resolve latest asdf version after retries${NC}"
    exit 1
fi

# Install base dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    wget \
    curl \
    git \
    unzip \
    build-essential \
    libncurses5-dev \
    libssl-dev

# ─────────────────────────────────────────────────────────────────────────────
# Install Erlang (prefer erlang-solutions prebuilt, fallback to asdf compile)
# ─────────────────────────────────────────────────────────────────────────────
ERLANG_INSTALLED=""

# Strategy 1: erlang-solutions (prebuilt, fast ~30s)
install_erlang_prebuilt() {
    echo -e "${YELLOW}Trying erlang-solutions packages (prebuilt)...${NC}"

    # Download and install erlang-solutions repo
    local DEB_FILE="/tmp/erlang-solutions.deb"
    if curl -fsSL --connect-timeout 10 --max-time 60 \
        "https://packages.erlang-solutions.com/erlang-solutions_2.0_all.deb" -o "$DEB_FILE"; then
        # Install the .deb package, handle dependencies
        if ! sudo dpkg -i "$DEB_FILE"; then
            echo -e "${YELLOW}Fixing dpkg dependencies...${NC}"
            if ! sudo apt-get install -f -y; then
                echo -e "${YELLOW}⚠ Could not fix dependencies automatically${NC}"
            fi
        fi
        rm -f "$DEB_FILE"
        sudo apt-get update

        # Install Erlang (esl-erlang includes everything)
        if sudo apt-get install -y esl-erlang 2>/dev/null; then
            ERLANG_INSTALLED=$(erl -eval 'erlang:display(erlang:system_info(otp_release)), halt().' -noshell 2>/dev/null | tr -d '"')
            echo -e "${GREEN}✓ Erlang/OTP ${ERLANG_INSTALLED} installed (prebuilt)${NC}"
            return 0
        fi
    fi

    return 1
}

# Strategy 2: asdf (compile from source, slow ~15-25min)
install_erlang_asdf() {
    echo -e "${YELLOW}Fallback to asdf (compile from source)...${NC}"

    # Install additional build dependencies
    sudo apt-get install -y \
        autoconf \
        m4 \
        libssh-dev

    # Install asdf if not present
    if [ ! -d "$ASDF_DATA_DIR" ]; then
        git clone https://github.com/asdf-vm/asdf.git "$ASDF_DATA_DIR" --branch "${ASDF_LATEST}"
    fi

    # shellcheck source=/dev/null
    source "$ASDF_DATA_DIR/asdf.sh"

    # Add erlang plugin
    asdf plugin add erlang https://github.com/asdf-vm/asdf-erlang.git 2>/dev/null || true

    # Install Erlang (find latest matching version)
    local ERLANG_FULL_VERSION
    ERLANG_FULL_VERSION=$(asdf list all erlang | grep "^${ERLANG_VERSION}" | tail -1)
    asdf install erlang "$ERLANG_FULL_VERSION"
    asdf global erlang "$ERLANG_FULL_VERSION"

    ERLANG_INSTALLED=$(erl -eval 'erlang:display(erlang:system_info(otp_release)), halt().' -noshell 2>/dev/null | tr -d '"')
    echo -e "${GREEN}✓ Erlang/OTP ${ERLANG_INSTALLED} installed (compiled)${NC}"
    return 0
}

# Try prebuilt first, fallback to compile
if ! install_erlang_prebuilt; then
    install_erlang_asdf
fi

# ─────────────────────────────────────────────────────────────────────────────
# Install Elixir (prefer GitHub releases, fallback to asdf)
# ─────────────────────────────────────────────────────────────────────────────
ELIXIR_INSTALLED=""

# Strategy 1: GitHub releases (prebuilt, fast ~10s)
install_elixir_prebuilt() {
    echo -e "${YELLOW}Trying Elixir GitHub releases (prebuilt)...${NC}"

    # Detect OTP major version for matching prebuilt
    local OTP_MAJOR
    OTP_MAJOR=$(erl -eval 'erlang:display(erlang:system_info(otp_release)), halt().' -noshell 2>/dev/null | tr -d '"')

    # Download Elixir prebuilt for this OTP version
    local ELIXIR_ZIP="/tmp/elixir.zip"
    local ELIXIR_URL="https://github.com/elixir-lang/elixir/releases/download/v${ELIXIR_VERSION}/elixir-otp-${OTP_MAJOR}.zip"

    if curl -fsSL "$ELIXIR_URL" -o "$ELIXIR_ZIP" 2>/dev/null; then
        sudo mkdir -p /usr/local/elixir
        sudo unzip -o "$ELIXIR_ZIP" -d /usr/local/elixir
        rm -f "$ELIXIR_ZIP"

        # Add to PATH
        export PATH="/usr/local/elixir/bin:$PATH"

        # Verify
        if command -v elixir &>/dev/null; then
            ELIXIR_INSTALLED=$(elixir --version | grep "Elixir" | head -n 1)
            echo -e "${GREEN}✓ ${ELIXIR_INSTALLED} installed (prebuilt)${NC}"

            # Add to system profile
            # shellcheck disable=SC2016 # Intentional: $PATH must expand at runtime
            echo 'export PATH="/usr/local/elixir/bin:$PATH"' | sudo tee /etc/profile.d/elixir.sh >/dev/null
            return 0
        fi
    fi

    return 1
}

# Strategy 2: asdf (compile from source, slow ~2-5min)
install_elixir_asdf() {
    echo -e "${YELLOW}Fallback to asdf for Elixir...${NC}"

    # Install asdf if not present
    if [ ! -d "$ASDF_DATA_DIR" ]; then
        git clone https://github.com/asdf-vm/asdf.git "$ASDF_DATA_DIR" --branch "${ASDF_LATEST}"
    fi

    # shellcheck source=/dev/null
    source "$ASDF_DATA_DIR/asdf.sh"

    # Add elixir plugin
    asdf plugin add elixir https://github.com/asdf-vm/asdf-elixir.git 2>/dev/null || true

    # Install Elixir with OTP version suffix
    local OTP_MAJOR
    OTP_MAJOR=$(erl -eval 'erlang:display(erlang:system_info(otp_release)), halt().' -noshell 2>/dev/null | tr -d '"')
    local ELIXIR_FULL="${ELIXIR_VERSION}-otp-${OTP_MAJOR}"

    asdf install elixir "$ELIXIR_FULL"
    asdf global elixir "$ELIXIR_FULL"

    ELIXIR_INSTALLED=$(elixir --version | grep "Elixir" | head -n 1)
    echo -e "${GREEN}✓ ${ELIXIR_INSTALLED} installed (compiled)${NC}"
    return 0
}

# Try prebuilt first, fallback to compile
if ! install_elixir_prebuilt; then
    install_elixir_asdf
fi

# Install Hex (package manager)
echo -e "${YELLOW}Installing Hex...${NC}"
mix local.hex --force
echo -e "${GREEN}✓ Hex installed${NC}"

# Install Rebar3 (build tool)
echo -e "${YELLOW}Installing Rebar3...${NC}"
mix local.rebar --force
echo -e "${GREEN}✓ Rebar3 installed${NC}"

# Create cache directories
mkdir -p "$MIX_HOME"
mkdir -p "$HEX_HOME"

# ─────────────────────────────────────────────────────────────────────────────
# Install Elixir Development Tools (latest versions)
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Elixir development tools...${NC}"

# Install Credo globally as an archive (per RULES.md)
echo -e "${YELLOW}Installing Credo...${NC}"
mix archive.install hex credo --force 2>/dev/null || echo -e "${YELLOW}⚠ Credo requires project context${NC}"
echo -e "${GREEN}✓ Credo setup ready${NC}"

# Install Dialyxir globally as an archive (per RULES.md)
echo -e "${YELLOW}Installing Dialyxir...${NC}"
mix archive.install hex dialyxir --force 2>/dev/null || echo -e "${YELLOW}⚠ Dialyxir requires project context${NC}"
echo -e "${GREEN}✓ Dialyxir setup ready${NC}"

# Install Elixir LS (Language Server)
echo -e "${YELLOW}Installing Elixir LS...${NC}"
mix archive.install hex elixir_ls --force 2>/dev/null || echo -e "${YELLOW}⚠ Elixir LS requires project context${NC}"
echo -e "${GREEN}✓ Elixir LS setup ready${NC}"

echo -e "${GREEN}✓ Elixir development tools installed${NC}"

# Setup shell integration for asdf (if installed)
if [ -d "$ASDF_DATA_DIR" ]; then
    # shellcheck disable=SC2016 # Intentional: $HOME must expand at runtime, not install time
    ASDF_INIT='. "$HOME/.cache/asdf/asdf.sh"'

    for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
        if [ -f "$rc_file" ] && ! grep -q "asdf.sh" "$rc_file"; then
            {
                echo ""
                echo "# asdf initialization"
                echo "$ASDF_INIT"
            } >> "$rc_file"
        fi
    done
fi

# Note about project-level installation
echo -e "${YELLOW}Note: For full functionality, add to your mix.exs:${NC}"
echo '  {:credo, "~> 1.7", only: [:dev, :test], runtime: false}'
echo '  {:dialyxir, "~> 1.4", only: [:dev, :test], runtime: false}'

print_success_banner "Elixir environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Elixir environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - Erlang/OTP ${ERLANG_INSTALLED:-$ERLANG_VERSION}"
echo "  - ${ELIXIR_INSTALLED:-Elixir $ELIXIR_VERSION}"
echo "  - Hex (package manager)"
echo "  - Rebar3 (build tool)"
echo ""
echo "Development tools:"
echo "  - Credo (linter)"
echo "  - Dialyxir (type checking)"
echo "  - Elixir LS (language server)"
echo ""
echo "Cache directories:"
echo "  - Mix: $MIX_HOME"
echo "  - Hex: $HEX_HOME"
echo ""
