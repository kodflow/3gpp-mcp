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

print_banner "C/C++ Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing C/C++ Development Environment"
    echo "========================================="
}

# Install C/C++ toolchain
echo -e "${YELLOW}Installing C/C++ toolchain...${NC}"
sudo apt-get update && sudo apt-get install -y \
    build-essential \
    gcc \
    g++ \
    gdb \
    clang \
    make \
    cmake \
    git \
    curl

GCC_VERSION=$(gcc --version | head -n 1)
CLANG_VERSION=$(clang --version | head -n 1)
CMAKE_VERSION=$(cmake --version | head -n 1)
MAKE_VERSION=$(make --version | head -n 1)

echo -e "${GREEN}✓ ${GCC_VERSION} installed${NC}"
echo -e "${GREEN}✓ ${CLANG_VERSION} installed${NC}"
echo -e "${GREEN}✓ ${CMAKE_VERSION} installed${NC}"
echo -e "${GREEN}✓ ${MAKE_VERSION} installed${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install C++ Development Tools (latest versions)
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing C++ development tools...${NC}"

# clang-format (formatter - mandatory per RULES.md)
echo -e "${YELLOW}Installing clang-format...${NC}"
sudo apt-get install -y clang-format
echo -e "${GREEN}✓ clang-format installed${NC}"

# clang-tidy (linter - mandatory per RULES.md)
echo -e "${YELLOW}Installing clang-tidy...${NC}"
sudo apt-get install -y clang-tidy
echo -e "${GREEN}✓ clang-tidy installed${NC}"

# ccache (compilation cache)
echo -e "${YELLOW}Installing ccache...${NC}"
sudo apt-get install -y ccache
echo -e "${GREEN}✓ ccache installed${NC}"

# ninja (fast build system)
echo -e "${YELLOW}Installing ninja-build...${NC}"
sudo apt-get install -y ninja-build
NINJA_VERSION=$(ninja --version)
echo -e "${GREEN}✓ ninja ${NINJA_VERSION} installed${NC}"

# Google Test (testing framework per RULES.md)
# libgtest-dev only provides sources - we need to compile the libraries
echo -e "${YELLOW}Installing Google Test...${NC}"
sudo apt-get install -y libgtest-dev

# Build Google Test libraries from source
if [ -d "/usr/src/gtest" ] || [ -d "/usr/src/googletest" ]; then
    GTEST_SRC=$([ -d "/usr/src/googletest" ] && echo "/usr/src/googletest" || echo "/usr/src/gtest")
    cd "$GTEST_SRC"
    sudo cmake -B build -DCMAKE_BUILD_TYPE=Release .
    sudo cmake --build build --parallel
    # Copy libraries with proper error handling
    if sudo cp build/lib/*.a /usr/lib/ 2>/dev/null; then
        echo -e "${GREEN}✓ Google Test libraries copied from build/lib/${NC}"
    elif sudo cp build/*.a /usr/lib/ 2>/dev/null; then
        echo -e "${GREEN}✓ Google Test libraries copied from build/${NC}"
    else
        echo -e "${RED}✗ Failed to copy Google Test libraries${NC}"
        echo -e "${YELLOW}  Check build output for errors${NC}"
        exit 1
    fi
    cd - > /dev/null
    echo -e "${GREEN}✓ Google Test installed (headers + libraries)${NC}"
else
    echo -e "${RED}✗ Google Test sources not found - required for linking${NC}"
    exit 1
fi

# cppcheck (static analysis)
echo -e "${YELLOW}Installing cppcheck...${NC}"
sudo apt-get install -y cppcheck
echo -e "${GREEN}✓ cppcheck installed${NC}"

# valgrind (memory checker)
echo -e "${YELLOW}Installing valgrind...${NC}"
sudo apt-get install -y valgrind
echo -e "${GREEN}✓ valgrind installed${NC}"

echo -e "${GREEN}✓ C++ development tools installed${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install Emscripten SDK (WebAssembly Compiler)
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Emscripten SDK (WebAssembly compiler)...${NC}"

# Install Python if not available (required by emsdk)
if ! command -v python3 &> /dev/null; then
    echo -e "${YELLOW}Installing Python (required by emsdk)...${NC}"
    sudo apt-get install -y python3
fi

# Clone and install emsdk
EMSDK_DIR="/opt/emsdk"
if [ ! -d "$EMSDK_DIR" ]; then
    sudo git clone https://github.com/emscripten-core/emsdk.git "$EMSDK_DIR" || {
        echo -e "${RED}✗ Failed to clone Emscripten SDK${NC}"
        exit 1
    }
    # Use devcontainer user (REMOTE_USER), fallback to vscode
    TARGET_USER="${_REMOTE_USER:-vscode}"
    sudo chown -R "${TARGET_USER}:${TARGET_USER}" "$EMSDK_DIR"
fi

cd "$EMSDK_DIR"

# Install and activate latest Emscripten
./emsdk install latest || {
    echo -e "${RED}✗ Failed to install Emscripten${NC}"
    exit 1
}
./emsdk activate latest || {
    echo -e "${RED}✗ Failed to activate Emscripten${NC}"
    exit 1
}

# Source emsdk environment
source "$EMSDK_DIR/emsdk_env.sh"

# Add to shell profiles for persistence
# Note: 2>/dev/null suppresses emsdk output during shell startup (cleaner prompt)
EMSDK_ENV_LINE="[ -s \"$EMSDK_DIR/emsdk_env.sh\" ] && source \"$EMSDK_DIR/emsdk_env.sh\" 2>/dev/null"
for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
    if [ -f "$rc_file" ] && ! grep -q "emsdk_env" "$rc_file"; then
        echo "" >> "$rc_file"
        echo "# Emscripten SDK" >> "$rc_file"
        echo "$EMSDK_ENV_LINE" >> "$rc_file"
    fi
done

cd - > /dev/null

# Verify installation
if command -v emcc &> /dev/null; then
    EMCC_VERSION=$(emcc --version 2>/dev/null | head -n 1)
    echo -e "${GREEN}✓ ${EMCC_VERSION}${NC}"
else
    echo -e "${GREEN}✓ Emscripten installed (source emsdk_env.sh to use)${NC}"
fi

print_success_banner "C/C++ environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}C/C++ environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${GCC_VERSION}"
echo "  - ${CLANG_VERSION}"
echo "  - ${CMAKE_VERSION}"
echo "  - ${MAKE_VERSION}"
echo "  - gdb (debugger)"
echo ""
echo "Development tools:"
echo "  - clang-format (formatter)"
echo "  - clang-tidy (linter)"
echo "  - ccache (compilation cache)"
echo "  - ninja ${NINJA_VERSION} (build system)"
echo "  - Google Test (testing)"
echo "  - cppcheck (static analysis)"
echo "  - valgrind (memory checker)"
echo ""
echo "WASM tools:"
echo "  - emscripten (C/C++ to WASM compiler)"
echo "  - emcc, em++ (compiler frontends)"
echo ""
