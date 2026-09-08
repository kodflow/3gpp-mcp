#!/usr/bin/env bash
#
# etsi-fetch.sh — discover the ETSI work-list, then download and convert every
# deliverable to HTML. It does NOT touch the corpus DB; that is etsi-ingest.sh.
#
# WHY THIS IS ITS OWN SCRIPT. It used to be the first two thirds of
# etsi-corpus.sh, which meant ONE pipeline step declared both the downloader and
# the Rust parser as its provenance. A change to rust/store/src/lib.rs — which
# cannot alter a single downloaded byte — therefore re-ran the whole ETSI half.
# Measured on build 24 (2026-09-07):
#
#     STEP corpus-etsi
#       reason  implementation changed: rust/store/src/lib.rs
#
# The 3GPP side has had `fetch` and `ingest` as separate steps from the start;
# this is the ETSI half catching up, not a new idea.
#
# AND IT IS A WORKER POOL NOW. The loop was strictly sequential: one curl, one
# pdftotext, then the next, for as many as 7 501 deliverables. Downloads are
# network-bound and pdftotext is a small short-lived process, so this is the one
# place in the ETSI chain where parallelism is free. Every other heavy step is
# bounded by the 16 GB DuckDB writer cap on a 28 GB machine, so two of THOSE can
# never overlap — measured before writing any of this, because a scheduler that
# memory forbids would have been a week spent on nothing.
#
# Flow (resumable + idempotent, mirrors scripts/corpus.sh):
#   1. cmd/discover-etsi --emit-worklist  -> "<id>\t<pdf-url>\t<version>\t<type>"
#   2. per line, in parallel: download the PDF (retry), convert_pdf -> HTML
#      (text-layer, NO OCR), PREPEND the "<!-- ETSI-SPEC: id | version | type -->"
#      provenance header so htmlparse attributes it. Skip if the HTML exists.
#
# Env: ETSI_SPECS, ETSI_ALL, ETSI_ALL_VERSIONS, ETSI_INCLUDE_3GPP, ETSI_TYPE_DIRS,
#      ETSI_CONVERT, ETSI_INDEX, ETSI_JOBS.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/etsi-common.sh
source "$ROOT/scripts/lib/etsi-common.sh"
# convert_pdf is used HERE and nowhere else, so it is sourced here and not in the
# shared prelude — the ingest must not inherit a dependency it never calls.
# shellcheck source=scripts/lib/convert.sh
source "$ROOT/scripts/lib/convert.sh"

etsi_resolve_bins discover

# JOBS: 8, this machine's logical core count.
#
# The 3GPP fetch runs 6 because LibreOffice is RAM-heavy and measured SLOWER above
# that ("4 workers convert 4.9/min, 6 workers 2.4/min"). pdftotext is not
# LibreOffice: it is a short-lived process on one PDF, so the ceiling here is cores
# for the convert and the ETSI CDN for the download, not memory. Overridable,
# because the right number on another machine is a different number.
JOBS="${ETSI_JOBS:-8}"

echo "[etsi] discovering work-list…"
wl="$(mktemp)"
disc_args=(--emit-worklist)
[ -n "${ETSI_INDEX:-}" ] && disc_args+=(--index "$ETSI_INDEX")
[ -n "${ETSI_SPECS:-}" ] && disc_args+=(--specs "$ETSI_SPECS")
# ETSI_ALL=1 → enumerate the WHOLE /deliver corpus (etsi_ts+tr+en), not just the LI
# suite (3GPP-parity completeness). Mutually exclusive with ETSI_SPECS in practice.
[ -n "${ETSI_ALL:-}" ] && disc_args+=(--all)
# ETSI_INCLUDE_3GPP=1 → also take ETSI's republications of 3GPP specs. Off by
# default because the 3GPP half of this corpus already holds those in EVERY
# release, while the ETSI archive publishes one version of each.
[ -n "${ETSI_INCLUDE_3GPP:-}" ] && disc_args+=(--include-3gpp-republications)
# ETSI_ALL_VERSIONS=1 → every PUBLISHED version of each deliverable, not just the
# latest: the ETSI analogue of keeping every 3GPP release. Multiplies the work
# list several-fold (TS 103 221-1 alone has 23 published versions).
[ -n "${ETSI_ALL_VERSIONS:-}" ] && disc_args+=(--all-versions)
[ -n "${ETSI_TYPE_DIRS:-}" ] && disc_args+=(--type-dirs "$ETSI_TYPE_DIRS")
"$DISCOVER_ETSI_BIN" "${disc_args[@]}" >"$wl" || { echo "::error::discover-etsi failed"; exit 1; }
n_total=$(wc -l <"$wl" | tr -dc '0-9'); n_total=${n_total:-0}
echo "[etsi] work-list: ${n_total} deliverable(s) to (re)fetch (jobs=$JOBS)"

# THE TALLY IS FILES, NOT VARIABLES. Workers are separate processes, so ok=$((ok+1))
# in one of them is invisible to the parent: the counters would have read 0 forever
# and the "converted=N failed=M" line would have been a confident lie. One empty
# file per outcome, counted at the end, is the portable way to add up across
# processes — and this is the first thing the sequential loop could take for
# granted that the pool cannot.
TALLY="$(mktemp -d)"
mkdir -p "$TALLY/ok" "$TALLY/fail"
export TALLY BUCKET ROOT

fetch_one() {
	local line="$1" id url version doctype safe target legacy pdf tmp_html
	IFS=$'\t' read -r id url version doctype <<<"$line"
	[ -n "$id" ] || return 0
	# A TS and a TR can share a number (103 101 is a TR; the TS tree 404s on it),
	# so the document type is part of the filename or the two would overwrite each
	# other in the same bucket.
	#
	# The default is applied per line and never inherited: `read` leaves an absent
	# trailing field EMPTY, and in the old sequential loop a value set on a previous
	# iteration would otherwise survive and label a TS as a TR. Each worker now
	# handles one line in its own process, which removes that hazard by construction
	# rather than relying on the default to paper over it.
	doctype="${doctype:-TS}"
	safe="${doctype}_${id// /_}_v${version}"
	target="$BUCKET/${safe}.html"
	# MIGRATE, do not duplicate. Every file converted before the type prefix existed
	# is named without one, so the resume check below would miss it, the PDF would be
	# downloaded and converted again under the new name, AND the old file would still
	# be sitting in the bucket for the ingest to read — one deliverable, twice, from
	# two files that disagree about nothing. Renaming is exact rather than a guess:
	# the untyped scheme only ever produced TS.
	legacy="$BUCKET/${id// /_}_v${version}.html"
	if [ "$doctype" = "TS" ] && [ ! -e "$target" ] && [ -s "$legacy" ]; then
		mv "$legacy" "$target" 2>/dev/null && echo "  ↻ migrated the untyped cache entry: $id"
	fi
	if [ -s "$target" ]; then
		: >"$TALLY/ok/$$.$RANDOM"
		return 0
	fi
	printf '[etsi] %s v%s (%s)\n' "$id" "$version" "$doctype"
	pdf="$(tmpfile_ext pdf)" || { : >"$TALLY/fail/$$.$RANDOM"; return 0; }
	# ETSI's /deliver CDN WAF 403s a bare curl User-Agent from datacenter IPs (GitHub
	# Actions): discover-etsi works on the same runner ONLY because it sends a browser
	# UA. Mirror that here (+ Accept/timeout) or every PDF download fails in CI.
	if ! retry curl -fsSL --connect-timeout 20 --max-time 180 \
		-A "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36" \
		-H "Accept: application/pdf,*/*" \
		-o "$pdf" "$url"; then
		echo "::warning::download failed: $url"
		rm -f "$pdf"
		: >"$TALLY/fail/$$.$RANDOM"
		return 0
	fi
	tmp_html="$(tmpfile_ext html)" || { rm -f "$pdf"; : >"$TALLY/fail/$$.$RANDOM"; return 0; }
	if convert_pdf "$pdf" "$tmp_html" "$id v$version"; then
		# WRITE THEN RENAME. The old loop wrote the provenance header and the body
		# straight into $target, so a kill between the two left a header-only file
		# that the resume check counts as done and the ingest reads as a deliverable
		# with no clauses. With workers running concurrently that window is entered
		# far more often, so the content lands on a temp name and the MOVE is what
		# publishes it.
		{
			printf '<!-- ETSI-SPEC: %s | %s | %s -->\n' "$id" "$version" "$doctype"
			cat "$tmp_html"
		} >"$target.part"
		mv "$target.part" "$target"
		echo "  ✓ $id v$version ($(wc -c <"$target" | tr -dc '0-9') bytes)"
		: >"$TALLY/ok/$$.$RANDOM"
	else
		echo "::warning::convert failed (no text layer?): $id v$version"
		: >"$TALLY/fail/$$.$RANDOM"
	fi
	rm -f "$pdf" "$tmp_html"
}
export -f fetch_one retry tmpfile_ext

# NUL-delimited, because an ETSI id CONTAINS A SPACE ("103 221-1"). Splitting the
# work list on whitespace would hand xargs two arguments for one deliverable and
# fetch a URL that does not exist. One line per worker invocation (-n1); the worker
# splits the tabs itself.
tr '\n' '\0' <"$wl" | xargs -0 -P "$JOBS" -n1 bash -c 'fetch_one "$0"' || true
rm -f "$wl"

ok=$(find "$TALLY/ok" -type f 2>/dev/null | wc -l | tr -dc '0-9')
fail=$(find "$TALLY/fail" -type f 2>/dev/null | wc -l | tr -dc '0-9')
rm -rf "$TALLY"
echo "[etsi] converted=${ok:-0} failed=${fail:-0}"
