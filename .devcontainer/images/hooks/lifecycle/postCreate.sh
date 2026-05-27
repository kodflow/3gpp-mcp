#!/bin/bash
# shellcheck disable=SC1090,SC1091
# ============================================================================
# postCreate.sh - Runs ONCE after container is assigned to user
# ============================================================================
# This script runs once after the dev container is assigned to a user.
# Use it for: User-specific setup, environment variables, shell config.
# Has access to user-specific secrets and permissions.
#
# Uses run_step pattern: each step runs in an isolated subshell so that
# failures (e.g. unconfigured git email, missing GPG keys) never kill
# the entire script. The container always starts successfully.
# ============================================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/../shared/utils.sh"

echo ""
echo -e "${CYAN}=========================================${NC}"
echo -e "${CYAN}   DevContainer Setup${NC}"
echo -e "${CYAN}=========================================${NC}"
echo ""

init_steps

# ============================================================================
# Step functions
# ============================================================================

# Prevents "dubious ownership" errors when container user differs from
# directory owner (common in Docker where /workspace may be owned by root)
step_git_safe_directory() {
    if ! git config --global --get-all safe.directory 2>/dev/null | grep -q "^/workspace$"; then
        git config --global --add safe.directory /workspace
        log_success "Git safe.directory configured for /workspace"
    else
        log_info "Git safe.directory already configured"
    fi
}

# Global gitignore — additional layer of protection against accidental secret commits.
# Primary protection is git-guard.sh (PreToolUse hook) which scans staged content
# for token patterns and blocks commits. This global gitignore is a secondary layer.
step_git_global_ignore() {
    local IGNORE_DIR="/home/vscode/.config/git"
    local IGNORE_FILE="$IGNORE_DIR/ignore"
    local MARKER="# managed-by: devcontainer-template"

    mkdir -p "$IGNORE_DIR" || { log_error "Failed to create $IGNORE_DIR"; return 1; }

    # If file exists and already has our managed block, skip (idempotent)
    if [ -f "$IGNORE_FILE" ] && grep -qF "$MARKER" "$IGNORE_FILE" 2>/dev/null; then
        log_info "Global gitignore already configured"
        return 0
    fi

    # Append our patterns (preserve any existing user patterns)
    cat >> "$IGNORE_FILE" << IGNOREEOF

$MARKER
# MCP configs (contain API tokens)
mcp.json
.mcp.json
**/mcp.json

# Environment files (contain secrets)
.env
.env.*
**/.env
**/.env.*

# Credential files
**/credentials.json
**/service-account.json
**/*.pem
**/id_rsa
**/id_ed25519

# 1Password
**/op-session-*
IGNOREEOF

    git config --global core.excludesfile "$IGNORE_FILE" || { log_error "Failed to set core.excludesfile"; return 1; }
    log_success "Global gitignore configured ($IGNORE_FILE)"
}

# Conditionally disable SSL verification (for corporate proxies/self-signed certs)
# Only applies when GIT_SSL_NO_VERIFY=1 is set in .env or environment
step_git_ssl_config() {
    if [ "${GIT_SSL_NO_VERIFY:-0}" = "1" ]; then
        git config --global http.sslVerify false
        log_success "Git SSL verification disabled (GIT_SSL_NO_VERIFY=1)"
    else
        log_info "Git SSL verification kept enabled (set GIT_SSL_NO_VERIFY=1 to disable)"
    fi
}

# Propagate $ENV_FILE (default /workspace/.env) GIT_USER / GIT_EMAIL into
# git config global. Without this step, the bind-mounted ~/.gitconfig
# retains the stale personal email from a previous machine; step_gpg_signing
# only reads GIT_EMAIL for GPG key lookup, leaving the committer field
# to leak the wrong identity (#365).
#
# ENV_FILE env-var indirection makes the step bats-testable.
# Idempotent: only writes git config when the value differs.
step_git_identity() {
    local env_file="${ENV_FILE:-/workspace/.env}"
    if [ ! -f "$env_file" ]; then
        log_info "No $env_file — skipping git identity propagation"
        return 0
    fi

    local declared_user declared_email current_user current_email
    declared_user=$(grep -E '^[[:space:]]*(export[[:space:]]+)?GIT_USER=' "$env_file" 2>/dev/null \
        | head -1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?GIT_USER=//; s/^"//; s/"$//' || true)
    declared_email=$(grep -E '^[[:space:]]*(export[[:space:]]+)?GIT_EMAIL=' "$env_file" 2>/dev/null \
        | head -1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?GIT_EMAIL=//; s/^"//; s/"$//' || true)

    if [ -z "$declared_user" ] && [ -z "$declared_email" ]; then
        log_info "No GIT_USER / GIT_EMAIL in $env_file — keeping current git config"
        return 0
    fi

    current_user=$(git config --global user.name 2>/dev/null || true)
    current_email=$(git config --global user.email 2>/dev/null || true)

    if [ -n "$declared_user" ] && [ "$current_user" != "$declared_user" ]; then
        git config --global user.name "$declared_user"
        log_success "Git user.name set from $env_file: $declared_user"
    fi
    if [ -n "$declared_email" ] && [ "$current_email" != "$declared_email" ]; then
        git config --global user.email "$declared_email"
        log_success "Git user.email set from $env_file: $declared_email"
    fi
}

# GPG commit signing configuration (#366: three-tier resolution).
#
# Modes (priority order, first match wins):
#   1. GPG_SIGNINGKEY declared in $ENV_FILE or environment
#   2. Existing git config --global user.signingkey (key must be in keystore)
#   3. UID match between $GIT_EMAIL and a secret key (legacy auto-discovery)
#
# Modes 1 & 2 set user.signingkey even if only the public half is present —
# this declares operator intent and survives the bootstrap window where the
# secret hasn't been imported into the bind-mounted ~/.gnupg yet.
# commit.gpgsign flips on only when a SECRET key is actually available, so
# commits never start failing mid-bootstrap.
step_gpg_signing() {
    local env_file="${ENV_FILE:-/workspace/.env}"
    local gnupghome="${GNUPGHOME:-/home/vscode/.gnupg}"

    if [ ! -d "$gnupghome" ] || ! gpg --list-keys 2>/dev/null | grep -q '^pub'; then
        # Clear any stale signing flags from a previous run so git doesn't
        # attempt to sign against a key that no longer exists. CodeRabbit #368.
        git config --global --unset commit.gpgsign 2>/dev/null || true
        git config --global --unset tag.forceSignAnnotated 2>/dev/null || true
        log_info "No GPG keys available - commit signing disabled"
        return 0
    fi

    # --- Mode 1: GPG_SIGNINGKEY from .env or environment ---
    local declared_key=""
    if [ -f "$env_file" ]; then
        declared_key=$(grep -E '^[[:space:]]*(export[[:space:]]+)?GPG_SIGNINGKEY=' "$env_file" 2>/dev/null \
            | head -1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?GPG_SIGNINGKEY=//; s/^"//; s/"$//' || true)
    fi
    [ -z "$declared_key" ] && declared_key="${GPG_SIGNINGKEY:-}"

    # --- Mode 2: pre-existing global config ---
    local preconfigured_key
    preconfigured_key=$(git config --global user.signingkey 2>/dev/null || true)

    local intent_key="${declared_key:-$preconfigured_key}"
    if [ -n "$intent_key" ]; then
        if gpg --list-keys "$intent_key" >/dev/null 2>&1; then
            git config --global user.signingkey "$intent_key"
            git config --global gpg.program gpg
            if gpg --list-secret-keys "$intent_key" 2>/dev/null | grep -q '^sec'; then
                git config --global commit.gpgsign true
                git config --global tag.forceSignAnnotated true
                local source_label="declared via GPG_SIGNINGKEY"
                [ -z "$declared_key" ] && source_label="pre-configured user.signingkey"
                log_success "Git GPG signing configured ($source_label): $intent_key"
            else
                # Pub-only: declare intent but don't enable signing yet.
                # Unset BOTH signing flags symmetrically so a previous run that
                # set tag.forceSignAnnotated doesn't keep failing tag creation.
                # Qodo #368 review.
                git config --global --unset commit.gpgsign 2>/dev/null || true
                git config --global --unset tag.forceSignAnnotated 2>/dev/null || true
                log_info "Signing key $intent_key present (pub only) — import the secret half to enable commit.gpgsign"
            fi
            return 0
        fi
        # Clear stale signing flags before warning — otherwise git keeps
        # trying to sign against the missing key on every commit. CodeRabbit #368.
        git config --global --unset commit.gpgsign 2>/dev/null || true
        git config --global --unset tag.forceSignAnnotated 2>/dev/null || true
        log_warning "Declared signing key $intent_key not in keystore — import the pubkey or 'git config --global --unset user.signingkey'"
        return 0
    fi

    # --- Mode 3: legacy UID match (downgraded log_warning → log_info on miss) ---
    local git_email=""
    if [ -f "$env_file" ]; then
        git_email=$(grep -E '^[[:space:]]*(export[[:space:]]+)?GIT_EMAIL=' "$env_file" 2>/dev/null \
            | head -1 | sed -E 's/^[[:space:]]*(export[[:space:]]+)?GIT_EMAIL=//; s/^"//; s/"$//' || true)
    fi
    [ -z "$git_email" ] && git_email=$(git config --global user.email 2>/dev/null || true)

    local gpg_key=""
    if [ -n "$git_email" ]; then
        # Parse --with-colons output (stable across gpg versions, unlike
        # the human-readable format whose layout shifted between gpg 1.x
        # and 2.x — the previous grep -B1 pipeline silently never matched
        # on modern gpg).
        gpg_key=$(gpg --list-secret-keys --with-colons --keyid-format LONG 2>/dev/null \
            | awk -v email="$git_email" -F: '
                /^sec:/ { sec_kid = $5 }
                /^uid:/ { if (index($10, email) > 0) { print sec_kid; exit } }
            ' || true)
    fi

    if [ -n "$gpg_key" ]; then
        git config --global user.signingkey "$gpg_key"
        git config --global commit.gpgsign true
        git config --global tag.forceSignAnnotated true
        git config --global gpg.program gpg
        log_success "Git GPG signing configured with key: $gpg_key (auto-matched $git_email)"
    else
        # Mode 3 no-match: clear any stale signing flags from a previous run
        # so git doesn't keep failing commit/tag operations. Symmetric with
        # the no-keys and intent-key-missing paths. Qodo #368 review.
        git config --global --unset commit.gpgsign 2>/dev/null || true
        git config --global --unset tag.forceSignAnnotated 2>/dev/null || true
        log_info "No GPG key found for email '$git_email' — declare GPG_SIGNINGKEY in $env_file or run /git to configure"
    fi
}

# Wire `core.hooksPath .githooks` for the workspace repo when a .githooks/
# directory is shipped (defence-in-depth layer 2 from issue #358 D3).
#
# Per-repo, not global: install.sh sets a GLOBAL hooksPath for the standalone
# Claude install path; that lives in ~/.claude/hooks/ and is unrelated. Here
# we want the workspace's own .githooks/commit-msg (and pre-commit) to run
# for any git client (VS Code SCM, GitKraken, terminal, …) — paths that
# never go through Claude's Bash tool and therefore bypass git-guard.sh.
step_git_hooks_path() {
    local hooks_dir="${WORKSPACE_FOLDER:-/workspace}/.githooks"
    if [ ! -d "$hooks_dir" ]; then
        log_info "No workspace .githooks/ directory — skipping core.hooksPath wiring"
        return 0
    fi
    # Make sure every hook file is executable (template ships them +x, but
    # tarball extraction or filesystem sync can drop the bit on some hosts).
    # Use `find` rather than a glob so empty dirs and filenames-with-spaces
    # both behave correctly (PR #359 CR-6).
    find "$hooks_dir" -maxdepth 1 -type f -exec chmod +x {} + 2>/dev/null || true

    # Use REPO-LOCAL config (not --global) so this stays scoped to the
    # consumer's project; multi-repo developers keep their other repos
    # untouched. Idempotent: re-running just confirms the value.
    # Guard on `.git/` existence — on a brand-new container the workspace
    # may be cloned later or may be a plain directory (PR #359 CR-7).
    if [ -d "${WORKSPACE_FOLDER:-/workspace}/.git" ] && \
       git -C "${WORKSPACE_FOLDER:-/workspace}" config core.hooksPath ".githooks" 2>/dev/null; then
        log_success "Wired core.hooksPath → .githooks/ for workspace repo"
    else
        log_info "Workspace not a git repo yet — core.hooksPath will be set on next postCreate"
    fi
}

# Create environment initialization script (~/.devcontainer-env.sh)
step_create_env_script() {
    log_info "Setting up environment variables and aliases..."

    cat > /home/vscode/.devcontainer-env.sh << 'ENVEOF'
# DevContainer Environment Initialization (v3 - lazy wrappers + cached completions)
# This file is sourced by ~/.zshrc and ~/.bashrc
#
# Architecture: Two-phase loading for fast shell startup
#   Phase 1 (always): PATH exports, env vars, fpath — fast, no subprocesses
#   Phase 2 (real terminal only): lazy wrappers, aliases, fast completions
#
# Why: VS Code's ptyHost spawns a shell to resolve env vars with a 10s timeout.
# Heavy init (eval, source <(...), nvm.sh) easily exceeds this on ARM64.
# Phase 1 gives VS Code the PATH/env it needs; Phase 2 only runs in terminals.
#
# v3 changes (from v2):
#   - Version managers (NVM, pyenv, rbenv, SDKMAN) use lazy wrappers instead of
#     eager init. Management commands load on first use; tool binaries (node,
#     python, ruby, java) work immediately via Phase 1 PATH/shims.
#   - Completions (kubectl, helm, docker, etc.) pre-cached to ~/.zsh_completions/
#     by postStart.sh and loaded via fpath (no more source <(...) subprocesses).

# ============================================================================
# Phase 1: Fast PATH and Environment Variables (no subprocesses)
# ============================================================================

# NVM (Node.js Version Manager)
export NVM_DIR="/usr/local/share/nvm"
export NVM_SYMLINK_CURRENT=true
# Add NVM current bin to PATH directly (no need to source heavy nvm.sh)
[ -d "$NVM_DIR/current/bin" ] && export PATH="$NVM_DIR/current/bin:$PATH"

# pyenv (Python Version Manager)
export PYENV_ROOT="/home/vscode/.cache/pyenv"
if [ -d "$PYENV_ROOT" ]; then
    export PATH="$PYENV_ROOT/shims:$PYENV_ROOT/bin:$PATH"
fi

# rbenv (Ruby Version Manager)
export RBENV_ROOT="/home/vscode/.cache/rbenv"
if [ -d "$RBENV_ROOT" ]; then
    export PATH="$RBENV_ROOT/shims:$RBENV_ROOT/bin:$PATH"
fi

# SDKMAN (Java/JVM SDK Manager)
export SDKMAN_DIR="/home/vscode/.cache/sdkman"
if [ -d "$SDKMAN_DIR/candidates" ]; then
    for _sdk_bin in "$SDKMAN_DIR"/candidates/*/current/bin; do
        [ -d "$_sdk_bin" ] && PATH="$_sdk_bin:$PATH"
    done
    unset _sdk_bin
fi

# Rust/Cargo
export CARGO_HOME="/home/vscode/.cache/cargo"
export RUSTUP_HOME="/home/vscode/.cache/rustup"
[ -d "$CARGO_HOME/bin" ] && export PATH="$CARGO_HOME/bin:$PATH"

# Go
export GOPATH="/home/vscode/.cache/go"
if [ -d "/usr/local/go" ]; then
    export GOROOT="/usr/local/go"
    export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"
fi

# Flutter/Dart
export FLUTTER_ROOT="/home/vscode/.cache/flutter"
export PUB_CACHE="/home/vscode/.cache/pub-cache"
if [ -d "$FLUTTER_ROOT" ]; then
    export PATH="$FLUTTER_ROOT/bin:$PUB_CACHE/bin:$PATH"
fi

# Composer (PHP)
export COMPOSER_HOME="/home/vscode/.cache/composer"
export PATH="$COMPOSER_HOME/vendor/bin:$PATH"

# Mix (Elixir)
export MIX_HOME="/home/vscode/.cache/mix"
export PATH="$MIX_HOME/escripts:$PATH"

# npm global packages
export PATH="/home/vscode/.local/share/npm-global/bin:$PATH"

# pnpm
export PNPM_HOME="/home/vscode/.cache/pnpm"
export PATH="$PNPM_HOME:$PATH"

# Local bin
export PATH="/home/vscode/.local/bin:$PATH"

# vcpkg
export VCPKG_ROOT="/home/vscode/.cache/vcpkg"
export PATH="$VCPKG_ROOT:$PATH"

# Scala (SBT)
export SBT_HOME="/home/vscode/.cache/sbt"
[ -d "$SBT_HOME/bin" ] && export PATH="$SBT_HOME/bin:$PATH"

# .NET (C#, VB.NET)
export DOTNET_ROOT="/usr/share/dotnet"
[ -d "$DOTNET_ROOT" ] && export PATH="$DOTNET_ROOT:$HOME/.dotnet/tools:$PATH"

# R
export R_HOME="/usr/lib/R"

# Cached completions: pre-generated by postStart.sh, loaded via fpath
# Must be set before compinit (which runs inside Oh My Zsh)
if [ -d "$HOME/.zsh_completions" ]; then
    fpath=("$HOME/.zsh_completions" $fpath)
fi

# ============================================================================
# Phase 2: Interactive Terminal Features (lazy wrappers, aliases, fast completions)
# ============================================================================
# Skip when stdout is not a real terminal (e.g., VS Code env resolution).
# This is the key optimization: VS Code only needs PATH/env from Phase 1.
if [ ! -t 1 ]; then
    return 0 2>/dev/null || true
fi

# ----------------------------------------------------------------------------
# Lazy-load wrappers for version managers
# Phase 1 PATH already covers tool binaries (node, python, ruby, java) via
# symlinks and shims. These wrappers only load the full manager when the
# management command itself is first used (nvm, pyenv, rbenv, sdk).
# ----------------------------------------------------------------------------

# NVM: lazy-load on first 'nvm' call (~500ms saved per shell)
nvm() {
    unfunction nvm 2>/dev/null || unset -f nvm 2>/dev/null
    [ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
    [ -s "$NVM_DIR/bash_completion" ] && \. "$NVM_DIR/bash_completion"
    nvm "$@"
}

# pyenv: lazy-load on first 'pyenv' call (~300ms saved per shell)
pyenv() {
    unfunction pyenv 2>/dev/null || unset -f pyenv 2>/dev/null
    if [ -d "$PYENV_ROOT" ]; then
        eval "$(command pyenv init -)" 2>/dev/null || true
        eval "$(command pyenv virtualenv-init -)" 2>/dev/null || true
    fi
    command pyenv "$@"
}

# rbenv: lazy-load on first 'rbenv' call (~150ms saved per shell)
rbenv() {
    unfunction rbenv 2>/dev/null || unset -f rbenv 2>/dev/null
    if [ -d "$RBENV_ROOT" ]; then
        eval "$(command rbenv init -)" 2>/dev/null || true
    fi
    command rbenv "$@"
}

# SDKMAN: lazy-load on first 'sdk' call (~400ms saved per shell)
sdk() {
    unfunction sdk 2>/dev/null || unset -f sdk 2>/dev/null
    [[ -s "$SDKMAN_DIR/bin/sdkman-init.sh" ]] && source "$SDKMAN_DIR/bin/sdkman-init.sh"
    sdk "$@"
}

# ----------------------------------------------------------------------------
# Aliases
# ----------------------------------------------------------------------------

# Detect permission flag once at shell init — avoids re-running `claude --help`
# on every super-claude invocation. Auto mode (v2.1.113+) is the upstream
# replacement for --dangerously-skip-permissions, which has been broken since
# v2.1.113 for `.claude/` and `.git/` paths (regression: hardcoded protected
# directories ignore bypassPermissions). Older containers without
# --permission-mode fall back to the legacy bypass flag automatically.
#
# Stored as a bash ARRAY so the multi-token "--permission-mode auto" form
# expands to two argv entries without unquoted word-splitting (shellcheck
# SC2086, Qodo finding on PR #332). Use "${_CLAUDE_PERM_FLAG[@]}" at call sites.
# See: ~/.claude/docs/learned/super-claude-auto-mode-fallback.md
if claude --help 2>&1 | grep -q -- '--permission-mode'; then
    _CLAUDE_PERM_FLAG=(--permission-mode auto)
else
    _CLAUDE_PERM_FLAG=(--dangerously-skip-permissions)
fi
export _CLAUDE_PERM_FLAG

# super-claude: runs claude with auto/bypass mode + MCP config if available
super-claude() {
    local mcp_config="/workspace/mcp.json"

    # Check if jq is available for JSON validation
    if ! command -v jq &>/dev/null; then
        echo "Warning: jq not found, skipping MCP config validation" >&2
        if [ -s "$mcp_config" ] && LC_ALL=C tr -d ' \t\r\n' < "$mcp_config" 2>/dev/null | head -c 1 | grep -q '{'; then
            claude "${_CLAUDE_PERM_FLAG[@]}" --mcp-config "$mcp_config" "$@"
        else
            claude "${_CLAUDE_PERM_FLAG[@]}" "$@"
        fi
        return
    fi

    if [ -f "$mcp_config" ] && jq empty "$mcp_config" 2>/dev/null; then
        claude "${_CLAUDE_PERM_FLAG[@]}" --mcp-config "$mcp_config" "$@"
    else
        claude "${_CLAUDE_PERM_FLAG[@]}" "$@"
    fi
}

# ----------------------------------------------------------------------------
# Fast completions (native complete -C, ~1ms each — no subprocess overhead)
# Heavier completions (kubectl, helm, docker, etc.) are pre-cached to
# ~/.zsh_completions/ by postStart.sh and loaded via fpath above.
# ----------------------------------------------------------------------------

# HashiCorp tools (native binary completion)
if command -v terraform &> /dev/null; then
    complete -o nospace -C "$(which terraform)" terraform 2>/dev/null || true
fi
if command -v vault &> /dev/null; then
    complete -o nospace -C "$(which vault)" vault 2>/dev/null || true
fi
if command -v consul &> /dev/null; then
    complete -o nospace -C "$(which consul)" consul 2>/dev/null || true
fi
if command -v nomad &> /dev/null; then
    complete -o nospace -C "$(which nomad)" nomad 2>/dev/null || true
fi
if command -v packer &> /dev/null; then
    complete -o nospace -C "$(which packer)" packer 2>/dev/null || true
fi

# AWS CLI (native binary completion)
if command -v aws_completer &> /dev/null; then
    complete -C aws_completer aws 2>/dev/null || true
fi

# Google Cloud SDK (static file, fast)
if [ -f "/usr/share/google-cloud-sdk/completion.zsh.inc" ]; then
    source "/usr/share/google-cloud-sdk/completion.zsh.inc" 2>/dev/null || true
fi
ENVEOF

    log_success "Environment script created at ~/.devcontainer-env.sh"
}

# Mark container as initialized
step_mark_initialized() {
    touch /home/vscode/.devcontainer-initialized
    log_success "DevContainer marked as initialized"
}

# ============================================================================
# Execution (always runs git steps, skips env if already initialized)
# ============================================================================

# Git steps run every time (safe directory, SSL, GPG)
run_step "Git safe directory"    step_git_safe_directory
run_step "Git global gitignore"  step_git_global_ignore
run_step "Git SSL configuration" step_git_ssl_config
run_step "Git identity"          step_git_identity
run_step "GPG signing"           step_gpg_signing
run_step "Git hooks path"        step_git_hooks_path

# Note: status-line is baked into the Docker image
# ktn-linter is installed by the Go feature (not in base image)

# Check if already initialized (but only if env file also exists)
# If ~/.devcontainer-env.sh is missing, we must recreate it even if marker exists
if [ -f /home/vscode/.devcontainer-initialized ] && [ -f /home/vscode/.devcontainer-env.sh ]; then
    log_success "DevContainer already initialized"
    echo ""
    exit 0
fi

run_step "Environment script"    step_create_env_script
run_step "Mark initialized"      step_mark_initialized

print_step_summary "postCreate"

exit 0
