#!/usr/bin/env bash
# update-feature-refresh.sh
#
# Downstream-mode auto-fix: force a fresh pull of feature tarballs from GHCR
# and invalidate the BuildKit layer cache so the next "Rebuild Container"
# actually re-executes install.sh. Writes a CTA file at
# /tmp/claude-rebuild-request.json so a companion VS Code task / human can
# react.
#
# Input: feature refs on stdin, one per line (e.g. ghcr.io/kodflow/.../go:1).
#
# Requires a reachable Docker daemon (skipped with a warning otherwise).
#
# Exit codes:
#   0 — refresh attempted (errors per-ref are tolerated)
#   1 — docker unavailable and no fallback possible

set -euo pipefail

if ! command -v docker >/dev/null; then
    echo "docker not available — skipping feature refresh" >&2
    exit 1
fi

if ! docker info >/dev/null 2>&1; then
    echo "docker daemon unreachable — skipping feature refresh" >&2
    exit 1
fi

declare -a refs=()
while IFS= read -r r; do
    [ -n "$r" ] && refs+=("$r")
done

if [ "${#refs[@]}" -eq 0 ]; then
    exit 0
fi

echo "Pulling fresh manifests for ${#refs[@]} feature(s)..."
for ref in "${refs[@]}"; do
    # `docker pull` on a devcontainer feature tarball works because GHCR
    # serves the OCI artifact with a manifest Docker can inspect — but the
    # blob isn't a container image so `pull` may fail silently. Use manifest
    # inspect as a best-effort cache-buster; it forces the registry client
    # to refresh its digest cache even if the blob is unusable as an image.
    docker manifest inspect "$ref" >/dev/null 2>&1 \
        || docker pull "$ref" >/dev/null 2>&1 \
        || echo "  (manifest refresh for $ref: not fatal, continuing)"
done

echo "Pruning BuildKit layer cache (regular type)..."
docker buildx prune --filter type=regular --force >/dev/null 2>&1 \
    || echo "  (buildx prune failed — you may need: docker builder prune)"

# devcontainer CLI keeps its own feature cache under the user's home.
# Remove what we find under the canonical paths; safe to recreate on demand.
rm -rf "${HOME}/.devcontainer/features" 2>/dev/null || true
rm -rf "${HOME}/.cache/devcontainer-cli/features" 2>/dev/null || true

# Re-run sync-toolchains in-place so volume-resident tools (Go CLI tools that
# live under $GOPATH/bin on the package-cache volume) are repopulated IMMEDIATELY
# without waiting for the next container start. Covers the volume-masking
# class of issues where install.sh build-time writes are hidden by the volume.
if [ -x /etc/devcontainer-hooks/lifecycle/sync-toolchains.sh ]; then
    echo "Re-syncing toolchains (volume-resident binaries)..."
    /etc/devcontainer-hooks/lifecycle/sync-toolchains.sh >/dev/null 2>&1 || true
fi

# CTA file — a VS Code extension (or an attentive human) can pick this up.
mkdir -p /tmp
cat > /tmp/claude-rebuild-request.json <<JSON
{
  "action": "rebuild_no_cache",
  "reason": "stale_features_refreshed",
  "features": $(printf '%s\n' "${refs[@]}" | jq -R . | jq -cs .),
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "instruction": "Command Palette → Dev Containers: Rebuild Without Cache"
}
JSON

echo
echo "Next step: Command Palette → 'Dev Containers: Rebuild Without Cache'"
echo "           (plain Rebuild may reuse the BuildKit layer cache.)"
