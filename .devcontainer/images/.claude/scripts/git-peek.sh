#!/bin/bash
# ============================================================================
# git-peek.sh - Collect all git context in a single JSON call
# Usage: git-peek.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces 13 sequential git commands with 1 script call.
# Used by: /git --commit (Phase 0.5 + 2.0)
# ============================================================================

set +e  # Fail-open

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- Identity ---
GIT_NAME=$(git config user.name 2>/dev/null || echo "")
GIT_EMAIL=$(git config user.email 2>/dev/null || echo "")
GPG_SIGN=$(git config --get commit.gpgsign 2>/dev/null || echo "false")
SIGNING_KEY=$(git config --get user.signingkey 2>/dev/null || echo "")

# --- Branch ---
CURRENT_BRANCH=$(git branch --show-current 2>/dev/null || echo "")
DEFAULT_BRANCH=$(git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null | sed 's|origin/||' || echo "main")
IS_PROTECTED=false
if [ "$CURRENT_BRANCH" = "$DEFAULT_BRANCH" ] || [ "$CURRENT_BRANCH" = "main" ] || [ "$CURRENT_BRANCH" = "master" ]; then
    IS_PROTECTED=true
fi

# --- Status ---
STATUS_RAW=$(git status --porcelain 2>/dev/null || echo "")
MODIFIED=$(echo "$STATUS_RAW" | grep -E '^ ?M' | sed 's/^...//;s/^ *//' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
UNTRACKED=$(echo "$STATUS_RAW" | grep -E '^\?\?' | sed 's/^...//;s/^ *//' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
STAGED=$(echo "$STATUS_RAW" | grep -E '^[ADMR]' | sed 's/^...//;s/^ *//' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
# All unmerged states: UU, AA, DD, UD, DU, AU, UA
CONFLICTS=$(echo "$STATUS_RAW" | grep -E '^(UU|AA|DD|UD|DU|AU|UA)' | sed 's/^...//;s/^ *//' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
HAS_LOCK=false
[ -f "$PROJECT_DIR/.git/index.lock" ] && HAS_LOCK=true

# --- Head commit ---
HEAD_SHA=$(git log -1 --format='%H' 2>/dev/null || echo "")
HEAD_SHORT=$(git log -1 --format='%h' 2>/dev/null || echo "")
HEAD_MSG=$(git log -1 --format='%s' 2>/dev/null || echo "")

# --- Diff stats ---
DIFF_STAT=$(git diff --stat 2>/dev/null || echo "")
FILES_CHANGED=$(echo "$DIFF_STAT" | tail -1 | grep -oE '[0-9]+ file' | grep -oE '[0-9]+' || echo 0)
FILES_CHANGED="${FILES_CHANGED:-0}"
INSERTIONS=$(echo "$DIFF_STAT" | tail -1 | grep -oE '[0-9]+ insertion' | grep -oE '[0-9]+' || echo 0)
INSERTIONS="${INSERTIONS:-0}"
DELETIONS=$(echo "$DIFF_STAT" | tail -1 | grep -oE '[0-9]+ deletion' | grep -oE '[0-9]+' || echo 0)
DELETIONS="${DELETIONS:-0}"

# --- Recent commits (for style matching) ---
RECENT_COMMITS=$(git log --oneline -5 2>/dev/null | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")

# --- Remote ---
REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")
PLATFORM="unknown"
ORG=""
REPO=""
if echo "$REMOTE_URL" | grep -q "github.com"; then
    PLATFORM="github"
elif echo "$REMOTE_URL" | grep -q "gitlab"; then
    PLATFORM="gitlab"
fi
# Parse org/repo from URL (handles https and ssh)
if [ -n "$REMOTE_URL" ]; then
    ORG_REPO=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
    ORG=$(echo "$ORG_REPO" | cut -d'/' -f1)
    REPO=$(echo "$ORG_REPO" | cut -d'/' -f2)
fi

# --- Worktree state ---
WORKTREE_COUNT=$(git -C "$PROJECT_DIR" worktree list 2>/dev/null | wc -l | tr -d ' ')
WORKTREE_COUNT=$((WORKTREE_COUNT - 1))  # Subtract main worktree
[ "$WORKTREE_COUNT" -lt 0 ] && WORKTREE_COUNT=0
IN_WORKTREE=false
CURRENT_WT=""
MAIN_WT=$(git -C "$PROJECT_DIR" worktree list --porcelain 2>/dev/null | head -1 | sed 's/^worktree //' | sed 's|/$||')
NORMALIZED_PROJECT_DIR=$(echo "$PROJECT_DIR" | sed 's|/$||')
if [ -n "$MAIN_WT" ] && [ "$NORMALIZED_PROJECT_DIR" != "$MAIN_WT" ]; then
    IN_WORKTREE=true
    CURRENT_WT=$(basename "$PROJECT_DIR")
fi
# Divergence from default branch
DIVERGE_AHEAD=$(git -C "$PROJECT_DIR" rev-list --count "origin/${DEFAULT_BRANCH}..HEAD" 2>/dev/null || echo 0)
DIVERGE_AHEAD=$(echo "$DIVERGE_AHEAD" | tr -d '[:space:]')
DIVERGE_AHEAD="${DIVERGE_AHEAD:-0}"

# --- .env identity (if exists) ---
ENV_NAME=""
ENV_EMAIL=""
if [ -f "$PROJECT_DIR/.env" ]; then
    ENV_NAME=$(grep -E '^GIT_USER=' "$PROJECT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '"' || echo "")
    ENV_EMAIL=$(grep -E '^GIT_EMAIL=' "$PROJECT_DIR/.env" 2>/dev/null | cut -d'=' -f2- | tr -d '"' || echo "")
fi

# --- Output JSON ---
jq -n \
    --arg name "$GIT_NAME" \
    --arg email "$GIT_EMAIL" \
    --arg gpg_sign "$GPG_SIGN" \
    --arg signing_key "$SIGNING_KEY" \
    --arg current "$CURRENT_BRANCH" \
    --arg default "$DEFAULT_BRANCH" \
    --argjson is_protected "$IS_PROTECTED" \
    --argjson modified "$MODIFIED" \
    --argjson untracked "$UNTRACKED" \
    --argjson staged "$STAGED" \
    --argjson conflicts "$CONFLICTS" \
    --argjson has_lock "$HAS_LOCK" \
    --arg sha "$HEAD_SHA" \
    --arg short_sha "$HEAD_SHORT" \
    --arg message "$HEAD_MSG" \
    --arg files_changed "$FILES_CHANGED" \
    --arg insertions "$INSERTIONS" \
    --arg deletions "$DELETIONS" \
    --argjson recent_commits "$RECENT_COMMITS" \
    --arg remote_url "$REMOTE_URL" \
    --arg platform "$PLATFORM" \
    --arg org "$ORG" \
    --arg repo "$REPO" \
    --arg env_name "$ENV_NAME" \
    --arg env_email "$ENV_EMAIL" \
    --arg wt_count "$WORKTREE_COUNT" \
    --argjson in_worktree "$IN_WORKTREE" \
    --arg current_wt "$CURRENT_WT" \
    --arg diverge_ahead "$DIVERGE_AHEAD" \
    '{
        identity: {name: $name, email: $email, gpg_signing: ($gpg_sign == "true"), signing_key: $signing_key, env_name: $env_name, env_email: $env_email},
        branch: {current: $current, default: $default, is_protected: $is_protected, needs_creation: $is_protected},
        status: {modified: $modified, untracked: $untracked, staged: $staged, conflicts: $conflicts, has_lock: $has_lock},
        head: {sha: $sha, short_sha: $short_sha, message: $message},
        diff_stats: {files_changed: ($files_changed | tonumber), insertions: ($insertions | tonumber), deletions: ($deletions | tonumber)},
        recent_commits: $recent_commits,
        remote: {url: $remote_url, platform: $platform, org: $org, repo: $repo},
        worktree: {active_count: ($wt_count | tonumber), in_worktree: $in_worktree, current_worktree: (if $current_wt == "" then null else $current_wt end), divergence_ahead: ($diverge_ahead | tonumber)}
    }'
