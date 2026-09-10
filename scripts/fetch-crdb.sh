#!/usr/bin/env bash
#
# fetch-crdb.sh — bring the 3GPP Change Request database into the local corpus so
# `enrich` can give the `changes` table a writer instead of serving a fossil.
#
# 3GPP publishes the whole CR database as ONE file, re-exported every few months:
#
#   /ftp/Information/Databases/Change_Request/CRDB_<YYYYMMDD>.zip
#     └─ CRDB_<YYYYMMDD>.xlsx        <- 596 696 change requests, 18 columns
#
# Layout produced:
#   data/sources/crdb/CRDB_<YYYYMMDD>.zip
#
# THE DATE IS READ FROM THE DIRECTORY LISTING, NEVER GUESSED OR PINNED. The export
# is republished under a new name each time, so a hardcoded URL would 404 the day
# 3GPP refreshes it — and a fetch that 404s here degrades to "the changelog kept
# whatever it had", which is exactly the kind of silent staleness this repository
# has paid for twice. Taking the newest name that the listing itself offers means
# the failure mode is a loud one.
#
# "Degrade, don't block" (corpus.sh doctrine): no network, or a listing that
# carries no CRDB_*.zip, warns and returns non-zero — enrich logs it and continues
# with whatever is already on disk, because an offline machine must still finish
# the run.
#
#   ./scripts/fetch-crdb.sh          # newest export, skipped when already held
#   OVERLAY_REFRESH=1 ./scripts/fetch-crdb.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# shellcheck source=/dev/null
source "$ROOT/scripts/lib/retry.sh"

BASE="https://www.3gpp.org/ftp/Information/Databases/Change_Request"
DEST="data/sources/crdb"
# 3gpp.org 403s a default curl User-Agent — the same browser string discover.sh
# uses, for the same reason.
UA="Mozilla/5.0 (X11; Linux x86_64) discover"

mkdir -p "$DEST"

listing="$(retry curl -fsSL -A "$UA" "$BASE/" 2>/dev/null || true)"
if [ -z "$listing" ]; then
    echo "fetch-crdb: could not read $BASE/ — leaving the changelog overlay as it is" >&2
    exit 1
fi

# PINNING, AND WHY IT IS NOT THE DEFAULT.
#
# scripts/fetch-5g-apis.sh pins an immutable commit SHA per release, and it is
# right to: a release's API set is finished, so a moving reference there would
# only ever mean drift. The CR database is the opposite — ONE rolling export,
# re-published every few months, whose whole value is that it is current. Pinning
# it by default would freeze the changelog at whatever date was committed and
# nothing would say so; the corpus would keep answering "no change request
# recorded" for every CR raised since, which is the failure this work exists to
# remove.
#
# Reproducibility is kept where it actually has to hold: `ingest-crs` stamps
# `changes_source` INTO the corpus, so every published image names the export it
# was built from, and CRDB_VERSION reproduces that build exactly.
#
#   CRDB_VERSION=CRDB_20260715.zip ./scripts/fetch-crdb.sh
#
if [ -n "${CRDB_VERSION:-}" ]; then
    newest="$CRDB_VERSION"
    if ! printf '%s' "$listing" | grep -qF "$newest"; then
        echo "fetch-crdb: $BASE/ no longer carries $newest — 3GPP keeps a limited window of exports; drop CRDB_VERSION to take the newest" >&2
        exit 1
    fi
else
    # Newest by NAME, which is a date stamp — CRDB_20260715.zip sorts after
    # CRDB_20260311.zip. sort -V rather than sort so a future four-digit day or a
    # suffix does not reorder them lexically.
    newest="$(printf '%s' "$listing" |
        grep -oE 'CRDB_[0-9]{8}\.zip' |
        sort -Vu |
        tail -1)"
fi

if [ -z "$newest" ]; then
    echo "fetch-crdb: $BASE/ carries no CRDB_<date>.zip — the export may have been renamed" >&2
    exit 1
fi

out="$DEST/$newest"
if [ -s "$out" ] && [ "${OVERLAY_REFRESH:-0}" != "1" ]; then
    echo "fetch-crdb: $newest already held ($(wc -c <"$out") bytes)"
    exit 0
fi

echo "fetch-crdb: $newest"
# Download beside the target and rename, so an interrupted transfer never leaves a
# truncated zip that the next run would treat as "already held" and hand to the
# parser.
tmp="$out.part"
retry curl -fsSL -A "$UA" --max-time 900 -o "$tmp" "$BASE/$newest"
mv -f "$tmp" "$out"

# Older exports are kept out of the way rather than deleted: ingest-crs takes the
# newest, and a superseded copy sitting beside it would silently double the
# directory every quarter.
find "$DEST" -maxdepth 1 -name 'CRDB_*.zip' ! -name "$newest" -print -delete >&2 || true

echo "fetch-crdb: $out ($(wc -c <"$out") bytes)"
