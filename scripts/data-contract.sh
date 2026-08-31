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
	# SELECTABLE AGAIN. Both gate binaries now declare --require-etsi, so the
	# ratchet in ADR 0002 continues here rather than stopping with an explanation.
	#
	# The flag takes the ETSI corpus's PATH, because that is what makes the check
	# possible at all: it opens the second store and asserts it holds clauses, that
	# every one of them carries a vector, and that its embedding identity equals the
	# 3GPP corpus's. The last of those is the one that matters — internal/mcp
	# recomputes semantic availability PER STORE, so an ETSI half at a stale
	# identity is answered lexically while the 3GPP half is not, with no error
	# anywhere.
	#
	# DATA_ETSI_DB overrides the path for a layout that is not the image's.
	flags="$flags --require-sparse --require-etsi ${DATA_ETSI_DB:-/data/mcp-3gpp/etsi.duckdb}"
	;;
*)
	echo "data-contract: unknown DATA_CONTRACT=$level (want: dense | dense+sparse | dense+sparse+etsi)" >&2
	exit 2
	;;
esac

printf '%s\n' "$flags"
