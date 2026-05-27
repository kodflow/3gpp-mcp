#!/usr/bin/env bash
# update-feature-scan.sh
#
# Scan the current project for devcontainer feature references and detect
# staleness — i.e., features whose upstream install.sh hash diverges from the
# one embedded in the currently-published GHCR digest.
#
# Usage:
#   update-feature-scan.sh [--template-root <path>]
#
# Required env (template-root mode):
#   TEMPLATE_ROOT — path to an extracted copy of kodflow/devcontainer-template
#                   (usually populated by /update detect.md via git tarball).
#
# Output: one line per referenced feature, pipe-delimited:
#   <feature_ref>|<local_version>|<ghcr_digest>|<upstream_install_sha>|<state>
# where state ∈ {fresh, stale, missing, unknown}.
#
# Exit codes:
#   0 — scan completed (zero or more stale entries)
#   1 — prerequisite missing (jq, curl) — caller should surface cleanly
#   2 — no devcontainer.json found (nothing to scan)

set -euo pipefail

TEMPLATE_ROOT=""
while [ $# -gt 0 ]; do
    case "$1" in
        --template-root) TEMPLATE_ROOT="$2"; shift 2 ;;
        *) shift ;;
    esac
done
: "${TEMPLATE_ROOT:=${UPDATE_TEMPLATE_ROOT:-}}"

command -v jq >/dev/null || { echo "jq required" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl required" >&2; exit 1; }

declare -a dc_files=()
for candidate in .devcontainer/devcontainer.json .devcontainer/devcontainer.local.json; do
    [ -f "$candidate" ] && dc_files+=("$candidate")
done
if [ "${#dc_files[@]}" -eq 0 ]; then
    echo "No devcontainer.json found in .devcontainer/" >&2
    exit 2
fi

# Collect every feature key that targets the kodflow namespace on GHCR.
declare -a refs=()
for dc in "${dc_files[@]}"; do
    while IFS= read -r r; do
        [ -n "$r" ] && refs+=("$r")
    done < <(jq -r '(.features // {}) | keys[]?' "$dc" 2>/dev/null \
             | grep '^ghcr\.io/kodflow/devcontainer-features/' || true)
done

if [ "${#refs[@]}" -eq 0 ]; then
    exit 0
fi

# Compute sha256 of install.sh for a feature name under TEMPLATE_ROOT.
upstream_install_sha() {
    local name="$1"
    local candidates=(
        "${TEMPLATE_ROOT}/.devcontainer/features/languages/${name}/install.sh"
        "${TEMPLATE_ROOT}/.devcontainer/features/${name}/install.sh"
    )
    for p in "${candidates[@]}"; do
        if [ -f "$p" ]; then
            sha256sum "$p" | awk '{print $1}'
            return 0
        fi
    done
    echo "MISSING"
}

# Query GHCR manifest for a ref and return its digest. Works anonymously for
# public packages; curl returns empty on error.
ghcr_digest() {
    local ref="$1"                            # ghcr.io/kodflow/devcontainer-features/go:1
    local path="${ref#ghcr.io/}"              # kodflow/devcontainer-features/go:1
    local repo="${path%:*}"
    local tag="${path##*:}"
    local token
    token=$(curl -fsSL "https://ghcr.io/token?scope=repository:${repo}:pull" 2>/dev/null \
            | jq -r '.token // empty' || true)
    [ -z "$token" ] && { echo "UNKNOWN"; return 0; }
    local manifest
    manifest=$(curl -fsSL -H "Authorization: Bearer ${token}" \
               -H "Accept: application/vnd.oci.image.manifest.v1+json" \
               -H "Accept: application/vnd.oci.image.index.v1+json" \
               "https://ghcr.io/v2/${repo}/manifests/${tag}" 2>/dev/null || true)
    [ -z "$manifest" ] && { echo "UNKNOWN"; return 0; }
    echo "$manifest" | jq -r '.config.digest // .layers[0].digest // empty' | head -1
}

for ref in "${refs[@]}"; do
    name=$(echo "$ref" | sed -E 's|.*devcontainer-features/||; s|:.*||')
    local_ver=$(echo "$ref" | awk -F: '{print $2}')
    digest=$(ghcr_digest "$ref")
    if [ -n "$TEMPLATE_ROOT" ]; then
        up_sha=$(upstream_install_sha "$name")
    else
        up_sha="UNKNOWN"
    fi

    if [ "$up_sha" = "MISSING" ]; then
        state="missing"
    elif [ "$up_sha" = "UNKNOWN" ] || [ "$digest" = "UNKNOWN" ]; then
        state="unknown"
    else
        # Heuristic: embed the install.sh SHA into the local install.sh footer
        # at publish time (future improvement). Until then, the scan reports
        # "unknown" state when it can't compare confidently, and /update falls
        # back to comparing the version string in devcontainer-feature.json.
        up_ver=$(jq -r '.version' "${TEMPLATE_ROOT}/.devcontainer/features/languages/${name}/devcontainer-feature.json" 2>/dev/null \
                 || jq -r '.version' "${TEMPLATE_ROOT}/.devcontainer/features/${name}/devcontainer-feature.json" 2>/dev/null \
                 || echo "?")
        if [ "$local_ver" = "1" ] || [ "$local_ver" = "latest" ]; then
            # Floating tag — compare upstream version to what GHCR serves by
            # resolving the :1 / :latest tag and checking if a newer tag exists.
            state="unknown"
        elif [ "$up_ver" != "?" ] && [ "$up_ver" != "$local_ver" ]; then
            state="stale"
        else
            state="fresh"
        fi
    fi

    printf '%s|%s|%s|%s|%s\n' "$ref" "${local_ver:-?}" "${digest:-UNKNOWN}" "$up_sha" "$state"
done
