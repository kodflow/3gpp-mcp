#!/bin/bash
# ============================================================================
# common.sh - Shared utilities for hook scripts
# ============================================================================
# Sourced by format.sh, lint.sh, typecheck.sh, test.sh to avoid duplication.
# ============================================================================

# Hook profile gating — controls which hooks execute based on HOOK_PROFILE env var.
# Levels: minimal (critical only), standard (+ quality), full (all, default)
#
# Usage: check_hook_profile "standard" || exit 0
#   critical  = always runs (git-guard, security)
#   standard  = runs in standard + full (lint, format, quality)
#   full      = runs only in full mode (observe, feature-update, logging)
check_hook_profile() {
    local required_level="$1"
    local current="${HOOK_PROFILE:-full}"

    case "$current" in
        minimal)
            if [ "$required_level" = "critical" ]; then
                return 0
            fi
            return 1 ;;
        standard)
            if [ "$required_level" = "critical" ] || [ "$required_level" = "standard" ]; then
                return 0
            fi
            return 1 ;;
        full|*)
            return 0 ;;
    esac
}

# Find project root by walking up from a given directory
# Checks for common build/config files that indicate a project root.
# Usage: PROJECT_ROOT=$(find_project_root "$DIR")
find_project_root() {
    local current="$1"
    local fallback="${2:-$current}"
    while [ "$current" != "/" ]; do
        if [ -f "$current/Makefile" ] || \
           [ -f "$current/package.json" ] || \
           [ -f "$current/pyproject.toml" ] || \
           [ -f "$current/go.mod" ] || \
           [ -f "$current/Cargo.toml" ] || \
           [ -f "$current/pom.xml" ] || \
           [ -f "$current/build.gradle" ] || \
           [ -f "$current/build.gradle.kts" ] || \
           [ -f "$current/build.sbt" ] || \
           [ -f "$current/mix.exs" ] || \
           [ -f "$current/pubspec.yaml" ] || \
           [ -f "$current/CMakeLists.txt" ] || \
           [ -f "$current/Package.swift" ] || \
           [ -f "$current/composer.json" ] || \
           [ -f "$current/Gemfile" ] || \
           [ -f "$current/fpm.toml" ] || \
           [ -f "$current/alire.toml" ]; then
            echo "$current"
            return
        fi
        current=$(dirname "$current")
    done
    echo "$fallback"
}

# Locate a consumer-authored golangci-lint config in $1.
# Usage: cfg=$(find_golangci_config "$PROJECT_ROOT") || cfg=""
# Why: golangci-lint should opt out by default — the gate stays silent unless
# the consumer ships a config, mirroring the Rust/Cargo.toml gating pattern.
find_golangci_config() {
    local root="${1:-.}"
    local f
    for f in .golangci.yml .golangci.yaml .golangci.toml; do
        if [ -f "$root/$f" ]; then
            printf '%s\n' "$root/$f"
            return 0
        fi
    done
    return 1
}

# Check if Makefile has a specific target.
# Tight regex rejects `# target:`, `TARGET := foo`, `integration-target:`,
# and `target:=foo` while accepting `target:` and `target: deps`.
# Usage: has_makefile_target "lint" "$PROJECT_ROOT"
has_makefile_target() {
    local target="$1"
    local root="${2:-.}"
    local makefile=""

    if [ -f "$root/Makefile" ]; then
        makefile="$root/Makefile"
    elif [ -f "$root/makefile" ]; then
        makefile="$root/makefile"
    else
        return 1
    fi

    grep -Eq "^${target}([[:space:]]+[[:alnum:]_.-]+)*:[^=]*$" "$makefile" 2>/dev/null
}

# Detect a Bazel workspace at a given root.
# Covers MODULE.bazel (bzlmod), WORKSPACE, WORKSPACE.bazel.
# Usage: has_bazel_workspace "$PROJECT_ROOT"
has_bazel_workspace() {
    local root="${1:-.}"
    [ -f "$root/MODULE.bazel" ] || \
    [ -f "$root/WORKSPACE" ] || \
    [ -f "$root/WORKSPACE.bazel" ]
}

# Resolve the preferred Bazel binary on PATH.
# Prefers bazelisk (matches Dockerfile.base), falls back to bazel.
# Echos the binary name on success; returns 1 if neither is available.
# Usage: bz=$(bazel_bin) || skip_bazel_branch
bazel_bin() {
    if command -v bazelisk >/dev/null 2>&1; then
        echo "bazelisk"
    elif command -v bazel >/dev/null 2>&1; then
        echo "bazel"
    else
        return 1
    fi
}

# Map a directory to a Bazel target label rooted at PROJECT_ROOT.
# - Project root → //...
# - Nested dir   → //pkg/foo/bar/...
# - Resolution failure (symlink loop, missing dir) → //...
# Both inputs are normalised via cd && pwd to absorb symlinks.
# Usage: label=$(bazel_label_for_dir "$DIR" "$PROJECT_ROOT")
bazel_label_for_dir() {
    local dir project_root rel
    # `pwd -P` resolves symlinks so a path like /workdir/symlink/pkg/auth
    # canonicalises to /workdir/proj/pkg/auth before the prefix strip.
    dir="$(cd "$1" 2>/dev/null && pwd -P)" || { echo "//..."; return 0; }
    project_root="$(cd "$2" 2>/dev/null && pwd -P)" || { echo "//..."; return 0; }

    if [ "$dir" = "$project_root" ]; then
        echo "//..."
        return 0
    fi

    rel="${dir#"$project_root"/}"
    if [ "$rel" = "$dir" ] || [ -z "$rel" ]; then
        echo "//..."
    else
        echo "//$rel/..."
    fi
}

# Check if Makefile supports FILE= parameter
# Usage: makefile_supports_file "$PROJECT_ROOT"
makefile_supports_file() {
    local root="${1:-.}"
    grep -qE "FILE\s*[:?]?=" "$root/Makefile" 2>/dev/null
}

# Run a Makefile target with optional FILE parameter
# Usage: run_makefile_target "fmt" "$FILE" "$PROJECT_ROOT"
run_makefile_target() {
    local target="$1"
    local file="$2"
    local root="${3:-.}"
    cd "$root" || exit 0
    if makefile_supports_file "$root"; then
        make "$target" FILE="$file" 2>/dev/null || true
    else
        make "$target" 2>/dev/null || true
    fi
}

# Source a per-script *.local.sh override if present. Caller passes its
# own ${BASH_SOURCE[0]} so the lookup resolves to the calling script's
# directory and basename, not common.sh's.
#
# Placement contract: define this near the top of the caller (sourced via
# common.sh), but CALL it after every upstream function is defined and
# immediately before the script entrypoint. Shell uses last-definition-wins,
# so .local.sh can replace any upstream function.
#
# Usage (in caller):
#     load_local_override "${BASH_SOURCE[0]}"
#     main "$@"
load_local_override() {
    local source_file="$1"
    local script_dir script_name local_override

    [ -n "$source_file" ] || return 0

    script_dir="$(cd "$(dirname "$source_file")" 2>/dev/null && pwd)" || return 0
    script_name="$(basename "$source_file" .sh)"
    local_override="$script_dir/${script_name}.local.sh"

    if [ -f "$local_override" ]; then
        # shellcheck source=/dev/null
        . "$local_override"
    fi
}

# Get files changed on current branch vs base + working tree
# Returns one path per line (relative to repo root), deduplicated.
# On main/base branch: only working tree + index (no branch diff).
# Usage: CHANGED=$(get_branch_changed_files [base_branch] [project_dir])
get_branch_changed_files() {
    local base="${1:-main}"
    local project_dir="${2:-${CLAUDE_PROJECT_DIR:-/workspace}}"
    (
        cd "$project_dir" 2>/dev/null || return
        local current_branch
        current_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
        {
            # Branch diff only if not on the base branch itself
            if [ -n "$current_branch" ] && [ "$current_branch" != "$base" ]; then
                git diff --name-only "$base"...HEAD 2>/dev/null
            fi
            # Always include working tree + index changes
            git diff --name-only 2>/dev/null
            git diff --cached --name-only 2>/dev/null
        } | sort -u
    )
}
