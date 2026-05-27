#!/bin/bash
# ============================================================================
# infra-peek.sh - Collect infrastructure context in a single JSON call
# Usage: infra-peek.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces ~6-8 sequential tool calls with 1 script call.
# Used by: /infra (Phase 1)
# ============================================================================

set +e

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# --- Tool detection ---
TF_NAME="none"
TF_VERSION=""
TF_AVAILABLE=false
TG_AVAILABLE=false
TOFU_AVAILABLE=false

if command -v terraform >/dev/null 2>&1; then
    TF_NAME="terraform"
    TF_VERSION=$(terraform version -json 2>/dev/null | jq -r '.terraform_version // empty' 2>/dev/null || terraform version 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || echo "unknown")
    TF_AVAILABLE=true
fi
if command -v terragrunt >/dev/null 2>&1; then
    TG_AVAILABLE=true
    TF_NAME="terragrunt"
fi
if command -v tofu >/dev/null 2>&1; then
    TOFU_AVAILABLE=true
    if [ "$TF_NAME" = "none" ]; then TF_NAME="opentofu"; fi
fi

# --- Workspace ---
WS_CURRENT="default"
WS_LIST="[]"
if $TF_AVAILABLE; then
    WS_CURRENT=$(terraform workspace show 2>/dev/null || echo "default")
    WS_LIST=$(terraform workspace list 2>/dev/null | sed 's/^[* ]*//' | grep -v '^$' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo '["default"]')
fi

# --- State ---
STATE_EXISTS=false
STATE_RESOURCES=0
STATE_MODIFIED=""
if [ -f "$PROJECT_DIR/terraform.tfstate" ]; then
    STATE_EXISTS=true
    STATE_RESOURCES=$(jq -r '.resources | length // 0' "$PROJECT_DIR/terraform.tfstate" 2>/dev/null || echo 0)
    STATE_MODIFIED=$(stat -c '%Y' "$PROJECT_DIR/terraform.tfstate" 2>/dev/null | xargs -I{} date -d @{} -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || stat -f '%Sm' -t '%Y-%m-%dT%H:%M:%SZ' "$PROJECT_DIR/terraform.tfstate" 2>/dev/null || echo "")
elif $TF_AVAILABLE && terraform state list >/dev/null 2>&1; then
    STATE_EXISTS=true
    STATE_RESOURCES=$(terraform state list 2>/dev/null | wc -l | tr -d ' ')
fi

# --- Variables ---
DECLARED_VARS="[]"
TF_VAR_ENV="[]"
MISSING_VARS="[]"

if compgen -G "$PROJECT_DIR/*.tf" >/dev/null 2>&1; then
    DECLARED_VARS=$(grep -h 'variable\s*"' "$PROJECT_DIR"/*.tf 2>/dev/null | sed -E 's/.*variable\s*"([^"]+)".*/\1/' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")

    TF_VAR_ENV=$(env 2>/dev/null | grep -oE '^TF_VAR_[a-zA-Z_]+' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")

    MISSING_VARS=$(jq -n --argjson declared "$DECLARED_VARS" --argjson env_vars "$TF_VAR_ENV" \
        '$declared - ($env_vars | map(sub("^TF_VAR_"; "")))' 2>/dev/null || echo "[]")
fi

# --- Modules ---
MODULE_COUNT=0
MODULE_SOURCES="[]"
if compgen -G "$PROJECT_DIR/*.tf" >/dev/null 2>&1; then
    MODULE_SOURCES=$(grep -h 'source\s*=' "$PROJECT_DIR"/*.tf 2>/dev/null | sed -E 's/.*source\s*=\s*"([^"]+)".*/\1/' | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
    MODULE_COUNT=$(echo "$MODULE_SOURCES" | jq 'length' 2>/dev/null || echo 0)
fi

# --- Backend ---
BACKEND_TYPE="local"
if compgen -G "$PROJECT_DIR/*.tf" >/dev/null 2>&1; then
    BACKEND_TYPE=$(grep -A2 'backend\s*"' "$PROJECT_DIR"/*.tf 2>/dev/null | grep -oE '"(s3|gcs|azurerm|consul|remote|http|pg|cos)"' | tr -d '"' | head -1)
    BACKEND_TYPE="${BACKEND_TYPE:-local}"
fi

# --- 1Password secrets for TF ---
OP_AVAILABLE=false
OP_TF_ITEMS="[]"
if [ -n "$OP_SERVICE_ACCOUNT_TOKEN" ] && command -v op >/dev/null 2>&1; then
    OP_AVAILABLE=true
    REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")
    if [ -n "$REMOTE_URL" ]; then
        ORG_REPO=$(echo "$REMOTE_URL" | sed -E 's|.*[:/]([^/]+)/([^/.]+)(\.git)?$|\1/\2|')
        OP_TF_ITEMS=$(op item list --vault CI --format=json 2>/dev/null | jq -r --arg prefix "$ORG_REPO" '[.[] | select(.title | startswith($prefix)) | .title]' 2>/dev/null || echo "[]")
    fi
fi

# --- Output JSON ---
jq -n \
    --arg tool_name "$TF_NAME" \
    --arg tool_version "$TF_VERSION" \
    --argjson tf "$TF_AVAILABLE" --argjson tg "$TG_AVAILABLE" --argjson tofu "$TOFU_AVAILABLE" \
    --arg ws_current "$WS_CURRENT" --argjson ws_list "$WS_LIST" \
    --argjson state_exists "$STATE_EXISTS" --argjson state_resources "$STATE_RESOURCES" --arg state_modified "$STATE_MODIFIED" \
    --argjson declared "$DECLARED_VARS" --argjson tf_var_env "$TF_VAR_ENV" --argjson missing "$MISSING_VARS" \
    --argjson module_count "$MODULE_COUNT" --argjson module_sources "$MODULE_SOURCES" \
    --arg backend "$BACKEND_TYPE" \
    --argjson op_available "$OP_AVAILABLE" --argjson op_items "$OP_TF_ITEMS" \
    '{
        tool: {name: $tool_name, version: $tool_version, terraform: $tf, terragrunt: $tg, opentofu: $tofu},
        workspace: {current: $ws_current, list: $ws_list},
        state: {exists: $state_exists, resources_count: $state_resources, last_modified: $state_modified},
        variables: {declared: $declared, tf_var_env: $tf_var_env, missing: $missing},
        modules: {count: $module_count, sources: $module_sources},
        backend: {type: $backend},
        secrets_1password: {available: $op_available, tf_var_items: $op_items}
    }'
