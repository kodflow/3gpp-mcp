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

print_banner "Swift Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Swift Development Environment"
    echo "========================================="
}

# Cleanup on failure
cleanup() {
    rm -f /tmp/swift-*.tar.gz /tmp/swiftformat.tar.gz /tmp/swiftlint.tar.gz
    rm -rf /tmp/SwiftFormat /tmp/SwiftLint
}
trap cleanup EXIT

# Environment variables
# Auto-resolve latest Swift version if not specified
if [ -z "${SWIFT_VERSION:-}" ] || [ "${SWIFT_VERSION}" = "latest" ]; then
    SWIFT_VERSION=""
    _swift_auth=()
    [[ -n "${GITHUB_TOKEN:-}" ]] && _swift_auth=(-H "Authorization: token ${GITHUB_TOKEN}")
    for _attempt in 1 2 3; do
        SWIFT_VERSION=$(curl -fsS --connect-timeout 5 --max-time 10 \
            "${_swift_auth[@]}" \
            "https://api.github.com/repos/swiftlang/swift/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"swift-\([^-]*\)-RELEASE".*/\1/p' | head -n 1)
        [[ -n "$SWIFT_VERSION" ]] && break
        [[ $_attempt -lt 3 ]] && sleep $((2 ** _attempt))
    done
    if [ -z "$SWIFT_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve latest Swift version from GitHub, using fallback 6.3.1${NC}"
    fi
    SWIFT_VERSION="${SWIFT_VERSION:-6.3.1}"
fi
export SWIFT_VERSION
export SWIFT_HOME="${SWIFT_HOME:-/usr/share/swift}"

# Install dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    git \
    clang \
    libcurl4-openssl-dev \
    libxml2-dev \
    libsqlite3-dev \
    libblocksruntime-dev \
    libncurses5-dev \
    libpython3-dev \
    binutils

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)
        SWIFT_ARCH="x86_64"
        ;;
    aarch64|arm64)
        SWIFT_ARCH="aarch64"
        ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

# Detect Ubuntu version
UBUNTU_VERSION=$(. /etc/os-release && echo "$VERSION_ID" | sed 's/\.//')
UBUNTU_LABEL=$(. /etc/os-release && echo "ubuntu${VERSION_ID}")

# Download and install Swift toolchain
echo -e "${YELLOW}Installing Swift ${SWIFT_VERSION} for ${SWIFT_ARCH}...${NC}"
SWIFT_TARBALL="swift-${SWIFT_VERSION}-RELEASE-${UBUNTU_LABEL}-${SWIFT_ARCH}.tar.gz"
SWIFT_URL="https://download.swift.org/swift-${SWIFT_VERSION}-release/${UBUNTU_LABEL//.}/${SWIFT_ARCH}/swift-${SWIFT_VERSION}-RELEASE/swift-${SWIFT_VERSION}-RELEASE-${UBUNTU_LABEL}-${SWIFT_ARCH}.tar.gz"

curl -fsSL --retry 3 --retry-delay 5 -o "/tmp/${SWIFT_TARBALL}" "$SWIFT_URL"

# Extract to SWIFT_HOME
sudo mkdir -p "$SWIFT_HOME"
sudo tar -xzf "/tmp/${SWIFT_TARBALL}" -C "$SWIFT_HOME" --strip-components=1
rm -f "/tmp/${SWIFT_TARBALL}"

export PATH="$SWIFT_HOME/usr/bin:$PATH"
SWIFT_INSTALLED=$(swift --version 2>&1 | head -n 1)
echo -e "${GREEN}${SWIFT_INSTALLED} installed${NC}"

# ─────────────────────────────────────────────────────────────────────────────
# Install SwiftFormat + SwiftLint in parallel
# ─────────────────────────────────────────────────────────────────────────────

# SwiftFormat (GitHub releases)
(
    echo -e "${YELLOW}Installing SwiftFormat...${NC}"
    SWIFTFORMAT_VERSION=""
    _fmt_auth=()
    [[ -n "${GITHUB_TOKEN:-}" ]] && _fmt_auth=(-H "Authorization: token ${GITHUB_TOKEN}")
    for _attempt in 1 2 3; do
        SWIFTFORMAT_VERSION=$(curl -fsS --connect-timeout 5 --max-time 10 \
            "${_fmt_auth[@]}" \
            "https://api.github.com/repos/nicklockwood/SwiftFormat/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
        [[ -n "$SWIFTFORMAT_VERSION" ]] && break
        [[ $_attempt -lt 3 ]] && sleep $((2 ** _attempt))
    done
    if [ -z "$SWIFTFORMAT_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve SwiftFormat version, skipping${NC}"
        exit 0
    fi

    SWIFTFORMAT_URL="https://github.com/nicklockwood/SwiftFormat/releases/download/${SWIFTFORMAT_VERSION}/swiftformat_linux_${SWIFT_ARCH}.tar.gz"
    if curl -fsSL --connect-timeout 10 --max-time 120 -o /tmp/swiftformat.tar.gz "$SWIFTFORMAT_URL" 2>/dev/null; then
        sudo tar -xzf /tmp/swiftformat.tar.gz -C /usr/local/bin/ 2>/dev/null || true
        sudo chmod +x /usr/local/bin/swiftformat 2>/dev/null || true
        rm -f /tmp/swiftformat.tar.gz
        echo -e "${GREEN}SwiftFormat ${SWIFTFORMAT_VERSION} installed${NC}"
    else
        echo -e "${YELLOW}SwiftFormat binary not available, building from source...${NC}"
        git clone --depth 1 --branch "$SWIFTFORMAT_VERSION" https://github.com/nicklockwood/SwiftFormat.git /tmp/SwiftFormat
        cd /tmp/SwiftFormat && swift build -c release
        sudo cp /tmp/SwiftFormat/.build/release/swiftformat /usr/local/bin/
        rm -rf /tmp/SwiftFormat
        echo -e "${GREEN}SwiftFormat ${SWIFTFORMAT_VERSION} installed (compiled)${NC}"
    fi
) &
SWIFTFORMAT_PID=$!

# SwiftLint (GitHub releases or build from source)
(
    echo -e "${YELLOW}Installing SwiftLint...${NC}"
    SWIFTLINT_VERSION=""
    _lint_auth=()
    [[ -n "${GITHUB_TOKEN:-}" ]] && _lint_auth=(-H "Authorization: token ${GITHUB_TOKEN}")
    for _attempt in 1 2 3; do
        SWIFTLINT_VERSION=$(curl -fsS --connect-timeout 5 --max-time 10 \
            "${_lint_auth[@]}" \
            "https://api.github.com/repos/realm/SwiftLint/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
        [[ -n "$SWIFTLINT_VERSION" ]] && break
        [[ $_attempt -lt 3 ]] && sleep $((2 ** _attempt))
    done
    if [ -z "$SWIFTLINT_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve SwiftLint version, skipping${NC}"
        exit 0
    fi

    SWIFTLINT_URL="https://github.com/realm/SwiftLint/releases/download/${SWIFTLINT_VERSION}/swiftlint_linux_${SWIFT_ARCH}.tar.gz"
    if curl -fsSL --connect-timeout 10 --max-time 120 -o /tmp/swiftlint.tar.gz "$SWIFTLINT_URL" 2>/dev/null; then
        sudo tar -xzf /tmp/swiftlint.tar.gz -C /usr/local/bin/ 2>/dev/null || true
        sudo chmod +x /usr/local/bin/swiftlint 2>/dev/null || true
        rm -f /tmp/swiftlint.tar.gz
        echo -e "${GREEN}SwiftLint ${SWIFTLINT_VERSION} installed${NC}"
    else
        echo -e "${YELLOW}SwiftLint binary not available, building from source...${NC}"
        git clone --depth 1 --branch "$SWIFTLINT_VERSION" https://github.com/realm/SwiftLint.git /tmp/SwiftLint
        cd /tmp/SwiftLint && swift build -c release
        sudo cp /tmp/SwiftLint/.build/release/swiftlint /usr/local/bin/
        rm -rf /tmp/SwiftLint
        echo -e "${GREEN}SwiftLint ${SWIFTLINT_VERSION} installed (compiled)${NC}"
    fi
) &
SWIFTLINT_PID=$!

wait "$SWIFTFORMAT_PID" 2>/dev/null || true
wait "$SWIFTLINT_PID" 2>/dev/null || true

# Add Swift to system profile
echo "export PATH=\"$SWIFT_HOME/usr/bin:\$PATH\"" | sudo tee /etc/profile.d/swift.sh >/dev/null

print_success_banner "Swift environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Swift environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${SWIFT_INSTALLED}"
if command -v swiftformat &>/dev/null; then
    echo "  - SwiftFormat (formatter)"
else
    echo "  - SwiftFormat (not installed)"
fi
if command -v swiftlint &>/dev/null; then
    echo "  - SwiftLint (linter)"
else
    echo "  - SwiftLint (not installed)"
fi
echo ""
