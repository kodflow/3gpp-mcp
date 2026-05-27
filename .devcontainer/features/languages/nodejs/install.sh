#!/bin/bash
# Use set -e for safety; retry logic handles expected failures internally
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
    log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
    log_success() { echo -e "${GREEN}[SUCCESS]${NC} $*"; }
    log_warning() { echo -e "${YELLOW}[WARNING]${NC} $*"; }
    log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
}

# Retry function
retry() {
    local max_attempts=$1
    local delay=$2
    shift 2
    local attempt=1
    local exit_code=0

    while [ $attempt -le $max_attempts ]; do
        if "$@"; then
            if [ $attempt -gt 1 ]; then
                log_success "Command succeeded on attempt $attempt"
            fi
            return 0
        fi

        exit_code=$?

        if [ $attempt -lt $max_attempts ]; then
            log_warning "Command failed (exit code: $exit_code), retrying in ${delay}s... (attempt $attempt/$max_attempts)"
            sleep "$delay"
        else
            log_error "Command failed after $max_attempts attempts"
        fi

        ((attempt++))
    done

    return $exit_code
}

# apt-get with retry and lock handling
apt_get_retry() {
    local max_attempts=5
    local attempt=1
    local delay=10

    while [ $attempt -le $max_attempts ]; do
        # Wait for apt locks to be released
        local lock_wait=0
        while sudo fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 || \
              sudo fuser /var/lib/apt/lists/lock >/dev/null 2>&1 || \
              sudo fuser /var/cache/apt/archives/lock >/dev/null 2>&1; do
            if [ $lock_wait -eq 0 ]; then
                log_warning "Waiting for apt locks to be released..."
            fi
            sleep 2
            lock_wait=$((lock_wait + 2))

            if [ $lock_wait -ge 60 ]; then
                log_warning "Forcing apt lock release after 60s wait"
                sudo rm -f /var/lib/dpkg/lock-frontend
                sudo rm -f /var/lib/apt/lists/lock
                sudo rm -f /var/cache/apt/archives/lock
                sudo dpkg --configure -a || true
                break
            fi
        done

        # Try apt-get command
        if sudo apt-get "$@"; then
            if [ $attempt -gt 1 ]; then
                log_success "apt-get succeeded on attempt $attempt"
            fi
            return 0
        fi

        exit_code=$?

        if [ $attempt -lt $max_attempts ]; then
            log_warning "apt-get failed, running update and retrying in ${delay}s... (attempt $attempt/$max_attempts)"
            sudo apt-get update --fix-missing || true
            sudo dpkg --configure -a || true
            sleep "$delay"
        else
            log_error "apt-get failed after $max_attempts attempts"
        fi

        ((attempt++))
    done

    return $exit_code
}

# Download and pipe to shell with retry
download_and_pipe() {
    local url=$1
    shift
    local shell_cmd=("$@")

    log_info "Downloading and executing: $url"

    local temp_file
    temp_file=$(mktemp)

    if retry 3 5 curl -fsSL --connect-timeout 30 --max-time 300 -o "$temp_file" "$url"; then
        "${shell_cmd[@]}" < "$temp_file"
        local exit_code=$?
        rm -f "$temp_file"
        return $exit_code
    else
        rm -f "$temp_file"
        return 1
    fi
}

# Safe directory creation
mkdir_safe() {
    local dir=$1
    if [ ! -d "$dir" ]; then
        mkdir -p "$dir" 2>/dev/null || sudo mkdir -p "$dir"

        if [ "$(whoami)" = "vscode" ]; then
            sudo chown -R vscode:vscode "$dir" 2>/dev/null || true
        fi
    fi
}

print_banner "Node.js Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Node.js Development Environment"
    echo "========================================="
}

# Environment variables
# NVM installed in system location (not volume) - Microsoft best practice
# See: https://github.com/microsoft/vscode-dev-containers/blob/main/script-library/docs/node.md
export NVM_DIR="/usr/local/share/nvm"
export NVM_SYMLINK_CURRENT=true
export NODE_VERSION="${NODE_VERSION:-lts/*}"
export npm_config_cache="${npm_config_cache:-/home/vscode/.cache/npm}"

# Install dependencies with retry
log_info "Installing dependencies..."
apt_get_retry update
apt_get_retry install -y curl git build-essential libssl-dev || {
    log_error "Failed to install dependencies"
    exit 1
}

# Install NVM (Node Version Manager)
log_info "Installing NVM..."
mkdir_safe "$NVM_DIR"
# Resolve latest NVM tag with 3-tier fallback (issue #354):
#   1. git ls-remote — not rate-limited, no auth
#   2. GitHub REST API via shared helper — fallback for restricted networks
#   3. Pinned NVM_FALLBACK_VERSION — guarantees the build never aborts here
NVM_FALLBACK_VERSION="v0.40.4"
NVM_LATEST=""
NVM_LATEST=$(git ls-remote --tags --refs --sort=-v:refname \
    https://github.com/nvm-sh/nvm.git 'v[0-9]*' 2>/dev/null \
    | head -n 1 | sed 's|.*refs/tags/||') || true
if [ -z "$NVM_LATEST" ]; then
    log_warning "git ls-remote failed for nvm-sh/nvm; trying GitHub API"
    NVM_LATEST=$(get_github_latest_version_or_empty "nvm-sh/nvm")
fi
if [ -z "$NVM_LATEST" ]; then
    log_warning "GitHub API rate-limited; falling back to pinned ${NVM_FALLBACK_VERSION}"
    NVM_LATEST="$NVM_FALLBACK_VERSION"
fi
# Helper strips the leading 'v'; NVM install URL expects it
[[ "$NVM_LATEST" != v* ]] && NVM_LATEST="v${NVM_LATEST}"
log_info "Using NVM ${NVM_LATEST}"
download_and_pipe "https://raw.githubusercontent.com/nvm-sh/nvm/${NVM_LATEST}/install.sh" bash || {
    log_error "Failed to install NVM"
    exit 1
}

# Load NVM
export NVM_DIR="$NVM_DIR"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"

# Install Node.js (latest LTS by default)
log_info "Installing Node.js ${NODE_VERSION}..."
retry 3 5 nvm install "$NODE_VERSION" || {
    log_error "Failed to install Node.js"
    exit 1
}
nvm use "$NODE_VERSION"
nvm alias default "$NODE_VERSION"

# Get installed Node and npm versions
NODE_INSTALLED=$(node --version)
NPM_INSTALLED=$(npm --version)

log_success "Node.js ${NODE_INSTALLED} installed"
log_success "npm ${NPM_INSTALLED} installed"

# Create cache directory
mkdir_safe "$npm_config_cache"

# Harden npm against transient registry timeouts (issue: ETIMEDOUT on registry.npmjs.org).
# npm defaults retry only 2x with 10s mintimeout — too tight for flaky CI/proxy setups.
npm config set fetch-retries 5
npm config set fetch-retry-mintimeout 20000
npm config set fetch-retry-maxtimeout 120000
npm config set fetch-timeout 600000

# npm install with retry — retries the whole batch on ETIMEDOUT/ECONNRESET.
npm_install_global() {
    retry 3 15 npm install -g --fetch-retries=5 --fetch-timeout=600000 "$@"
}

# ─────────────────────────────────────────────────────────────────────────────
# Install Node.js Development Tools — batched for speed
# ─────────────────────────────────────────────────────────────────────────────
log_info "Installing Node.js development tools..."

if npm_install_global pnpm@latest typescript@latest eslint@latest prettier@latest tsx@latest; then
    log_success "Core tools installed (pnpm, typescript, eslint, prettier, tsx)"
else
    # Batch failed after retries — fall back to per-package install so a single
    # flaky package does not block the others. Critical core: pnpm + typescript.
    log_warning "Batch install failed; falling back to per-package install"
    CORE_FAILED=0
    for core_pkg in pnpm@latest typescript@latest eslint@latest prettier@latest tsx@latest; do
        if npm_install_global "$core_pkg"; then
            log_success "Installed $core_pkg"
        else
            log_error "Failed to install $core_pkg after retries"
            case "$core_pkg" in
                pnpm@latest|typescript@latest) CORE_FAILED=1 ;;
            esac
        fi
    done
    if [ "$CORE_FAILED" -eq 1 ]; then
        log_error "Critical core tool (pnpm or typescript) failed to install — aborting"
        exit 1
    fi
fi

# Install additional global packages from devcontainer-feature.json option
GLOBAL_PACKAGES="${GLOBALPACKAGES:-}"
if [ -n "$GLOBAL_PACKAGES" ]; then
    log_info "Installing additional global packages: ${GLOBAL_PACKAGES}"
    IFS=',' read -ra PKGS <<< "$GLOBAL_PACKAGES"
    for pkg in "${PKGS[@]}"; do
        pkg=$(echo "$pkg" | xargs)  # trim whitespace
        if [ -n "$pkg" ] && ! command -v "$pkg" &>/dev/null; then
            if npm_install_global "$pkg"; then
                log_success "Installed $pkg"
            else
                log_warning "Failed to install $pkg (non-critical)"
            fi
        fi
    done
fi

# ─────────────────────────────────────────────────────────────────────────────
# Install Desktop & WASM Tools — parallel
# ─────────────────────────────────────────────────────────────────────────────
log_info "Installing Desktop & WASM tools..."

(
    if npm_install_global electron@latest && command -v electron &> /dev/null; then
        ELECTRON_VERSION=$(electron --version 2>/dev/null || echo "installed")
        log_success "Electron ${ELECTRON_VERSION} installed"
    else
        log_warning "Electron installation failed or not in PATH (non-critical)"
    fi
) &
ELECTRON_PID=$!

(
    if npm_install_global assemblyscript@latest && command -v asc &> /dev/null; then
        ASC_VERSION=$(asc --version 2>/dev/null | head -n 1 || echo "installed")
        log_success "AssemblyScript ${ASC_VERSION} installed"
    else
        log_warning "AssemblyScript installation failed or not in PATH (non-critical)"
    fi
) &
ASC_PID=$!

# Per-PID wait so we surface real failures (bare `wait` returns 0 unconditionally).
electron_rc=0; asc_rc=0
wait "$ELECTRON_PID" || { electron_rc=$?; log_warning "Electron subshell exited non-zero (non-critical)"; }
wait "$ASC_PID" || { asc_rc=$?; log_warning "AssemblyScript subshell exited non-zero (non-critical)"; }
if [ "$electron_rc" -eq 0 ] && [ "$asc_rc" -eq 0 ]; then
    log_success "Desktop & WASM tools installed"
else
    log_warning "Desktop & WASM tools installed with non-critical failures"
fi

# Create global symlinks for node, npm, and npx
# This ensures they're available for subsequent devcontainer features
log_info "Creating global symlinks..."
NVM_NODE_DIR=$(nvm which current | xargs dirname)
if [ -n "$NVM_NODE_DIR" ] && [ -d "$NVM_NODE_DIR" ]; then
    # Create symlinks in /usr/local/bin (which is in default PATH)
    sudo ln -sf "$NVM_NODE_DIR/node" /usr/local/bin/node
    sudo ln -sf "$NVM_NODE_DIR/npm" /usr/local/bin/npm
    sudo ln -sf "$NVM_NODE_DIR/npx" /usr/local/bin/npx

    # Verify symlinks were created
    if [ -L /usr/local/bin/node ] && [ -L /usr/local/bin/npm ]; then
        log_success "Global symlinks created in /usr/local/bin"
    else
        log_warning "Failed to create global symlinks, but NVM is still available"
    fi
else
    log_warning "Could not determine NVM node directory, skipping symlink creation"
fi

# Ensure vscode user can update NVM files (especially the 'current' symlink)
# This is required because NVM_SYMLINK_CURRENT=true needs write access
log_info "Setting NVM directory ownership for vscode user..."
if [ -d "$NVM_DIR" ]; then
    sudo chown -R vscode:vscode "$NVM_DIR" 2>/dev/null || true
    log_success "NVM directory ownership set to vscode"
fi

# NVM is loaded via ~/.devcontainer-env.sh (sourced by both .zshrc and .bashrc)
# No need to inject NVM into .zshrc directly — avoids double-loading which
# causes VS Code ptyHost shell environment resolution timeout
log_info "NVM will be loaded via ~/.devcontainer-env.sh (two-phase loading)"

print_success_banner "Node.js environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Node.js environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
log_success "Installation complete!"
echo ""
echo "Installed components:"
echo "  - NVM (Node Version Manager)"
echo "  - Node.js ${NODE_INSTALLED}"
echo "  - npm ${NPM_INSTALLED}"
echo ""
echo "Development tools:"
echo "  - pnpm (package manager)"
echo "  - TypeScript (type checker)"
echo "  - ESLint (linter)"
echo "  - Prettier (formatter)"
echo "  - tsx (TypeScript runner)"
echo ""
echo "Desktop & WASM tools:"
echo "  - electron (desktop GUI framework)"
echo "  - assemblyscript (TypeScript to WASM compiler)"
echo ""
echo "Global availability:"
echo "  - node, npm, npx, pnpm, tsc, eslint, prettier available globally"
echo "  - NVM loaded in interactive shells"
echo ""
echo "Cache directory:"
echo "  - npm: $npm_config_cache"
echo ""

# Exit successfully
exit 0
