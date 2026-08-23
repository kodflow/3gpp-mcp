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
	# NOT SELECTABLE YET, and it says so instead of emitting a phantom flag.
	#
	# This branch used to append --require-etsi, which NEITHER gate binary
	# declares: cmd/validate and `mcp-3gpp check-data` both reject it as an unknown
	# flag. Choosing this level therefore did not tighten the contract — it broke
	# the bake, with Go's opaque "flag provided but not defined" as the only clue.
	# Failing here, with the reason, is strictly better than failing there without.
	#
	# Restore the original line once --require-etsi exists in BOTH binaries
	# (ETSI ingestion, Phase C); the ratchet in ADR 0002 then continues.
	echo "data-contract: DATA_CONTRACT=dense+sparse+etsi is not implementable yet — neither cmd/validate nor 'mcp-3gpp check-data' declares --require-etsi (Phase C). Use dense or dense+sparse." >&2
	exit 2
	;;
*)
	echo "data-contract: unknown DATA_CONTRACT=$level (want: dense | dense+sparse | dense+sparse+etsi)" >&2
	exit 2
	;;
esac

printf '%s\n' "$flags"
