#!/usr/bin/env bash
# update-repo-mode.sh
#
# Detect whether the current repository is the devcontainer-template itself
# (template mode — /update auto-bumps feature versions and opens a PR) or a
# downstream consumer project (downstream mode — /update pulls latest GHCR
# tarballs, prunes BuildKit cache, emits a "Rebuild Without Cache" CTA).
#
# Output: a single line, either "template" or "downstream".
# Exit: always 0 (best-effort detection; unknown defaults to downstream).

set -euo pipefail

remote_url="$(git remote get-url origin 2>/dev/null || echo '')"

if [[ "$remote_url" =~ (github\.com[:/]|gitlab\.com[:/])kodflow/devcontainer-template(\.git)?$ ]]; then
    echo "template"
    exit 0
fi

# Some forks or mirrors may drop the kodflow/ prefix; fall back to repo name.
if [[ "$remote_url" =~ /devcontainer-template(\.git)?$ ]]; then
    echo "template"
    exit 0
fi

echo "downstream"
