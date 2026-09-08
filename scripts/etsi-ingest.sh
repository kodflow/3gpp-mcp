#!/usr/bin/env bash
#
# etsi-ingest.sh — parse the converted ETSI HTML into data/etsi.duckdb.
#
# The second half of what used to be etsi-corpus.sh. It is separate so that the
# `ingest-etsi` pipeline step declares the Rust chain and NOTHING ELSE: before the
# split one step's provenance covered both the downloader and the parser, so a
# change to either re-ran both.
#
# `ingest --etsi` IS the publish step on this side — there is no merge — so it
# writes straight into the corpus DB and rebuilds the clause indexes itself.
#
# Env: ETSI_OUT (DB, default data/etsi.duckdb), ETSI_CONVERT, ETSI_ORIGIN.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/etsi-common.sh
source "$ROOT/scripts/lib/etsi-common.sh"

etsi_resolve_bins ingest

# A CONVERT DIRECTORY WITH NO HTML IS A BUG, NOT AN EMPTY CORPUS. Reaching the
# ingest with nothing to read means the fetch produced nothing and said it
# succeeded; ingesting zero files leaves a schema-only DB that serves as an empty
# corpus without complaining. The step's Validate catches that shape too, but
# failing HERE names the actual cause instead of the symptom two steps later.
n_html=$(find "$BUCKET" -name '*.html' -type f 2>/dev/null | wc -l | tr -dc '0-9')
if [ "${n_html:-0}" -eq 0 ]; then
	echo "::error::no converted ETSI HTML under $BUCKET — run scripts/etsi-fetch.sh first"
	exit 1
fi

echo "[etsi] ingesting ${n_html} converted deliverable(s)…"
"$INGEST_BIN" --etsi --convert "$CONVERT" --origin "$ORIGIN" --db "$OUT" --resume
echo "[etsi] done: $OUT"
