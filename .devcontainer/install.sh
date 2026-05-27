#!/bin/bash
# ============================================================================
# Universal Claude Code Installation Script
# ============================================================================
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/kodflow/devcontainer-template/main/.devcontainer/install.sh | bash
#
# Or with custom target:
#   DC_TARGET=/path/to/project curl -fsSL ... | bash
#
# Or minimal installation (no docs):
#   curl -fsSL ... | bash -s -- --minimal
# ============================================================================

set -euo pipefail

# ============================================================================
# Configuration
# ============================================================================
REPO="kodflow/devcontainer-template"
BRANCH="main"
BASE="https://raw.githubusercontent.com/${REPO}/${BRANCH}"
API="https://api.github.com/repos/${REPO}/contents"

# Parse arguments
INSTALL_MINIMAL=false
NO_TEAMS=false
for arg in "$@"; do
    case "$arg" in
        --minimal) INSTALL_MINIMAL=true ;;
        --no-teams) NO_TEAMS=true ;;
        --help)
            cat <<EOF
Universal Claude Code Installation Script

Usage:
  curl -fsSL URL | bash                    # Full installation
  curl -fsSL URL | bash -s -- --minimal    # Skip documentation (155+ files)
  curl -fsSL URL | bash -s -- --no-teams   # Disable Agent Teams (force SUBAGENTS mode)
  DC_TARGET=/path curl -fsSL URL | bash    # Custom target directory

Options:
  --minimal    Skip 155 design-pattern docs (saves ~2.4MB). Learned patterns (operational guardrails) are still downloaded.
  --no-teams   Force-disable Agent Teams feature even if Claude Code supports it.
               Skills will always use the legacy Task-tool dispatch. Use this on
               third-party containers where you want zero experimental behavior.
  --help       Show this help message

Installation Location:
  Always:          \$HOME/.claude/ (both host and container)

What Gets Installed:
  - Claude CLI (if not already installed)
  - 82 specialist agents
  - 20+ slash commands (/git, /review, /plan, etc.)
  - 31 hook scripts (security, lint, format, etc.)
  - tmux (optional, enables Agent Teams split-pane mode)
  - 155+ design patterns (unless --minimal)
  - Configuration files (settings.json, mcp.json, etc.)
  - super-claude function (in ~/.bashrc and ~/.zshrc)
  - Agent Teams capability detection (~/.claude/.team-capability)

1Password Integration (REQUIRED for MCP tokens):
  OP_SERVICE_ACCOUNT_TOKEN  1Password Service Account Token
                            (vault auto-detected from service account)

  Items to create in 1Password:
    mcp-github    → GitHub Personal Access Token (field: credential)
    mcp-gitlab    → GitLab Personal Access Token (field: credential)

Agent Teams:
  Requires Claude Code >= 2.1.32. Automatically detected.
  Three capability levels:
    TMUX        - split-pane teammates (tmux + compatible terminal)
    IN_PROCESS  - teammates in main terminal (Shift+Down to cycle)
    NONE        - legacy SUBAGENTS mode (forced off or unsupported)

Total: 239+ files (~3.2MB) or 84 files (~0.8MB) with --minimal
EOF
            exit 0
            ;;
    esac
done

# ============================================================================
# Environment Detection
# ============================================================================
detect_environment() {
    echo "═══════════════════════════════════════════════"
    echo "  Universal Claude Code Installation"
    echo "═══════════════════════════════════════════════"
    echo ""

    # OS Detection
    case "$(uname -s)" in
        Linux*)  OS="linux" ;;
        Darwin*) OS="darwin" ;;
        MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
        *)       OS="unknown" ;;
    esac

    # Architecture Detection
    case "$(uname -m)" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)            ARCH="unknown" ;;
    esac

    # Container Detection
    IS_CONTAINER=false
    if [ -f /.dockerenv ]; then
        IS_CONTAINER=true
    fi

    # Home Directory Detection
    HOME_DIR="${HOME:-/home/vscode}"

    # Target Directory - ALWAYS in $HOME/.claude/ (same behavior for host and container)
    # This ensures no Claude files pollute the project workspace
    TARGET_DIR="${DC_TARGET:-$HOME_DIR/.claude}"

    echo "→ Environment Detection:"
    echo "  OS:         $OS"
    echo "  Arch:       $ARCH"
    echo "  Container:  $IS_CONTAINER"
    echo "  Home:       $HOME_DIR"
    echo "  Target:     $TARGET_DIR"
    echo ""
}

# ============================================================================
# Safe Download with Validation
# ============================================================================
# GitHub API call with optional authentication
# Uses GITHUB_TOKEN if available (5000 req/h vs 60 req/h)
github_api_call() {
    local url="$1"
    local auth_header=""

    # Use token if available for higher rate limit
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        auth_header="-H \"Authorization: token ${GITHUB_TOKEN}\""
    fi

    eval curl -sL $auth_header "$url" 2>/dev/null
}

safe_download() {
    local url="$1"
    local output="$2"
    local temp_file
    temp_file=$(mktemp)

    # Download with HTTP code
    local http_code
    http_code=$(curl -sL -w "%{http_code}" -o "$temp_file" "$url" 2>/dev/null || echo "000")

    # Validate download
    if [ "$http_code" != "200" ]; then
        rm -f "$temp_file"
        return 1
    fi

    # Check for HTML error pages (404 disguised as 200)
    if head -1 "$temp_file" 2>/dev/null | grep -qE "^404|^<!DOCTYPE|^<html"; then
        rm -f "$temp_file"
        return 1
    fi

    # Check not empty
    if [ ! -s "$temp_file" ]; then
        rm -f "$temp_file"
        return 1
    fi

    # All good, move to destination
    mkdir -p "$(dirname "$output")"
    mv "$temp_file" "$output"
    return 0
}

# ============================================================================
# Install Claude CLI
# ============================================================================
install_claude_cli() {
    if command -v claude &>/dev/null; then
        echo "→ Claude CLI:"
        echo "  ✓ Already installed ($(command -v claude))"
        return 0
    fi

    echo "→ Installing Claude CLI..."

    # Method 1: npm (if available)
    if command -v npm &>/dev/null; then
        if npm install -g @anthropic-ai/claude-code 2>/dev/null; then
            echo "  ✓ Installed via npm"
            return 0
        fi
    fi

    # Method 2: Official installer
    if curl -fsSL https://claude.ai/install.sh | sh 2>/dev/null; then
        echo "  ✓ Installed via official script"
        return 0
    fi

    echo "  ⚠ Installation failed (may already be in PATH)"
    return 0  # Non-blocking
}

# ============================================================================
# Install tmux (optional, enables Agent Teams split-pane mode)
# ============================================================================
# Non-blocking: if install fails, Agent Teams will degrade to IN_PROCESS mode.
# Multi-distro: apt, dnf, pacman, apk, brew.
# ============================================================================
install_tmux() {
    if command -v tmux &>/dev/null; then
        echo "→ tmux: ✓ already installed ($(tmux -V 2>/dev/null || echo unknown))"
        return 0
    fi

    echo "→ Installing tmux (optional, enables Agent Teams split-pane mode)..."

    # Detect sudo availability (some containers run as root, no sudo)
    local SUDO=""
    if [ "$(id -u)" != "0" ] && command -v sudo &>/dev/null; then
        SUDO="sudo"
    fi

    case "$OS" in
        linux)
            if command -v apt-get &>/dev/null; then
                $SUDO apt-get update -qq 2>/dev/null || true
                $SUDO apt-get install -y -qq tmux 2>/dev/null || true
            elif command -v dnf &>/dev/null; then
                $SUDO dnf install -y -q tmux 2>/dev/null || true
            elif command -v yum &>/dev/null; then
                $SUDO yum install -y -q tmux 2>/dev/null || true
            elif command -v pacman &>/dev/null; then
                $SUDO pacman -S --noconfirm tmux 2>/dev/null || true
            elif command -v apk &>/dev/null; then
                $SUDO apk add --no-cache tmux 2>/dev/null || true
            elif command -v zypper &>/dev/null; then
                $SUDO zypper install -y tmux 2>/dev/null || true
            fi
            ;;
        darwin)
            if command -v brew &>/dev/null; then
                brew install tmux 2>/dev/null || true
            fi
            ;;
    esac

    if command -v tmux &>/dev/null; then
        echo "  ✓ tmux installed ($(tmux -V 2>/dev/null))"
    else
        echo "  ⚠ tmux install failed or package manager unavailable"
        echo "    Agent Teams will run in in-process mode (still works)"
    fi

    return 0  # Never blocking
}

# ============================================================================
# Detect Agent Teams Capability
# ============================================================================
# Writes $HOME/.claude/.team-capability with one of:
#   TMUX        - Claude >= 2.1.32 + tmux + known-compatible terminal
#   IN_PROCESS  - Claude >= 2.1.32 but tmux absent or terminal incompatible
#   NONE        - Claude < 2.1.32, --no-teams flag, or env disabled
#
# Uses heuristics from team-mode-primitives.sh (duplicated inline here so
# install.sh is self-contained and runs before the primitives are in place).
# ============================================================================
classify_terminal_install() {
    # Priority: known-incompatible > known-compatible > unknown
    [ -n "${VSCODE_PID:-}" ]            && { echo "known-incompatible"; return; }
    [ "${TERM_PROGRAM:-}" = "vscode" ]  && { echo "known-incompatible"; return; }
    [ -n "${WT_SESSION:-}" ]            && { echo "known-incompatible"; return; }

    [ -n "${TMUX:-}" ]                  && { echo "known-compatible"; return; }

    case "${TERM_PROGRAM:-}" in
        iTerm.app|WezTerm|ghostty|kitty) echo "known-compatible"; return ;;
    esac

    echo "unknown"
}

detect_agent_teams_support() {
    local min="2.1.32"
    # Portable version comparison (works on GNU, BSD, busybox, Alpine)
    _version_gte() {
        local IFS=.
        set -- $1; local r1="${1:-0}" r2="${2:-0}" r3="${3:-0}"
        set -- $2; local c1="${1:-0}" c2="${2:-0}" c3="${3:-0}"
        [ "$c1" -gt "$r1" ] && return 0; [ "$c1" -lt "$r1" ] && return 1
        [ "$c2" -gt "$r2" ] && return 0; [ "$c2" -lt "$r2" ] && return 1
        [ "$c3" -ge "$r3" ] && return 0; return 1
    }

    local cap="NONE"
    local current=""
    local reason=""

    mkdir -p "$HOME_DIR/.claude"

    # Explicit opt-out first
    if [ "$NO_TEAMS" = "true" ]; then
        cap="NONE"
        reason="--no-teams flag"
    else
        # Probe claude version
        if command -v claude &>/dev/null; then
            current=$(claude --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
        fi

        if [ -z "$current" ]; then
            cap="NONE"
            reason="claude CLI not found or version unreadable"
        elif ! _version_gte "$min" "$current"; then
            cap="NONE"
            reason="claude $current < required $min"
        else
            # Version OK — check tmux + terminal
            local term_class
            term_class=$(classify_terminal_install)
            if command -v tmux &>/dev/null && [ "$term_class" = "known-compatible" ]; then
                cap="TMUX"
                reason="claude=$current tmux=yes term=$term_class"
            else
                cap="IN_PROCESS"
                reason="claude=$current tmux=$(command -v tmux >/dev/null && echo yes || echo no) term=$term_class"
            fi
        fi
    fi

    # Persist
    printf '%s\n' "$cap" > "$HOME_DIR/.claude/.team-capability"

    # If NONE, strip the env var from settings.json so skills fall back cleanly
    if [ "$cap" = "NONE" ] && [ -f "$HOME_DIR/.claude/settings.json" ] && command -v jq &>/dev/null; then
        if jq -e '.env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS' "$HOME_DIR/.claude/settings.json" >/dev/null 2>&1; then
            jq 'del(.env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS)' \
                "$HOME_DIR/.claude/settings.json" > "$HOME_DIR/.claude/settings.json.tmp" \
                && mv "$HOME_DIR/.claude/settings.json.tmp" "$HOME_DIR/.claude/settings.json"
            echo "  ✓ Stripped CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS from settings.json (capability=NONE)"
        fi
    fi

    echo "→ Agent Teams capability: $cap"
    echo "  Reason: $reason"
    echo "  Persisted to: $HOME_DIR/.claude/.team-capability"
}

# ============================================================================
# Download Assets Archive (single file, ~1MB)
# ============================================================================
# Priority method: download pre-built tar.gz instead of 20+ API calls
download_assets_archive() {
    local target_dir="$1"
    local release_url="https://github.com/${REPO}/releases/latest/download/claude-assets.tar.gz"
    local temp_archive
    temp_archive=$(mktemp)

    echo "→ Trying assets archive (faster)..."

    # Try GitHub Releases first (faster CDN, versioned)
    local http_code
    http_code=$(curl -sL -w "%{http_code}" -o "$temp_archive" "$release_url" 2>/dev/null || echo "000")

    if [ "$http_code" != "200" ]; then
        echo "  ⚠ Release archive not available, will use API discovery"
        rm -f "$temp_archive"
        return 1
    fi

    # Validate archive (tar -tzf works on all POSIX systems, unlike `file`)
    if ! tar -tzf "$temp_archive" >/dev/null 2>&1; then
        echo "  ⚠ Invalid archive format"
        rm -f "$temp_archive"
        return 1
    fi

    # Extract to target directory
    mkdir -p "$target_dir"
    if tar -xzf "$temp_archive" -C "$target_dir" 2>/dev/null; then
        local file_count
        file_count=$(tar -tzf "$temp_archive" 2>/dev/null | wc -l)
        echo "  ✓ Extracted $file_count files from archive"
        rm -f "$temp_archive"

        # Make scripts executable
        chmod -R 755 "$target_dir/scripts/" 2>/dev/null || true
        chmod -R 755 "$target_dir/agents/" 2>/dev/null || true

        return 0
    else
        echo "  ⚠ Failed to extract archive"
        rm -f "$temp_archive"
        return 1
    fi
}

# ============================================================================
# Download Agents (35 files)
# ============================================================================
download_agents() {
    local target_dir="$1"
    mkdir -p "$target_dir/agents"

    echo "→ Downloading agents..."

    # Discover via GitHub API (uses GITHUB_TOKEN if available for higher rate limit)
    local agents
    agents=$(github_api_call "$API/.devcontainer/images/.claude/agents" | jq -r '.[].name' 2>/dev/null | grep '\.md$' || echo "")

    if [ -z "$agents" ]; then
        echo "  ⚠ Could not discover agents via API, using fallback"
        # Fallback: known agents list (truncated for brevity)
        agents="developer-orchestrator.md developer-specialist-go.md developer-specialist-python.md"
    fi

    local count=0
    local failed=0

    for agent in $agents; do
        if safe_download "$BASE/.devcontainer/images/.claude/agents/$agent" "$target_dir/agents/$agent"; then
            count=$((count + 1))
        else
            failed=$((failed + 1))
        fi
    done

    echo "  ✓ Downloaded $count agents"
    [ $failed -gt 0 ] && echo "  ⚠ Failed: $failed agents" || true
}

# ============================================================================
# Download Commands (11 files)
# ============================================================================
download_commands() {
    local target_dir="$1"
    mkdir -p "$target_dir/commands"

    echo "→ Downloading commands..."

    # Discover via GitHub API
    local commands
    commands=$(github_api_call "$API/.devcontainer/images/.claude/commands" | jq -r '.[].name' 2>/dev/null | grep '\.md$' || echo "")

    if [ -z "$commands" ]; then
        echo "  ⚠ Could not discover commands via API, using fallback"
        commands="git.md review.md plan.md do.md search.md update.md"
    fi

    local count=0
    local failed=0

    for cmd in $commands; do
        if safe_download "$BASE/.devcontainer/images/.claude/commands/$cmd" "$target_dir/commands/$cmd"; then
            count=$((count + 1))
        else
            failed=$((failed + 1))
        fi
    done

    echo "  ✓ Downloaded $count commands"
    [ $failed -gt 0 ] && echo "  ⚠ Failed: $failed commands" || true
}

# ============================================================================
# Download Scripts (11 files)
# ============================================================================
download_scripts() {
    local target_dir="$1"
    mkdir -p "$target_dir/scripts"

    echo "→ Downloading scripts..."

    # Discover via GitHub API
    local scripts
    scripts=$(github_api_call "$API/.devcontainer/images/.claude/scripts" | jq -r '.[].name' 2>/dev/null | grep '\.sh$' || echo "")

    if [ -z "$scripts" ]; then
        echo "  ⚠ Could not discover scripts via API, using fallback"
        scripts="git-guard.sh format.sh lint.sh log.sh post-compact.sh post-edit.sh pre-commit-checks.sh pre-validate.sh test.sh typecheck.sh"
    fi

    local count=0
    local failed=0

    for script in $scripts; do
        if safe_download "$BASE/.devcontainer/images/.claude/scripts/$script" "$target_dir/scripts/$script"; then
            chmod +x "$target_dir/scripts/$script"
            count=$((count + 1))
        else
            failed=$((failed + 1))
        fi
    done

    echo "  ✓ Downloaded $count scripts"
    [ $failed -gt 0 ] && echo "  ⚠ Failed: $failed scripts" || true
}

# ============================================================================
# Download Documentation (155+ files) - OPTIONAL
# ============================================================================
download_docs() {
    local target_dir="$1"

    # Even in --minimal mode, always pull the `learned/` patterns. They are
    # operational guardrails (e.g. "agents must not git stash") that protect
    # the user's work and are cheap to ship (typically a handful of files).
    # Skipping them in minimal mode silently drops protections users rely on.
    if [ "$INSTALL_MINIMAL" = true ]; then
        mkdir -p "$target_dir/docs/learned"
        echo "→ Downloading learned patterns only (--minimal mode)..."

        # Hard-coded fallback list — learned patterns are the operational
        # guardrails users depend on, and silently shipping zero of them
        # because the GitHub API or jq returned nothing defeats the whole
        # reason this branch exists. The list MUST be kept in sync with
        # .devcontainer/images/.claude/docs/learned/*.md (CodeRabbit/Qodo, #332).
        local learned_fallback=(
            "agent-git-stash-destruction.md"
            "super-claude-auto-mode-fallback.md"
        )

        # Try API discovery first; fall back to the hard-coded list on failure.
        local discovered_files
        discovered_files=$(github_api_call "$API/.devcontainer/images/.claude/docs/learned" 2>/dev/null \
            | jq -r '.[]?.name // empty' 2>/dev/null | grep '\.md$' || echo "")

        local learned_files=()
        if [ -n "$discovered_files" ]; then
            # Read line-by-line so filenames with whitespace stay intact.
            while IFS= read -r f; do
                # Only accept simple basenames (defense against API
                # poisoning / path traversal).
                case "$f" in
                    */*|.*|"") continue ;;
                    *.md) learned_files+=("$f") ;;
                esac
            done <<<"$discovered_files"
        fi

        if [ "${#learned_files[@]}" -eq 0 ]; then
            echo "  ⚠ API discovery returned no learned files — falling back to hard-coded list"
            learned_files=("${learned_fallback[@]}")
        fi

        local learned_count=0
        for file in "${learned_files[@]}"; do
            if safe_download "$BASE/.devcontainer/images/.claude/docs/learned/$file" "$target_dir/docs/learned/$file"; then
                learned_count=$((learned_count + 1))
            fi
        done

        if [ "$learned_count" -eq 0 ]; then
            echo "  ✗ Downloaded 0 learned pattern(s) — operational guardrails missing!" >&2
            echo "    This is a hard failure: install MUST ship the safety patterns." >&2
            echo "    Check network access to raw.githubusercontent.com." >&2
            return 1
        fi

        echo "  ✓ Downloaded $learned_count learned pattern(s) (full design-patterns docs skipped)"
        return 0
    fi

    mkdir -p "$target_dir/docs"

    echo "→ Downloading documentation (this may take a moment)..."

    # Download root docs files
    local root_docs="CLAUDE.md README.md TEMPLATE-PATTERN.md TEMPLATE-README.md .markdownlint.json"
    local root_count=0

    for file in $root_docs; do
        if safe_download "$BASE/.devcontainer/images/.claude/docs/$file" "$target_dir/docs/$file"; then
            root_count=$((root_count + 1))
        fi
    done

    # Download category directories (20 categories)
    local categories
    categories=$(github_api_call "$API/.devcontainer/images/.claude/docs" | jq -r '.[] | select(.type == "dir") | .name' 2>/dev/null || echo "")

    if [ -z "$categories" ]; then
        echo "  ⚠ Could not discover doc categories, skipping patterns"
        return 0
    fi

    local pattern_count=0
    local failed=0

    for category in $categories; do
        mkdir -p "$target_dir/docs/$category"

        # Download all .md files in category
        local category_files
        category_files=$(github_api_call "$API/.devcontainer/images/.claude/docs/$category" | jq -r '.[].name' 2>/dev/null | grep '\.md$' || echo "")

        for file in $category_files; do
            if safe_download "$BASE/.devcontainer/images/.claude/docs/$category/$file" "$target_dir/docs/$category/$file"; then
                pattern_count=$((pattern_count + 1))
            else
                failed=$((failed + 1))
            fi
        done
    done

    echo "  ✓ Downloaded $root_count root files"
    echo "  ✓ Downloaded $pattern_count pattern files"
    [ $failed -gt 0 ] && echo "  ⚠ Failed: $failed files" || true
}

# ============================================================================
# Download Configuration Files
# ============================================================================
download_configs() {
    local target_dir="$1"

    echo "→ Downloading configurations..."

    local count=0

    if safe_download "$BASE/.devcontainer/images/.claude/settings.json" "$target_dir/settings.json"; then
        count=$((count + 1))
    fi

    if safe_download "$BASE/.devcontainer/images/.claude/.claude.json" "$target_dir/.claude.json"; then
        count=$((count + 1))
    fi

    # Download CLAUDE.md if not in container (host installation)
    if [ "$IS_CONTAINER" = false ] && [ ! -f "$HOME_DIR/CLAUDE.md" ]; then
        if safe_download "$BASE/CLAUDE.md" "$HOME_DIR/CLAUDE.md"; then
            echo "  ✓ Downloaded CLAUDE.md to $HOME_DIR/"
        fi
    fi

    echo "  ✓ Downloaded $count config files"
}

# ============================================================================
# Download Additional Tools (rtk, status-line)
# ============================================================================
download_tools() {
    echo "→ Installing additional tools..."

    local tool_count=0

    # Install rtk (token savings CLI proxy).
    #
    # RTK is MANDATORY: this script ships in a Linux devcontainer image and
    # the project doctrine (CLAUDE.md §2.0 RTK-FIRST) treats it as a build-
    # required tool. Failing here surfaces install regressions immediately
    # instead of letting the runtime probe paper over them.
    #
    # Tested in tests/scripts/install-sh-rtk.bats. Set RTK_INSTALL_TEST_FAIL=1
    # to deterministically simulate a failed download and assert the
    # exit-non-zero path.
    # Pinned rtk version. MUST stay in sync with ARG RTK_VERSION in
    # .devcontainer/images/Dockerfile (drift caught by
    # tests/scripts/install-sh-rtk.bats test 9). Bump policy in
    # .devcontainer/images/.claude/CLAUDE.md §2.0: dedicated PR + smoke
    # test against a freshly built container before bumping here.
    local RTK_PINNED_VERSION="v0.38.0"

    if ! command -v rtk &>/dev/null; then
        mkdir -p "$HOME_DIR/.local/bin"

        # rtk-ai/rtk has never released aarch64-unknown-linux-musl —
        # arm64 ships only the gnu variant (verified for v0.38.0 + v0.39.0).
        # Match the Dockerfile's logic. Issue #348.
        local rtk_rust_arch
        case "${ARCH}" in
            amd64) rtk_rust_arch="x86_64-unknown-linux-musl" ;;
            arm64) rtk_rust_arch="aarch64-unknown-linux-gnu" ;;
            *)
                echo "  ✗ rtk: unsupported architecture ${ARCH} (devcontainer expects amd64 or arm64)" >&2
                return 1
                ;;
        esac

        local rtk_url="https://github.com/rtk-ai/rtk/releases/download/${RTK_PINNED_VERSION}/rtk-${rtk_rust_arch}.tar.gz"
        local rtk_tmp rtk_extract
        rtk_tmp=$(mktemp)
        rtk_extract=$(mktemp -d)

        # Test hook: RTK_INSTALL_TEST_FAIL=1 forces the curl to fail so the
        # bats harness can assert the exit-non-zero path without altering
        # production behavior.
        if [ "${RTK_INSTALL_TEST_FAIL:-0}" = "1" ] || \
           ! curl -fsL --retry 3 --proto '=https' --tlsv1.2 "$rtk_url" -o "$rtk_tmp" 2>/dev/null || \
           ! tar -xzf "$rtk_tmp" -C "$rtk_extract" 2>/dev/null; then
            echo "  ✗ rtk: download/extract failed (required)" >&2
            rm -f "$rtk_tmp"
            rm -rf "$rtk_extract"
            return 1
        fi

        install -m 0755 "$rtk_extract/rtk" "$HOME_DIR/.local/bin/rtk"
        tool_count=$((tool_count + 1))
        echo "  ✓ rtk ${RTK_PINNED_VERSION} installed"

        rm -f "$rtk_tmp"
        rm -rf "$rtk_extract"
    else
        echo "  ✓ rtk already installed"
    fi

    # Install status-line (git status display)
    if ! command -v status-line &>/dev/null; then
        mkdir -p "$HOME_DIR/.local/bin"

        local status_ext=""
        [ "$OS" = "windows" ] && status_ext=".exe"

        local status_url="https://github.com/kodflow/status-line/releases/latest/download/status-line-${OS}-${ARCH}${status_ext}"
        local status_tmp
        status_tmp=$(mktemp)

        if curl -fsL --retry 3 --proto '=https' --tlsv1.2 "$status_url" -o "$status_tmp" 2>/dev/null; then
            install -m 0755 "$status_tmp" "$HOME_DIR/.local/bin/status-line${status_ext}"
            tool_count=$((tool_count + 1))
            echo "  ✓ status-line installed"
        else
            echo "  ⚠ status-line download failed (optional)"
        fi
        rm -f "$status_tmp"
    else
        echo "  ✓ status-line already installed"
    fi

    # Add to PATH if needed
    if [[ ":$PATH:" != *":$HOME_DIR/.local/bin:"* ]]; then
        # shellcheck disable=SC2016
        echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME_DIR/.bashrc" 2>/dev/null || true
        # shellcheck disable=SC2016
        echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME_DIR/.zshrc" 2>/dev/null || true
        echo "  → Added ~/.local/bin to PATH (restart shell to apply)"
    fi
}

# ============================================================================
# Ensure sensitive files are in .gitignore
# ============================================================================
ensure_gitignore() {
    local gitignore="$HOME_DIR/.gitignore"

    # Use project .gitignore if we're in a git repo
    if [ -d ".git" ]; then
        gitignore=".gitignore"
    fi

    echo "→ Updating .gitignore..."

    # Create .gitignore if it doesn't exist
    if [ ! -f "$gitignore" ]; then
        touch "$gitignore"
        echo "  ✓ Created $gitignore"
    fi

    # Add .env if not present
    if ! grep -qE '^\.env$|^\*\*\/\.env$' "$gitignore" 2>/dev/null; then
        echo "" >> "$gitignore"
        echo "# Environment files (contain secrets)" >> "$gitignore"
        echo ".env" >> "$gitignore"
        echo "**/.env" >> "$gitignore"
        echo "  ✓ Added .env"
    else
        echo "  ✓ .env already ignored"
    fi

    # Add CLAUDE.md if not present
    if ! grep -qE '^CLAUDE\.md$|^\*\*\/CLAUDE\.md$' "$gitignore" 2>/dev/null; then
        echo "" >> "$gitignore"
        echo "# Claude Code configuration (local preferences)" >> "$gitignore"
        echo "CLAUDE.md" >> "$gitignore"
        echo "**/CLAUDE.md" >> "$gitignore"
        echo "  ✓ Added CLAUDE.md"
    else
        echo "  ✓ CLAUDE.md already ignored"
    fi

    # Add .claude/ directory if not present
    if ! grep -qE '^\.claude\/?$|^\*\*\/\.claude\/?$' "$gitignore" 2>/dev/null; then
        echo "" >> "$gitignore"
        echo "# Claude Code local directory (created per project)" >> "$gitignore"
        echo ".claude/" >> "$gitignore"
        echo "**/.claude/" >> "$gitignore"
        echo "  ✓ Added .claude/"
    else
        echo "  ✓ .claude/ already ignored"
    fi
}

# ============================================================================
# 1Password Integration
# ============================================================================

# Load .env file if exists (for OP_SERVICE_ACCOUNT_TOKEN)
load_env_file() {
    local env_file="$1"
    if [ -f "$env_file" ]; then
        echo "  → Loading $env_file"
        while IFS='=' read -r key value; do
            # Skip comments and empty lines
            [[ "$key" =~ ^#.*$ ]] && continue
            [[ -z "$key" ]] && continue
            # Remove quotes from value
            value="${value%\"}"
            value="${value#\"}"
            # Export only OP token
            case "$key" in
                OP_SERVICE_ACCOUNT_TOKEN)
                    export "$key=$value"
                    ;;
            esac
        done < "$env_file"
    fi
}

# List all vaults from service account
get_1password_vaults() {
    if command -v op &>/dev/null && [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
        op vault list --format=json 2>/dev/null | jq -r '.[].name' 2>/dev/null || echo ""
    fi
}

# Get field from 1Password (searches all vaults)
get_1password_field() {
    local item="$1"
    local field="${2:-credential}"
    local value=""
    local vaults

    if ! command -v op &>/dev/null || [ -z "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
        echo ""
        return
    fi

    # Get all vaults
    vaults=$(get_1password_vaults)

    # Search item in each vault
    for vault in $vaults; do
        value=$(op item get "$item" --vault "$vault" --fields "$field" --reveal 2>/dev/null || echo "")
        if [ -n "$value" ]; then
            echo "$value"
            return
        fi
    done

    echo ""
}

# Fetch tokens from 1Password vault "halys"
fetch_1password_tokens() {
    echo "→ Checking 1Password for tokens..."

    # Try to load .env from common locations
    local env_locations=(
        "./.devcontainer/.env"
        "./.env"
        "$HOME_DIR/.env"
        "$HOME_DIR/.claude/.env"
    )

    for env_file in "${env_locations[@]}"; do
        if [ -f "$env_file" ]; then
            load_env_file "$env_file"
            break
        fi
    done

    # Check if 1Password CLI is available and configured
    if ! command -v op &>/dev/null; then
        echo "  ⚠ 1Password CLI (op) not installed"
        echo "    Install: https://developer.1password.com/docs/cli/get-started/"
        return 0
    fi

    if [ -z "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
        echo "  ⚠ OP_SERVICE_ACCOUNT_TOKEN not set"
        echo "    Set it in .env or export it"
        return 0
    fi

    echo "  ✓ 1Password CLI available"

    # List available vaults
    local vaults
    vaults=$(get_1password_vaults)

    if [ -z "$vaults" ]; then
        echo "  ⚠ No vaults accessible"
        return 0
    fi

    echo "  → Vaults: $(echo $vaults | tr '\n' ' ')"
    echo "  → Searching for tokens..."

    # Fetch tokens from 1Password (searches all vaults)
    local op_github op_gitlab

    op_github=$(get_1password_field "mcp-github" "credential")
    op_gitlab=$(get_1password_field "mcp-gitlab" "credential")

    # Use 1Password tokens if found
    [ -n "$op_github" ] && export GITHUB_TOKEN="$op_github" && echo "    ✓ mcp-github" || true
    [ -n "$op_gitlab" ] && export GITLAB_TOKEN="$op_gitlab" && echo "    ✓ mcp-gitlab" || true

    # Report what wasn't found
    [ -z "$op_github" ] && echo "    ⚠ mcp-github not found" || true
    [ -z "$op_gitlab" ] && echo "    ⚠ mcp-gitlab not found" || true
}

# ============================================================================
# Generate MCP Configuration
# ============================================================================
generate_mcp_config() {
    local target_dir="$1"
    local mcp_output="$HOME_DIR/.claude/mcp.json"

    # First, try to fetch tokens from 1Password
    fetch_1password_tokens

    echo ""
    echo "→ Generating MCP configuration..."

    # Download template
    local mcp_tpl
    mcp_tpl=$(mktemp)

    if ! safe_download "$BASE/.devcontainer/images/mcp.json.tpl" "$mcp_tpl"; then
        echo "  ⚠ Could not download MCP template"
        rm -f "$mcp_tpl"
        return 0
    fi

    # Get tokens (set by 1Password only)
    local github_token="${GITHUB_TOKEN:-}"
    local gitlab_token="${GITLAB_TOKEN:-}"
    local gitlab_api="${GITLAB_API_URL:-https://gitlab.com/api/v4}"

    # Escape tokens for sed
    local escaped_github escaped_gitlab escaped_gitlab_api
    escaped_github=$(printf '%s' "$github_token" | sed 's/[&/\]/\\&/g')
    escaped_gitlab=$(printf '%s' "$gitlab_token" | sed 's/[&/\]/\\&/g')
    escaped_gitlab_api=$(printf '%s' "$gitlab_api" | sed 's/[&/\]/\\&/g')

    # Generate mcp.json from template
    mkdir -p "$(dirname "$mcp_output")"

    if sed -e "s|{{GITHUB_TOKEN}}|${escaped_github}|g" \
           -e "s|{{GITLAB_TOKEN}}|${escaped_gitlab}|g" \
           -e "s|{{GITLAB_API_URL:-https://gitlab.com/api/v4}}|${escaped_gitlab_api}|g" \
           "$mcp_tpl" > "$mcp_output"; then

        chmod 600 "$mcp_output"

        # Validate JSON
        if command -v jq &>/dev/null && jq empty "$mcp_output" 2>/dev/null; then
            echo "  ✓ mcp.json generated at $mcp_output"
        else
            echo "  ⚠ mcp.json created but could not validate (jq not available)"
        fi
    else
        echo "  ⚠ Failed to generate mcp.json"
    fi

    rm -f "$mcp_tpl"

    # Show final token status
    echo "  Token status:"
    [ -n "$github_token" ] && echo "    GITHUB_TOKEN: ✓ configured" || echo "    GITHUB_TOKEN: ✗ not set"
    [ -n "$gitlab_token" ] && echo "    GITLAB_TOKEN: ✓ configured" || echo "    GITLAB_TOKEN: ✗ not set"
}

# ============================================================================
# Install super-claude Function
# ============================================================================
install_super_claude() {
    echo "→ Installing super-claude function..."

    local shell_functions="$HOME_DIR/.shell-functions.sh"

    # Create ~/.shell-functions.sh with super-claude (and any future functions)
    # This file is sourced by both .bashrc and .zshrc
    cat > "$shell_functions" << 'FUNCSEOF'
# Shell functions - sourced by .bashrc and .zshrc
# Created by Claude Code installer

# _run_claude: internal helper, runs claude with MCP config
_run_claude() {
    local mcp_config="$HOME/.claude/mcp.json"
    export CLAUDE_CONFIG_DIR="$HOME/.claude"

    if [ -f "$mcp_config" ] && command -v jq &>/dev/null && jq empty "$mcp_config" 2>/dev/null; then
        claude --dangerously-skip-permissions --mcp-config "$mcp_config" "$@"
    elif [ -f "$mcp_config" ]; then
        # jq not available, try anyway if file exists
        claude --dangerously-skip-permissions --mcp-config "$mcp_config" "$@"
    else
        claude --dangerously-skip-permissions "$@"
    fi
}

# super-claude: capability-aware wrapper for claude
#
# Behavior by capability (~/.claude/.team-capability):
#   NONE        → never wrap, run claude directly
#   IN_PROCESS  → never wrap, run claude directly
#   TMUX        → wrap in `tmux new -A -s claude` IF not already in tmux
#
# Bypass flag: SUPER_CLAUDE_NO_TMUX=1 → never wrap (for edge cases)
# Safety: if $TMUX is set (already in tmux), NEVER nest (known footgun)
super-claude() {
    local cap
    cap=$(cat "$HOME/.claude/.team-capability" 2>/dev/null || echo NONE)

    # Explicit bypass
    if [ "${SUPER_CLAUDE_NO_TMUX:-0}" = "1" ]; then
        _run_claude "$@"
        return
    fi

    # Already inside tmux → never nest
    if [ -n "${TMUX:-}" ]; then
        _run_claude "$@"
        return
    fi

    # Only wrap when capability is TMUX and tmux binary present
    if [ "$cap" = "TMUX" ] && command -v tmux >/dev/null 2>&1; then
        # -A attach-if-exists, -s session name
        tmux new-session -A -s claude "_run_claude \"$@\""
    else
        _run_claude "$@"
    fi
}
FUNCSEOF
    echo "  ✓ Created ~/.shell-functions.sh"

    # Source line to add to shell configs (generic, no Claude mention)
    local source_line='[[ -f ~/.shell-functions.sh ]] && source ~/.shell-functions.sh'

    # Add source line to .bashrc (create if doesn't exist)
    touch "$HOME_DIR/.bashrc" 2>/dev/null || true
    if ! grep -q "shell-functions.sh" "$HOME_DIR/.bashrc" 2>/dev/null; then
        echo "" >> "$HOME_DIR/.bashrc"
        echo "$source_line" >> "$HOME_DIR/.bashrc"
        echo "  ✓ Added source line to ~/.bashrc"
    else
        echo "  ✓ Source line already in ~/.bashrc"
    fi

    # Add source line to .zshrc (create if doesn't exist)
    touch "$HOME_DIR/.zshrc" 2>/dev/null || true
    if ! grep -q "shell-functions.sh" "$HOME_DIR/.zshrc" 2>/dev/null; then
        echo "" >> "$HOME_DIR/.zshrc"
        echo "$source_line" >> "$HOME_DIR/.zshrc"
        echo "  ✓ Added source line to ~/.zshrc"
    else
        echo "  ✓ Source line already in ~/.zshrc"
    fi

    echo ""
    echo "  Usage: super-claude [args]"
    echo "  → Runs claude with ~/.claude/mcp.json automatically"
}

# ============================================================================
# Configure Git Hooks (global, pointing to ~/.claude/hooks/)
# ============================================================================
configure_git_hooks() {
    local target_dir="$1"
    local hooks_dir="$target_dir/hooks"

    echo "→ Configuring Git hooks..."

    # Create hooks directory
    mkdir -p "$hooks_dir"

    # Create pre-commit hook that calls our validation scripts
    cat > "$hooks_dir/pre-commit" << 'HOOKEOF'
#!/bin/bash
# Pre-commit hook - calls Claude Code validation scripts
SCRIPTS_DIR="$HOME/.claude/scripts"

# Pre-commit hook: git-guard.sh (PreToolUse) handles AI-attribution + secret
# scanning at Claude's Bash layer; this git-native pre-commit only runs the
# project-level lint/format/test gate. The git-native AI-attribution layer
# lives in .githooks/commit-msg (defence-in-depth layer 2, issue #358).

# Run pre-commit checks (lint, format, test)
if [ -x "$SCRIPTS_DIR/pre-commit-checks.sh" ]; then
    "$SCRIPTS_DIR/pre-commit-checks.sh" || exit 1
fi

exit 0
HOOKEOF

    # Create commit-msg hook
    cat > "$hooks_dir/commit-msg" << 'HOOKEOF'
#!/bin/bash
# Commit-msg hook - validates commit message format
COMMIT_MSG_FILE="$1"
SCRIPTS_DIR="$HOME/.claude/scripts"

# Check for AI mentions in commit message
if [ -f "$COMMIT_MSG_FILE" ]; then
    MSG=$(cat "$COMMIT_MSG_FILE")

    # Forbidden patterns (case insensitive)
    FORBIDDEN=(
        "co-authored-by.*claude"
        "co-authored-by.*anthropic"
        "co-authored-by.*ai"
        "co-authored-by.*gpt"
        "generated.*by.*ai"
        "generated.*by.*claude"
        "🤖"
    )

    for pattern in "${FORBIDDEN[@]}"; do
        if echo "$MSG" | grep -iE "$pattern" > /dev/null 2>&1; then
            echo "❌ Commit blocked: AI mention detected in commit message"
            echo "   Pattern: $pattern"
            echo "   Remove AI references and try again."
            exit 1
        fi
    done
fi

exit 0
HOOKEOF

    # Make hooks executable
    chmod +x "$hooks_dir/pre-commit" "$hooks_dir/commit-msg"

    # Configure git to use our hooks directory (global)
    git config --global core.hooksPath "$hooks_dir"

    echo "  ✓ Git hooks installed in $hooks_dir"
    echo "  ✓ Global core.hooksPath configured"
    echo ""
    echo "  Hooks installed:"
    echo "    pre-commit  → runs validation scripts"
    echo "    commit-msg  → blocks AI mentions"
}

# ============================================================================
# Verification
# ============================================================================
verify_installation() {
    local target_dir="$1"
    local errors=0

    echo ""
    echo "→ Verifying installation..."

    # Check Claude CLI
    if command -v claude &>/dev/null; then
        local claude_version
        claude_version=$(claude --version 2>/dev/null || echo "unknown")
        echo "  ✓ Claude CLI: $claude_version"
    else
        echo "  ✗ Claude CLI not found in PATH"
        errors=$((errors + 1))
    fi

    # Count assets
    local agent_count=0
    local cmd_count=0
    local script_count=0
    local doc_count=0

    [ -d "$target_dir/agents" ] && agent_count=$(find "$target_dir/agents" -name "*.md" 2>/dev/null | wc -l)
    [ -d "$target_dir/commands" ] && cmd_count=$(find "$target_dir/commands" -name "*.md" 2>/dev/null | wc -l)
    [ -d "$target_dir/scripts" ] && script_count=$(find "$target_dir/scripts" -name "*.sh" 2>/dev/null | wc -l)
    [ -d "$target_dir/docs" ] && doc_count=$(find "$target_dir/docs" -name "*.md" 2>/dev/null | wc -l)

    echo "  Assets installed:"
    echo "    Agents:   $agent_count / 82 expected"
    echo "    Commands: $cmd_count / 20+ expected"
    echo "    Scripts:  $script_count / 31 expected"
    if [ "$INSTALL_MINIMAL" = false ]; then
        echo "    Docs:     $doc_count / 155+ expected"
    else
        echo "    Docs:     skipped (--minimal mode)"
    fi

    # Validate settings.json
    if [ -f "$target_dir/settings.json" ]; then
        if command -v jq &>/dev/null && jq empty "$target_dir/settings.json" 2>/dev/null; then
            echo "  ✓ settings.json is valid JSON"
        else
            echo "  ⚠ settings.json validation skipped (jq not available)"
        fi
    else
        echo "  ✗ settings.json not found"
        errors=$((errors + 1))
    fi

    # Check Git hooks
    local hooks_path
    hooks_path=$(git config --global core.hooksPath 2>/dev/null || echo "")
    if [ -n "$hooks_path" ] && [ -d "$hooks_path" ]; then
        local hook_count
        hook_count=$(find "$hooks_path" -type f -executable 2>/dev/null | wc -l)
        echo "  ✓ Git hooks: $hook_count hooks in $hooks_path"
    else
        echo "  ⚠ Git hooks not configured (run configure_git_hooks)"
    fi

    echo ""
    if [ $errors -eq 0 ]; then
        echo "✓ Installation verified successfully"
        return 0
    else
        echo "⚠ Installation completed with $errors error(s)"
        return 1
    fi
}

# ============================================================================
# Main Installation Flow
# ============================================================================
main() {
    detect_environment

    install_claude_cli

    echo ""
    install_tmux

    echo ""
    echo "→ Downloading Claude Code assets..."
    echo "  Target: $TARGET_DIR"
    echo ""

    # Try archive first (1 request vs 20+)
    if download_assets_archive "$TARGET_DIR"; then
        echo "  → Using archive (fast path)"
        # Only download configs separately (may need dynamic generation)
        download_configs "$TARGET_DIR"
    else
        # Fallback to individual API discovery
        echo "  → Using API discovery (slow path)"
        download_agents "$TARGET_DIR"
        download_commands "$TARGET_DIR"
        download_scripts "$TARGET_DIR"
        download_docs "$TARGET_DIR"
        download_configs "$TARGET_DIR"
    fi

    echo ""
    download_tools

    echo ""
    ensure_gitignore

    echo ""
    generate_mcp_config "$TARGET_DIR"

    echo ""
    detect_agent_teams_support

    echo ""
    install_super_claude

    echo ""
    configure_git_hooks "$TARGET_DIR"

    verify_installation "$TARGET_DIR"

    echo ""
    echo "═══════════════════════════════════════════════"
    echo "  ✓ Installation Complete!"
    echo "═══════════════════════════════════════════════"
    echo ""
    echo "  Installation directory: $TARGET_DIR"
    echo ""
    echo "  Available commands:"
    echo "    /git      - Git workflow (commit, branch, PR)"
    echo "    /review   - AI-powered code review"
    echo "    /plan     - Planning mode"
    echo "    /do       - Iterative task execution"
    echo "    /search   - Documentation research"
    echo "    /update   - DevContainer template update"
    echo ""
    if [ "$IS_CONTAINER" = false ]; then
        echo "  Next steps:"
        echo "    1. Restart your shell (or source ~/.zshrc)"
        echo "    2. Create items in 1Password:"
        echo "       - mcp-github (GitHub token)"
        echo "       - mcp-gitlab (GitLab token)"
        echo "    3. Run: super-claude"
        echo ""
    else
        echo "  → Restart the DevContainer to apply changes"
        echo ""
    fi
    echo "═══════════════════════════════════════════════"
}

# Run main installation
main
