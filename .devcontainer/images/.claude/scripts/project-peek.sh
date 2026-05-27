#!/bin/bash
# ============================================================================
# project-peek.sh - Collect project context in a single JSON call
# Usage: project-peek.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces ~12 sequential tool calls with 1 script call.
# Used by: /warmup (Phase 1), /init (Phase 1), /improve (Phase 1), /search (Phase 1)
# ============================================================================

set +e

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- CLAUDE.md hierarchy ---
CLAUDE_FILES="[]"
if command -v jq >/dev/null 2>&1; then
    CLAUDE_FILES=$(find "$PROJECT_DIR" -name "CLAUDE.md" -not -path "*/.git/*" -not -path "*/node_modules/*" 2>/dev/null | while IFS= read -r f; do
        REL_PATH="${f#"$PROJECT_DIR"/}"
        DEPTH=$(echo "$REL_PATH" | tr '/' '\n' | wc -l | tr -d ' ')
        DEPTH=$((DEPTH - 1))
        LINES=$(wc -l < "$f" 2>/dev/null | tr -d ' ')
        UPDATED=$(head -1 "$f" 2>/dev/null | grep -oE '[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]+Z' || echo "")
        jq -n --arg path "$REL_PATH" --argjson depth "$DEPTH" --argjson lines "${LINES:-0}" --arg updated "$UPDATED" \
            '{path: $path, depth: $depth, lines: $lines, updated: $updated}'
    done | jq -s '.' 2>/dev/null || echo "[]")
fi

# --- Project type detection ---
PROJECT_TYPE="unknown"
IS_TEMPLATE=false

FOUND_MARKERS=""
for marker in go.mod Cargo.toml package.json pyproject.toml pom.xml build.gradle mix.exs Gemfile composer.json pubspec.yaml deno.json CMakeLists.txt Makefile; do
    if [ -f "$PROJECT_DIR/$marker" ]; then
        FOUND_MARKERS="${FOUND_MARKERS}\"${marker}\","
    fi
done

if [ -f "$PROJECT_DIR/devcontainer.json" ] || [ -f "$PROJECT_DIR/.devcontainer/devcontainer.json" ]; then
    FOUND_MARKERS="${FOUND_MARKERS}\"devcontainer.json\","
fi

FOUND_MARKERS="[${FOUND_MARKERS%,}]"

if echo "$FOUND_MARKERS" | grep -q "go.mod"; then PROJECT_TYPE="go";
elif echo "$FOUND_MARKERS" | grep -q "Cargo.toml"; then PROJECT_TYPE="rust";
elif echo "$FOUND_MARKERS" | grep -q "package.json"; then PROJECT_TYPE="node";
elif echo "$FOUND_MARKERS" | grep -q "pyproject.toml"; then PROJECT_TYPE="python";
elif echo "$FOUND_MARKERS" | grep -q "pom.xml"; then PROJECT_TYPE="java";
elif echo "$FOUND_MARKERS" | grep -q "mix.exs"; then PROJECT_TYPE="elixir";
elif echo "$FOUND_MARKERS" | grep -q "Gemfile"; then PROJECT_TYPE="ruby";
elif echo "$FOUND_MARKERS" | grep -q "devcontainer.json"; then PROJECT_TYPE="devcontainer-template";
fi

if grep -ql "DevContainer Template\|devcontainer-template" "$PROJECT_DIR/CLAUDE.md" 2>/dev/null; then
    IS_TEMPLATE=true
fi

# --- Git remote ---
REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")
GIT_ORG=""
GIT_REPO=""
if [ -n "$REMOTE_URL" ]; then
    ORG_REPO=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
    GIT_ORG=$(echo "$ORG_REPO" | cut -d'/' -f1)
    GIT_REPO=$(echo "$ORG_REPO" | cut -d'/' -f2)
fi

CURRENT_BRANCH=$(git branch --show-current 2>/dev/null || echo "")

# --- Features ---
FEATURES_VERSION=0
FEATURES_COUNT=0
if [ -f "$PROJECT_DIR/.claude/features.json" ]; then
    FEATURES_VERSION=$(jq -r '.version // 0' "$PROJECT_DIR/.claude/features.json" 2>/dev/null || echo 0)
    FEATURES_COUNT=$(jq -r '.features | length // 0' "$PROJECT_DIR/.claude/features.json" 2>/dev/null || echo 0)
fi

# --- Template status ---
IS_PERSONALIZED=false
TEMPLATE_MARKER=false
if [ -f "$PROJECT_DIR/CLAUDE.md" ]; then
    if grep -q "devcontainer-template" "$PROJECT_DIR/CLAUDE.md" 2>/dev/null; then
        TEMPLATE_MARKER=true
    fi
    if ! grep -q "Universal DevContainer shell" "$PROJECT_DIR/CLAUDE.md" 2>/dev/null; then
        IS_PERSONALIZED=true
    fi
fi

# --- Local docs ---
DOCS_DIR="$HOME/.claude/docs"
DOCS_CATEGORIES="[]"
DOCS_TOTAL=0
if [ -d "$DOCS_DIR" ]; then
    DOCS_CATEGORIES=$(find "$DOCS_DIR" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | while IFS= read -r d; do
        basename "$d"
    done | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
    DOCS_TOTAL=$(find "$DOCS_DIR" -name "*.md" -not -name "README.md" -not -name "TEMPLATE-*.md" 2>/dev/null | wc -l | tr -d ' ')
fi

# --- File inventory (quick counts) ---
GO_COUNT=$(find "$PROJECT_DIR/src" -name "*.go" 2>/dev/null | wc -l | tr -d ' ')
RS_COUNT=$(find "$PROJECT_DIR/src" -name "*.rs" 2>/dev/null | wc -l | tr -d ' ')
TS_COUNT=$(find "$PROJECT_DIR/src" -name "*.ts" -o -name "*.tsx" 2>/dev/null | wc -l | tr -d ' ')
PY_COUNT=$(find "$PROJECT_DIR/src" -name "*.py" 2>/dev/null | wc -l | tr -d ' ')
SH_COUNT=$(find "$PROJECT_DIR" -name "*.sh" -not -path "*/.git/*" -not -path "*/node_modules/*" 2>/dev/null | wc -l | tr -d ' ')
MD_COUNT=$(find "$PROJECT_DIR" -name "*.md" -not -path "*/.git/*" -not -path "*/node_modules/*" 2>/dev/null | wc -l | tr -d ' ')

# --- Output JSON ---
jq -n \
    --argjson claude_hierarchy "$CLAUDE_FILES" \
    --arg project_type "$PROJECT_TYPE" \
    --argjson markers "$FOUND_MARKERS" \
    --argjson is_template "$IS_TEMPLATE" \
    --arg remote_url "$REMOTE_URL" \
    --arg git_org "$GIT_ORG" \
    --arg git_repo "$GIT_REPO" \
    --arg branch "$CURRENT_BRANCH" \
    --argjson features_version "$FEATURES_VERSION" \
    --argjson features_count "$FEATURES_COUNT" \
    --argjson is_personalized "$IS_PERSONALIZED" \
    --argjson template_marker "$TEMPLATE_MARKER" \
    --argjson docs_categories "$DOCS_CATEGORIES" \
    --argjson docs_total "$DOCS_TOTAL" \
    --argjson go "$GO_COUNT" \
    --argjson rs "$RS_COUNT" \
    --argjson ts "$TS_COUNT" \
    --argjson py "$PY_COUNT" \
    --argjson sh "$SH_COUNT" \
    --argjson md "$MD_COUNT" \
    '{
        claude_hierarchy: $claude_hierarchy,
        project: {type: $project_type, markers: $markers, is_template: $is_template},
        git: {remote_url: $remote_url, org: $git_org, repo: $git_repo, branch: $branch},
        features: {version: $features_version, count: $features_count},
        template: {is_personalized: $is_personalized, marker_found: $template_marker},
        local_docs: {categories: $docs_categories, total_files: $docs_total},
        file_counts: {go: $go, rust: $rs, typescript: $ts, python: $py, shell: $sh, markdown: $md}
    }'
