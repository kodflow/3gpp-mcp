#!/bin/bash
# ============================================================================
# review-context.sh - Collect review context in 1 JSON call
# Usage: review-context.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces 4-8 sequential commands in /review Phase 0.0 + 0.5
# ============================================================================

set +e  # Fail-open

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- Git context ---
BRANCH=$(git branch --show-current 2>/dev/null || echo "")
DEFAULT_BRANCH=$(git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null | sed 's|origin/||' || echo "main")
REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")

PLATFORM="unknown"
ORG=""
REPO=""
if echo "$REMOTE_URL" | grep -q "github.com"; then
    PLATFORM="github"
elif echo "$REMOTE_URL" | grep -q "gitlab"; then
    PLATFORM="gitlab"
fi
if [ -n "$REMOTE_URL" ]; then
    ORG_REPO=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
    ORG=$(echo "$ORG_REPO" | cut -d'/' -f1)
    REPO=$(echo "$ORG_REPO" | cut -d'/' -f2)
fi

# --- Diff stats ---
DIFF_BASE="$DEFAULT_BRANCH"
git rev-parse "origin/$DEFAULT_BRANCH" &>/dev/null && DIFF_BASE="origin/$DEFAULT_BRANCH"

DIFF_FILES=$(git diff --name-only "$DIFF_BASE"...HEAD 2>/dev/null || git diff --name-only HEAD 2>/dev/null || echo "")
DIFF_FILES_JSON=$(echo "$DIFF_FILES" | grep -v '^$' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
FILES_CHANGED=$(echo "$DIFF_FILES" | grep -c -v '^$' 2>/dev/null || echo 0)
FILES_CHANGED=$(echo "$FILES_CHANGED" | tr -d '[:space:]')

DIFF_STAT=$(git diff --stat "$DIFF_BASE"...HEAD 2>/dev/null || git diff --stat HEAD 2>/dev/null || echo "")
INSERTIONS=$(echo "$DIFF_STAT" | tail -1 | grep -oE '[0-9]+ insertion' | grep -oE '[0-9]+' || echo 0)
INSERTIONS=$(echo "$INSERTIONS" | tr -d '[:space:]')
DELETIONS=$(echo "$DIFF_STAT" | tail -1 | grep -oE '[0-9]+ deletion' | grep -oE '[0-9]+' || echo 0)
DELETIONS=$(echo "$DELETIONS" | tr -d '[:space:]')

# --- Repo profile ---
has_file() { [ -f "$PROJECT_DIR/$1" ] && echo true || echo false; }

HAS_CLAUDE_MD=$(has_file "CLAUDE.md")
HAS_CONTRIBUTING=$(has_file "CONTRIBUTING.md")
HAS_CODEOWNERS=$(has_file "CODEOWNERS")
[ "$(has_file "CODEOWNERS")" = "false" ] && HAS_CODEOWNERS=$(has_file ".github/CODEOWNERS")
HAS_EDITORCONFIG=$(has_file ".editorconfig")

# Lint configs
LINT_CONFIGS="[]"
for f in .golangci.yml .golangci.yaml .eslintrc.js .eslintrc.json .eslintrc.yml eslint.config.js eslint.config.mjs .pylintrc pyproject.toml .rubocop.yml .phpstan.neon phpstan.neon.dist .clang-tidy; do
    if [ -f "$PROJECT_DIR/$f" ]; then
        LINT_CONFIGS=$(echo "$LINT_CONFIGS" | jq --arg f "$f" '. + [$f]')
    fi
done

# --- PR detection (via gh/glab CLI) ---
PR_EXISTS=false
PR_NUMBER=""
if [ "$PLATFORM" = "github" ] && command -v gh &>/dev/null; then
    PR_NUMBER=$(gh pr view --json number -q '.number' 2>/dev/null || echo "")
    [ -n "$PR_NUMBER" ] && PR_EXISTS=true
elif [ "$PLATFORM" = "gitlab" ] && command -v glab &>/dev/null; then
    PR_NUMBER=$(glab mr view --output json 2>/dev/null | jq -r '.iid // ""' 2>/dev/null || echo "")
    [ -n "$PR_NUMBER" ] && PR_EXISTS=true
fi

# --- Output JSON ---
jq -n \
    --arg branch "$BRANCH" \
    --arg default_branch "$DEFAULT_BRANCH" \
    --arg platform "$PLATFORM" \
    --arg org "$ORG" \
    --arg repo "$REPO" \
    --arg files_changed "$FILES_CHANGED" \
    --arg insertions "$INSERTIONS" \
    --arg deletions "$DELETIONS" \
    --argjson files "$DIFF_FILES_JSON" \
    --argjson has_claude_md "$HAS_CLAUDE_MD" \
    --argjson has_contributing "$HAS_CONTRIBUTING" \
    --argjson has_codeowners "$HAS_CODEOWNERS" \
    --argjson has_editorconfig "$HAS_EDITORCONFIG" \
    --argjson lint_configs "$LINT_CONFIGS" \
    --argjson pr_exists "$PR_EXISTS" \
    --arg pr_number "$PR_NUMBER" \
    '{
        git: {branch: $branch, default_branch: $default_branch, platform: $platform, org: $org, repo: $repo},
        diff: {files_changed: ($files_changed | tonumber), insertions: ($insertions | tonumber), deletions: ($deletions | tonumber), files: $files},
        repo_profile: {has_claude_md: $has_claude_md, has_contributing: $has_contributing, has_codeowners: $has_codeowners, has_editorconfig: $has_editorconfig, lint_configs: $lint_configs},
        pr: {exists: $pr_exists, number: (if $pr_number == "" then null else ($pr_number | tonumber) end)}
    }'
