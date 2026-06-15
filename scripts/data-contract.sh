#!/usr/bin/env bash
# data-contract.sh — SINGLE SOURCE OF TRUTH for the data-completeness contract.
#
# Echoes the flag string shared by BOTH gates that must agree on "is this data layer
# complete enough to promote":
#   - cmd/validate            (the corpus-data-image bake gate, before pushing :latest)
#   - mcp-3gpp check-data     (the Dockerfile full-stage guard, before the mcp image ships)
#
# The pullable tag is only ever moved onto a data layer that PASSES this contract, so
# tightening the contract is a one-variable change here — no workflow edits, no drift
# between the two gates (the "half-baked image" failure mode this exists to prevent).
#
# Env (CI repo variables / job env):
#   DATA_CONTRACT     dense | dense+sparse | dense+sparse+etsi   (default: dense)
#   DATA_EMBED_FLOOR  release floor for dense convergence, e.g. Rel-99 (default: all)
#
# RATCHET: keep DATA_CONTRACT=dense until the first full sparse bake exists, then flip
# to dense+sparse; add +etsi only once cmd/validate/check-data gain --require-etsi
# (ETSI ingestion, Phase C). dense = FTS + frozen HNSW + dense embed converged.
set -euo pipefail

level="${DATA_CONTRACT:-dense}"
floor="${DATA_EMBED_FLOOR:-}"

flags="--require-fts --require-hnsw --require-embed-complete"
if [ -n "$floor" ]; then
	flags="$flags --embed-floor $floor"
fi

case "$level" in
dense) ;;
dense+sparse)
	flags="$flags --require-sparse"
	;;
dense+sparse+etsi)
	# NOTE: --require-etsi must exist in cmd/validate AND check-data (Phase C) before
	# selecting this level, or the gate binaries will reject the unknown flag.
	flags="$flags --require-sparse --require-etsi"
	;;
*)
	echo "data-contract: unknown DATA_CONTRACT=$level (want: dense | dense+sparse | dense+sparse+etsi)" >&2
	exit 2
	;;
esac

printf '%s\n' "$flags"
