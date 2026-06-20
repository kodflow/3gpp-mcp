#!/usr/bin/env bash
#
# etsi-corpus.sh — build the ETSI corpus the SAME way scripts/corpus.sh builds the
# 3GPP one ("process identique"): discover the work-list, fetch each deliverable,
# convert it to HTML, then ingest. The ONLY ETSI specifics are the source (the ETSI
# /deliver archive, PDF) and the conversion (PDF text-layer, not DOC/DOCX) — both
# isolated here; ingest/htmlparse/store/merge are reused verbatim.
#
# Flow (resumable + idempotent, mirrors corpus.sh):
#   1. cmd/discover-etsi --emit-worklist  -> "<id>\t<pdf-url>\t<version>" lines
#   2. per line: download the PDF (retry), convert_pdf -> HTML (text-layer, NO OCR),
#      PREPEND the "<!-- ETSI-SPEC: <id> | <version> -->" provenance header so
#      htmlparse attributes it. Skip if the HTML already exists (resume).
#   3. cmd/ingest --etsi --resume  -> clauses + FTS into the ETSI DuckDB.
#
# Env: ETSI_SPECS (override discover scope), ETSI_OUT (DB, default data/etsi.duckdb),
#      ETSI_CONVERT (default data/sources/convert-etsi), ETSI_INDEX (etsi-index.json).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/convert.sh
source "$ROOT/scripts/lib/convert.sh"

OUT="${ETSI_OUT:-$ROOT/data/etsi.duckdb}"
CONVERT="${ETSI_CONVERT:-$ROOT/data/sources/convert-etsi}"
ORIGIN="${ETSI_ORIGIN:-$ROOT/data/sources/etsi-origin}"
INDEX="${ETSI_INDEX:-}"
BUCKET="$CONVERT/ETSI" # ingest globs <convert>/*/*.html
mkdir -p "$BUCKET" "$ORIGIN"

retry() { local n=0; until "$@"; do n=$((n + 1)); [ "$n" -ge 5 ] && return 1; sleep $((n * 3)); done; }

echo "[etsi] building tools…"
go build -o "$ROOT/bin/discover-etsi" ./cmd/discover-etsi
# ingest is the RUST bin (parse3gpp + store-rs; --etsi mode). libduckdb is bundled.
command -v cargo >/dev/null 2>&1 || curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
PATH="$HOME/.cargo/bin:$PATH" cargo build --release --manifest-path "$ROOT/rust/ingest/Cargo.toml" --bin ingest
cp "$ROOT/rust/target/release/ingest" "$ROOT/bin/ingest"

echo "[etsi] discovering work-list…"
wl="$(mktemp)"
disc_args=(--emit-worklist)
[ -n "$INDEX" ] && disc_args+=(--index "$INDEX")
[ -n "${ETSI_SPECS:-}" ] && disc_args+=(--specs "$ETSI_SPECS")
# ETSI_ALL=1 → enumerate the WHOLE /deliver corpus (etsi_ts+tr+en), not just the LI
# suite (3GPP-parity completeness). Mutually exclusive with ETSI_SPECS in practice.
[ -n "${ETSI_ALL:-}" ] && disc_args+=(--all)
[ -n "${ETSI_TYPE_DIRS:-}" ] && disc_args+=(--type-dirs "$ETSI_TYPE_DIRS")
"$ROOT/bin/discover-etsi" "${disc_args[@]}" >"$wl" || { echo "::error::discover-etsi failed"; exit 1; }
n_total=$(wc -l <"$wl" | tr -dc '0-9'); n_total=${n_total:-0}
echo "[etsi] work-list: ${n_total} deliverable(s) to (re)fetch"

i=0
ok=0
fail=0
# IFS=tab so the id's internal space ("103 221-1") survives; never `for x in $var` (zsh).
while IFS=$'\t' read -r id url version; do
	[ -n "$id" ] || continue
	i=$((i + 1))
	safe="${id// /_}_v${version}"
	target="$BUCKET/${safe}.html"
	printf '[etsi] (%d/%d) %s v%s\n' "$i" "$n_total" "$id" "$version"
	if [ -s "$target" ]; then
		echo "  ✓ already converted (resume)"
		ok=$((ok + 1))
		continue
	fi
	pdf="$(mktemp --suffix=.pdf)"
	# ETSI's /deliver CDN WAF 403s a bare curl User-Agent from datacenter IPs (GitHub
	# Actions): discover-etsi works on the same runner ONLY because it sends a browser
	# UA. Mirror that here (+ Accept/timeout) or every PDF download fails in CI.
	if ! retry curl -fsSL --connect-timeout 20 --max-time 180 \
		-A "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36" \
		-H "Accept: application/pdf,*/*" \
		-o "$pdf" "$url"; then
		echo "::warning::download failed: $url"
		rm -f "$pdf"
		fail=$((fail + 1))
		continue
	fi
	tmp_html="$(mktemp --suffix=.html)"
	if convert_pdf "$pdf" "$tmp_html" "$id v$version"; then
		# Prepend the provenance header htmlparse keys on, then the converted body.
		{
			printf '<!-- ETSI-SPEC: %s | %s -->\n' "$id" "$version"
			cat "$tmp_html"
		} >"$target"
		echo "  ✓ converted ($(wc -c <"$target" | tr -dc '0-9') bytes)"
		ok=$((ok + 1))
	else
		echo "::warning::convert failed (no text layer?): $id v$version"
		fail=$((fail + 1))
	fi
	rm -f "$pdf" "$tmp_html"
done <"$wl"
rm -f "$wl"
echo "[etsi] converted=${ok} failed=${fail}"

echo "[etsi] ingesting…"
"$ROOT/bin/ingest" --etsi --convert "$CONVERT" --origin "$ORIGIN" --db "$OUT" --resume
echo "[etsi] done: $OUT"
