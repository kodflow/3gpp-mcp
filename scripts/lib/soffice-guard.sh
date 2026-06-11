#!/usr/bin/env bash
#
# soffice-guard.sh — conversion-determinism guard (ADVISORY, never blocking).
#
# WHY: LibreOffice is installed unpinned from the runner's apt (hard-pinning a
# soffice version on a GitHub Ubuntu runner is fragile and was deliberately
# rejected). The ingest already absorbs formatting drift — collapse() folds
# nbsp/tabs/newlines/space-runs into single spaces before hashing — so only a
# LibreOffice change that alters real CONTENT/STRUCTURE (entity decoding, table
# extraction) can move the text hash, and that is rare and a genuine diff. This
# guard makes such a toolchain bump VISIBLE in the run (a GitHub warning, plus
# the exact version in the log) instead of silent, which is the whole residual
# risk. Bump SOFFICE_EXPECTED_PREFIX deliberately: validate the new version
# with scripts/convert-smoke.sh first, then change the pin in a dedicated PR.
#
# NOT in scripts/lib/convert.sh on purpose: convert.sh is part of the convert
# cache key in corpus-matrix.yml — touching it re-converts the whole corpus.
# This file is outside that key, so the guard can evolve at zero convert cost.
SOFFICE_EXPECTED_PREFIX="LibreOffice 24.2"

v="$(soffice --version 2>/dev/null | head -1 || true)"
echo "soffice: ${v:-<missing>} (pinned line: $SOFFICE_EXPECTED_PREFIX)"
case "$v" in
  "$SOFFICE_EXPECTED_PREFIX"*)
    echo "conversion toolchain matches the pinned line"
    ;;
  *)
    msg="LibreOffice drifted from the pinned line ('$SOFFICE_EXPECTED_PREFIX' -> '${v:-missing}'). Whitespace drift is absorbed by ingest normalization; a real extraction change surfaces as text-hash diffs (re-converted + re-embedded clauses). Validate scripts/convert-smoke.sh on the new version, then bump SOFFICE_EXPECTED_PREFIX in scripts/lib/soffice-guard.sh."
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
      echo "::warning::$msg"
    else
      echo "WARNING: $msg" >&2
    fi
    ;;
esac
exit 0
