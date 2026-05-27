#!/bin/bash
# ============================================================================
# session-init.sh - Cache project metadata as env vars at session start
# Hook: SessionStart (all events)
# Exit 0 = always (fail-open)
#
# Purpose: Pre-cache git metadata into CLAUDE_ENV_FILE so every hook
#          doesn't need to call git rev-parse repeatedly.
#
# Env vars cached:
#   GH_ORG, GH_REPO, GH_BRANCH, GH_DEFAULT_BRANCH
# ============================================================================

set +e  # Fail-open: never block

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"

# Read stdin for session metadata
INPUT="$(cat 2>/dev/null || true)"
SOURCE=""
MODEL=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    SOURCE=$(printf '%s' "$INPUT" | jq -r '.source // ""' 2>/dev/null || echo "")
    MODEL=$(printf '%s' "$INPUT" | jq -r '.model // ""' 2>/dev/null || echo "")
fi

# Clean up orphaned worktrees FIRST (from crashed/interrupted sessions)
# Avoids leaving stale worktree directories around between sessions.
# Must run before any early-exit so cleanup always happens
WORKTREE_BASE="$HOME/.claude/worktrees"
BUILTIN_BASE="$PROJECT_DIR/.claude/worktrees"
WORKTREE_BASE_REAL=$(realpath -m "$WORKTREE_BASE" 2>/dev/null || echo "")
BUILTIN_BASE_REAL=$(realpath -m "$BUILTIN_BASE" 2>/dev/null || echo "")
for WT_BASE in "$WORKTREE_BASE" "$BUILTIN_BASE"; do
    if [ -d "$WT_BASE" ]; then
        for wt_dir in "$WT_BASE"/*/; do
            wt_dir="${wt_dir%/}"
            [ -d "$wt_dir" ] || continue
            # Skip symlinks to prevent following them into unrelated directories
            [ -L "$wt_dir" ] && continue
            # Canonicalize and verify path is under allowed bases
            wt_real=$(realpath -m "$wt_dir" 2>/dev/null || echo "")
            [ -n "$wt_real" ] || continue
            [[ "$wt_real" == "$WORKTREE_BASE_REAL/"* ]] || \
            [[ "$wt_real" == "$BUILTIN_BASE_REAL/"* ]] || continue
            [ -d "$wt_real" ] || continue
            # Remove worktrees older than 24h (likely orphaned)
            if find "$wt_real" -maxdepth 0 -mmin +1440 -print -quit 2>/dev/null | grep -q .; then
                git -C "$PROJECT_DIR" worktree remove "$wt_real" --force 2>/dev/null || \
                    rm -rf -- "$wt_real" 2>/dev/null || true
            fi
        done
        git -C "$PROJECT_DIR" worktree prune 2>/dev/null || true
    fi
done

# CLAUDE_ENV_FILE is set by Claude Code runtime; if not, we cannot write env vars
ENV_FILE="${CLAUDE_ENV_FILE:-}"
if [ -z "$ENV_FILE" ]; then
    exit 0
fi

# Cache git metadata
GH_BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
GH_DEFAULT_BRANCH=$(git -C "$PROJECT_DIR" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|refs/remotes/origin/||' || echo "main")

# Extract org/repo from git remote
REMOTE_URL=$(git -C "$PROJECT_DIR" remote get-url origin 2>/dev/null || echo "")
GH_ORG=""
GH_REPO=""
if [ -n "$REMOTE_URL" ]; then
    # Handle both HTTPS and SSH formats
    SLUG=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
    GH_ORG=$(echo "$SLUG" | cut -d'/' -f1)
    GH_REPO=$(echo "$SLUG" | cut -d'/' -f2)
fi

# Write to CLAUDE_ENV_FILE (key=value format, one per line)
{
    echo "GH_ORG=$GH_ORG"
    echo "GH_REPO=$GH_REPO"
    echo "GH_BRANCH=$GH_BRANCH"
    echo "GH_DEFAULT_BRANCH=$GH_DEFAULT_BRANCH"
} >> "$ENV_FILE" 2>/dev/null || true

# Log session start with source and model
BRANCH_SAFE=$(printf '%s' "$GH_BRANCH" | tr '/ ' '__')
LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

if command -v jq &>/dev/null; then
    TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg src "$SOURCE" \
        --arg mdl "$MODEL" \
        --arg branch "$GH_BRANCH" \
        '{timestamp:$ts,source:$src,model:$mdl,branch:$branch,event:"SessionStart"}' \
        >> "$LOG_DIR/session.jsonl" 2>/dev/null || true
fi

# ============================================================================
# probe_rtk_mode — emit a single [rtk] mode=… reason=… line per session.
#
# Modes (canonical, see plan 2026-05-06-rtk-mandatory-install-and-claude-memory.md):
#   enforcing  binary + RTK.md + @RTK.md import + hook + no bypass + config valid
#   advisory   reason=session-bypass    when RTK_BYPASS=1 (and everything else OK)
#   advisory   reason=hook-missing      when settings.json absent or no rtk hook entry
#                                        (binary + doctrine still present)
#   degraded   reason=no-binary         command -v rtk fails
#   degraded   reason=config-invalid    rtk config exits non-zero
#   degraded   reason=marker-missing    RTK.md missing OR @RTK.md import absent
#
# Bypass and degradation are NEVER conflated. RTK_BYPASS=1 always advisory.
# Skipped during postStart bootstrap (CLAUDE_HOOKS_BOOTSTRAP=1) to avoid spam.
# ============================================================================
probe_rtk_mode() {
    local mode reason version
    mode=""; reason=""; version=""

    # Skip during bootstrap: postStart's init_rtk runs curl/tar/jq before this
    # script's hook would be useful, and we don't want to flag transient state.
    if [ "${CLAUDE_HOOKS_BOOTSTRAP:-0}" = "1" ]; then
        return 0
    fi

    if ! command -v rtk &>/dev/null; then
        mode="degraded"; reason="no-binary"
    elif ! rtk config &>/dev/null; then
        mode="degraded"; reason="config-invalid"
    elif [ ! -f "$HOME/.claude/RTK.md" ] || ! grep -qE '^@RTK\.md' "$HOME/.claude/CLAUDE.md" 2>/dev/null; then
        # Anchor on line start so user prose mentioning @RTK.md in passing
        # (e.g. "no @RTK.md import here") doesn't trigger a false match.
        mode="degraded"; reason="marker-missing"
    elif [ "${RTK_BYPASS:-0}" = "1" ]; then
        mode="advisory"; reason="session-bypass"
    elif [ ! -f "$HOME/.claude/settings.json" ] || \
         ! grep -qE '"(rtk hook claude|[^"]*rtk-hook-claude\.sh)"' "$HOME/.claude/settings.json" 2>/dev/null; then
        # Accept either form: legacy direct invocation OR the fail-open wrapper
        # path (issue #348). Anchored on the closing `"` to avoid matching the
        # string in a comment or in commented-out template lines.
        mode="advisory"; reason="hook-missing"
    else
        mode="enforcing"
        version=$(rtk --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    fi

    # Emit one stderr line. Format-stable so /audit and tests can match it.
    if [ "$mode" = "enforcing" ]; then
        printf '[rtk] mode=%s version=%s\n' "$mode" "${version:-unknown}" >&2
    else
        printf '[rtk] mode=%s reason=%s\n' "$mode" "$reason" >&2
    fi

    # Persist snapshot for /audit (non-fatal if log dir is unwritable).
    if [ -n "$LOG_DIR" ] && command -v jq &>/dev/null; then
        local ts
        ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        jq -n -c \
            --arg mode "$mode" \
            --arg reason "$reason" \
            --arg version "${version:-}" \
            --arg ts "$ts" \
            '{mode:$mode,reason:$reason,version:$version,timestamp:$ts}' \
            > "$LOG_DIR/rtk-mode.json" 2>/dev/null || true
    fi
}

probe_rtk_mode

exit 0
