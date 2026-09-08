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
# Usage:
#   scripts/data-contract.sh [3gpp|etsi]      (default: 3gpp)
#
# THE ARM ARGUMENT EXISTS BECAUSE BOTH CORPORA ARE NOW GATED, NOT ONE.
#
# `validate` used to be a single step that ran the whole contract on
# data/3gpp.duckdb and judged the ETSI half by ONE composite flag, --require-etsi.
# So the 3GPP corpus was held to --require-fts, --require-hnsw,
# --require-embed-complete and --require-sparse, and the ETSI corpus — the same
# size, the same writers, the same freeze — was held to none of them. An ETSI FTS
# index that failed to build, or a sparse layer that came out empty, could not fail
# a gate, because no gate looked. `validate-etsi` now runs the SAME contract on
# data/etsi.duckdb, and it asks this file for its flags rather than carrying a copy
# of them, because a second copy of the contract is a second contract.
#
# WHAT THE ETSI ARM DROPS, AND WHY IT IS THE ONLY DIFFERENCE. --require-etsi is not
# a check about a corpus; it is a check about the PAIR — it opens the peer and
# asserts its embedding identity equals this one's. Emitting it on both arms would
# run the same comparison twice and, on the ETSI arm, point the corpus at itself.
# It stays on the 3GPP arm, once, and that is the whole of the asymmetry.
#
# Env (CI repo variables / job env):
#   DATA_CONTRACT     dense | dense+sparse | dense+sparse+etsi   (default: dense+sparse+etsi)
#   DATA_EMBED_FLOOR  release floor for dense convergence, e.g. Rel-99 (default: all)
#
# RATCHET — ADVANCED 2026-09-07 to dense+sparse+etsi, both conditions now met.
#
# The original note said: keep dense until the first full sparse bake exists, then
# flip to dense+sparse; add +etsi once both gate binaries gain --require-etsi.
#
#   full sparse bake   3GPP 194 111 501 postings, ETSI 127 476 905, both at
#                      sparse_model=b13103bce7ae            (build 23, measured)
#   --require-etsi     declared by cmd/validate AND mcp-3gpp check-data
#
# WHY THE DEFAULT AND NOT THE CALLERS. The strong contract already existed and was
# already passed by hand in .local/resume/*.sh — but `make build`, the command that
# actually publishes, sourced only the toolchain prelude and therefore validated
# with the WEAK contract. Build 23 published a corpus whose sparse layer and whose
# entire ETSI half were never checked. A gate that exists but is not on the path
# that runs is the failure mode this file was written to prevent, so the strength
# belongs in the DEFAULT, where forgetting to pass it cannot silently weaken it.
#
# Loosening is still possible and still deliberate: DATA_CONTRACT=dense for a
# corpus that genuinely has no sparse layer yet.
set -euo pipefail

level="${DATA_CONTRACT:-dense+sparse+etsi}"
floor="${DATA_EMBED_FLOOR:-}"
arm="${1:-3gpp}"

case "$arm" in
3gpp | etsi) ;;
*)
	echo "data-contract: unknown arm '$arm' (want: 3gpp | etsi)" >&2
	exit 2
	;;
esac

flags="--require-fts --require-hnsw --require-embed-complete"
# THE FLOOR IS A 3GPP CONCEPT AND MUST NOT REACH THE ETSI ARM.
#
# --require-embed-complete counts clauses at or above --embed-floor, and
# clauses_needing_embedding skips any clause whose release has no ordinal once a
# floor is set. An ETSI release is the constant "ETSI", which has no ordinal, so a
# floor here would select ZERO clauses and the strongest check in the contract
# would pass over an entirely unvectorised corpus. That is the same trap
# corpusETSI().Floor already documents on the embed side, one gate later.
if [ -n "$floor" ] && [ "$arm" = 3gpp ]; then
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
	#
	# ONLY ON THE 3GPP ARM. See the header: this is the one check about the pair
	# rather than about a corpus, and the ETSI arm would be pointing it at itself.
	# The ETSI arm still gets --require-sparse, which is a check about a corpus and
	# which nothing used to ask of it.
	flags="$flags --require-sparse"
	if [ "$arm" = 3gpp ]; then
		flags="$flags --require-etsi ${DATA_ETSI_DB:-/data/mcp-3gpp/etsi.duckdb}"
	fi
	;;
*)
	echo "data-contract: unknown DATA_CONTRACT=$level (want: dense | dense+sparse | dense+sparse+etsi)" >&2
	exit 2
	;;
esac

printf '%s\n' "$flags"
