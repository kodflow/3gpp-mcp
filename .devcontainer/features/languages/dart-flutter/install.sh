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

print_banner "Dart/Flutter Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Dart/Flutter Development Environment"
    echo "========================================="
}

# Environment variables
export FLUTTER_ROOT="${FLUTTER_ROOT:-/home/vscode/.cache/flutter}"
export PUB_CACHE="${PUB_CACHE:-/home/vscode/.cache/pub-cache}"

# Install dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    git \
    unzip \
    xz-utils \
    zip

# Install Flutter (includes Dart)
echo -e "${YELLOW}Installing Flutter...${NC}"

# Clone with retry
MAX_RETRIES=3
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if git clone https://github.com/flutter/flutter.git -b stable "$FLUTTER_ROOT"; then
        break
    fi
    RETRY_COUNT=$((RETRY_COUNT + 1))
    if [ $RETRY_COUNT -lt $MAX_RETRIES ]; then
        echo -e "${YELLOW}Git clone failed, retrying (attempt $((RETRY_COUNT + 1))/$MAX_RETRIES)...${NC}"
        rm -rf "$FLUTTER_ROOT"
        sleep 5
    else
        echo -e "${RED}Failed to clone Flutter repository after $MAX_RETRIES attempts${NC}"
        exit 1
    fi
done

# Setup Flutter
export PATH="$FLUTTER_ROOT/bin:$PATH"

# Run flutter doctor to download dependencies
flutter doctor

FLUTTER_VERSION=$(flutter --version | head -n 1)
DART_VERSION=$(dart --version 2>&1)
echo -e "${GREEN}✓ ${FLUTTER_VERSION} installed${NC}"
echo -e "${GREEN}✓ ${DART_VERSION} installed${NC}"

# Create cache directories
mkdir -p "$PUB_CACHE"

# ─────────────────────────────────────────────────────────────────────────────
# Install Dart/Flutter Development Tools (latest versions)
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Dart/Flutter development tools...${NC}"

# Track failed tools
FAILED_TOOLS=()

# Install DCM, Very Good CLI, and Melos in parallel
(
    echo -e "${YELLOW}Installing DCM...${NC}"
    if dart pub global activate dcm; then
        echo -e "${GREEN}✓ DCM installed${NC}"
    else
        echo -e "${YELLOW}⚠ DCM failed (may require license for some features)${NC}"
    fi
) &
DCM_PID=$!

(
    echo -e "${YELLOW}Installing Very Good CLI...${NC}"
    if dart pub global activate very_good_cli; then
        echo -e "${GREEN}✓ Very Good CLI installed${NC}"
    else
        echo -e "${RED}✗ Very Good CLI failed to install${NC}"
    fi
) &
VGC_PID=$!

(
    echo -e "${YELLOW}Installing Melos...${NC}"
    if dart pub global activate melos; then
        echo -e "${GREEN}✓ Melos installed${NC}"
    else
        echo -e "${RED}✗ Melos failed to install${NC}"
    fi
) &
MELOS_PID=$!

wait "$DCM_PID" 2>/dev/null || FAILED_TOOLS+=("dcm")
wait "$VGC_PID" 2>/dev/null || FAILED_TOOLS+=("very_good_cli")
wait "$MELOS_PID" 2>/dev/null || FAILED_TOOLS+=("melos")

# dart_style (formatter - part of SDK but ensure global)
echo -e "${YELLOW}Verifying dart format...${NC}"
dart format --version
echo -e "${GREEN}✓ dart format available${NC}"

# Add pub global bin to PATH (both .bashrc and .zshrc for consistency)
PUB_BIN="$PUB_CACHE/bin"
for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
    if [ -f "$rc_file" ] && ! grep -q "Dart pub global binaries" "$rc_file" 2>/dev/null; then
        echo "" >> "$rc_file"
        echo "# Dart pub global binaries" >> "$rc_file"
        echo "export PATH=\"\$PATH:$PUB_BIN\"" >> "$rc_file"
    fi
done

# Summary of tool installations
if [ ${#FAILED_TOOLS[@]} -gt 0 ]; then
    echo -e "${YELLOW}⚠ Some tools failed to install: ${FAILED_TOOLS[*]}${NC}"
else
    echo -e "${GREEN}✓ All Dart/Flutter development tools installed successfully${NC}"
fi

print_success_banner "Dart/Flutter environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Dart/Flutter environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${FLUTTER_VERSION}"
echo "  - ${DART_VERSION}"
echo "  - Pub (package manager)"
echo ""
echo "Development tools:"
echo "  - DCM (code metrics)"
echo "  - Very Good CLI (scaffolding)"
echo "  - Melos (monorepo management)"
echo "  - dart format (formatter)"
echo "  - dart analyze (static analysis)"
echo ""
echo "Cache directories:"
echo "  - Flutter: $FLUTTER_ROOT"
echo "  - Pub cache: $PUB_CACHE"
echo ""
