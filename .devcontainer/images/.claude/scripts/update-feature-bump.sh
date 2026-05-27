#!/usr/bin/env bash
# update-feature-bump.sh
#
# Template-mode auto-fix: bump the "version" field in a feature's
# devcontainer-feature.json when its install.sh (or other non-docs content)
# has changed since the last bump.
#
# Idempotent: if the current feature version is already > the last published
# GHCR version, skip. Caller passes a list of feature names on stdin, one per
# line. Each bump is staged (but NOT committed) — orchestrator (apply.md)
# creates the single consolidated commit.
#
# Usage:
#   echo -e "go\nkubernetes" | update-feature-bump.sh
#
# Output: one line per feature:
#   <feature_name>|<old_version>|<new_version>|<bump_type>
#
# Exit codes:
#   0 — all bumps attempted (orchestrator decides on errors)
#   1 — prerequisite missing

set -euo pipefail

command -v jq >/dev/null || { echo "jq required" >&2; exit 1; }

# Find a feature's devcontainer-feature.json path from its short name.
feature_path() {
    local name="$1"
    for p in ".devcontainer/features/languages/${name}/devcontainer-feature.json" \
             ".devcontainer/features/${name}/devcontainer-feature.json"; do
        [ -f "$p" ] && { echo "$p"; return 0; }
    done
    return 1
}

# Decide the bump kind based on the diff since last version change.
# - If install.sh changed → patch
# - If devcontainer-feature.json.options changed → minor
# - If anything changed the public surface (containerEnv/onCreateCommand) → minor
# - Otherwise → patch (safe default)
classify_bump() {
    local path="$1"
    local dir
    dir=$(dirname "$path")
    # Find the commit that last touched the version field.
    local last_bump_sha
    last_bump_sha=$(git log -S '"version":' --format=%H -- "$path" 2>/dev/null | head -1 || true)
    [ -z "$last_bump_sha" ] && { echo "patch"; return 0; }
    local changed
    changed=$(git diff --name-only "${last_bump_sha}"..HEAD -- "$dir" 2>/dev/null || true)
    if git diff "${last_bump_sha}"..HEAD -- "$path" 2>/dev/null \
           | grep -E '^[-+][[:space:]]*"(options|containerEnv|onCreateCommand|postCreateCommand|postStartCommand)"' >/dev/null; then
        echo "minor"
    elif echo "$changed" | grep -q install.sh; then
        echo "patch"
    else
        echo "patch"
    fi
}

# Bump x.y.z per kind.
bump_version() {
    local cur="$1" kind="$2"
    local x y z
    IFS=. read -r x y z <<< "$cur"
    case "$kind" in
        major) x=$((x+1)); y=0; z=0 ;;
        minor) y=$((y+1)); z=0 ;;
        patch) z=$((z+1)) ;;
    esac
    echo "${x}.${y}.${z}"
}

while IFS= read -r name; do
    [ -z "$name" ] && continue
    path=$(feature_path "$name") || { echo "${name}|?|?|no-metadata-file" >&2; continue; }
    old=$(jq -r '.version' "$path")
    kind=$(classify_bump "$path")
    new=$(bump_version "$old" "$kind")
    # Rewrite JSON in place, preserving 2-space formatting.
    tmp=$(mktemp)
    jq --arg v "$new" '.version = $v' "$path" > "$tmp" && mv "$tmp" "$path"
    git add "$path" >/dev/null 2>&1 || true
    printf '%s|%s|%s|%s\n' "$name" "$old" "$new" "$kind"
done
