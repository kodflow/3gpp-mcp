#!/bin/bash
# ============================================================================
# initialize.sh - Runs on HOST machine BEFORE container build
# ============================================================================
# This script runs on the host machine before Docker Compose starts.
# Use it for: .env setup, project name configuration, feature validation.
# ============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOOKS_DIR="$(dirname "$SCRIPT_DIR")"
DEVCONTAINER_DIR="$(dirname "$HOOKS_DIR")"
ENV_FILE="$DEVCONTAINER_DIR/.env"

# Extract project name from git remote URL (with fallback if no remote)
REPO_NAME=$(basename "$(git config --get remote.origin.url 2>/dev/null || echo "devcontainer")" .git)

# Sanitize project name for Docker Compose requirements:
# - Must start with a letter or number
# - Only lowercase alphanumeric, hyphens, and underscores allowed
REPO_NAME=$(echo "$REPO_NAME" | sed 's/^[^a-zA-Z0-9]*//' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9_-]/-/g')

# If name is empty after sanitization, use a default
if [ -z "$REPO_NAME" ]; then
    REPO_NAME="devcontainer"
fi

echo "Initializing devcontainer environment..."
echo "Project name: $REPO_NAME"

# If .env doesn't exist, create it from .env.example
if [ ! -f "$ENV_FILE" ]; then
    echo "Creating .env from .env.example..."
    cp "$HOOKS_DIR/shared/.env.example" "$ENV_FILE"
fi

# Update or add COMPOSE_PROJECT_NAME in .env
if grep -q "^COMPOSE_PROJECT_NAME=" "$ENV_FILE"; then
    # Update existing line
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' "s|^COMPOSE_PROJECT_NAME=.*|COMPOSE_PROJECT_NAME=$REPO_NAME|" "$ENV_FILE"
    else
        # Linux
        sed -i "s|^COMPOSE_PROJECT_NAME=.*|COMPOSE_PROJECT_NAME=$REPO_NAME|" "$ENV_FILE"
    fi
    echo "Updated COMPOSE_PROJECT_NAME=$REPO_NAME in .env"
else
    # Add at the beginning of the file
    echo "COMPOSE_PROJECT_NAME=$REPO_NAME" | cat - "$ENV_FILE" > "$ENV_FILE.tmp" && mv "$ENV_FILE.tmp" "$ENV_FILE"
    echo "Added COMPOSE_PROJECT_NAME=$REPO_NAME to .env"
fi

# ============================================================================
# Validate devcontainer features
# ============================================================================
echo ""
echo "Validating devcontainer features..."

FEATURES_DIR="$DEVCONTAINER_DIR/features"
ERRORS=0
FIXED=0

for category in "$FEATURES_DIR"/*; do
    [ ! -d "$category" ] && continue

    for feature in "$category"/*; do
        [ ! -d "$feature" ] && continue

        # Skip utility directories (not actual features)
        [ "$(basename "$feature")" = "shared" ] && continue

        feature_name="$(basename "$category")/$(basename "$feature")"

        # Check devcontainer-feature.json
        if [ ! -f "$feature/devcontainer-feature.json" ]; then
            echo "ERROR: $feature_name: Missing devcontainer-feature.json"
            ERRORS=$((ERRORS + 1))
            continue
        fi

        # Check install.sh
        if [ ! -f "$feature/install.sh" ]; then
            echo "ERROR: $feature_name: Missing install.sh"
            ERRORS=$((ERRORS + 1))
            continue
        fi

        # Fix permissions if needed
        if [ ! -x "$feature/install.sh" ]; then
            chmod +x "$feature/install.sh"
            FIXED=$((FIXED + 1))
        fi
    done
done

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "ERROR: Found $ERRORS critical error(s) in features!"
    echo "Please fix missing files before building the devcontainer."
    exit 1
fi

if [ $FIXED -gt 0 ]; then
    echo "Fixed permissions on $FIXED install.sh file(s)"
fi

echo "All features validated successfully"

# ============================================================================
# Pull latest Docker image (bypass Docker cache on rebuild)
# ============================================================================
echo ""
echo "Pulling latest devcontainer image..."
docker pull ghcr.io/kodflow/devcontainer-template:latest 2>/dev/null || echo "Warning: Could not pull latest image, using cached version"

# ============================================================================
# Clean up existing containers to prevent race conditions during rebuild
# ============================================================================
echo ""
echo "Cleaning up existing devcontainer instances..."
docker compose -f "$DEVCONTAINER_DIR/docker-compose.yml" --project-name "$REPO_NAME" down --remove-orphans --timeout 5 2>/dev/null || true
echo "Cleanup complete"

echo ""
echo "Environment initialization complete!"
