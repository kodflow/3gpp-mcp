#!/usr/bin/env bash
#
# etsi-corpus.sh — fetch, then ingest, in one call.
#
# THIS IS A WRAPPER NOW. The work lives in scripts/etsi-fetch.sh and
# scripts/etsi-ingest.sh, which the pipeline drives as two separate steps so that
# each declares only its own provenance — the shape scripts/corpus.sh and the
# `ingest` step have always had on the 3GPP side.
#
# This entry point stays because "build the ETSI corpus" is one thing someone
# wants from a standalone or CI invocation, and because cmd/discover-etsi's own
# documentation points at this filename.
#
# Every ETSI_* variable is read by the two halves; see their headers.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bash "$ROOT/scripts/etsi-fetch.sh"
bash "$ROOT/scripts/etsi-ingest.sh"
