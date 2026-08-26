#!/usr/bin/env bash
# check-duckdb-pin.sh — guard that the Rust and Go sides use the SAME DuckDB engine.
#
# The migration has Rust WRITE the .duckdb (duckdb-rs) and Go READ it back
# (go-duckdb). They MUST share the on-disk storage format, which means the same
# DuckDB engine LINE (major.minor).
#
# WHY THIS SCRIPT WAS REWRITTEN (2026-08-23)
#
# The previous version compared DECLARATIONS, not resolutions, and therefore
# validated a drift instead of catching it:
#
#   - it grepped `duckdb = { version = "1.4"` out of rust/store/Cargo.toml and
#     concluded "Rust is on 1.4.x". But `"1.4"` is a CARET requirement in Cargo
#     (>=1.4.0, <2.0.0), and the duckdb crate has since moved to a 1.<MM><PP>.0
#     numbering where 1.10504.0 sorts far above 1.4.x. rust/Cargo.lock actually
#     resolves duckdb 1.10504.0 — DuckDB 1.5.4.
#   - it inferred the Go engine from the go-duckdb version in a comment
#     ("v2.4.3 bundles engine 1.4.3"), which is not where the engine comes from.
#     The engine is decided by github.com/duckdb/duckdb-go-bindings, and go.mod
#     pins that root at v0.10503.0 — DuckDB 1.5.3.
#
# So both sides had moved to the 1.5 line while the guard printed "pin OK: both
# on DuckDB 1.4.x". A guard that cannot fail is worse than no guard: it converts
# an open question into a false assurance.
#
# This version reads the LOCK and the MODULE GRAPH — the values that actually
# reach the compiler.
#
# The authoritative proof of compatibility remains the runtime round-trip (a Go
# process opening a Rust-written fixture, TestPhase0RustGoRoundTrip). This is the
# cheap, fast drift guard in front of it.
set -euo pipefail

# duckdb_line <version> — map a crate/module version onto its DuckDB major.minor.
#
# Two numbering schemes are in the wild:
#   1.4.3        classic semver, DuckDB 1.4.x            (crate)
#   v0.1.24      old bindings scheme, table in the        (bindings, DuckDB <=1.4.3)
#                bindings README maps it to DuckDB 1.4.3
#   1.10504.0    DuckDB 1.5.4  — "1", then MM=05, PP=04   (crate, since DuckDB 1.5)
#   v0.10503.0   DuckDB 1.5.3  — same digits after "0."   (bindings, since DuckDB 1.5)
duckdb_line() {
	local v="${1#v}"
	local mid="${v#*.}"
	mid="${mid%%.*}" # middle component
	case "$v" in
	0.1.* | 0.2.* | 0.3.*)
		# Old bindings scheme. The mapping lives in the bindings README; every
		# tag in this family targets the 1.4 line or older. We only need the
		# line, and v0.1.19+ is 1.4.x.
		echo "1.4"
		;;
	*)
		if [ "${#mid}" -ge 5 ]; then
			# Middle component is 1MMPP: a leading "1" (the DuckDB major), then a
			# two-digit minor, then a two-digit patch. The bindings README states
			# the anchor: DuckDB v1.5.0 <-> bindings v0.10500.0, i.e. 1|05|00.
			printf '1.%d\n' "$((10#${mid:1:2}))"
		else
			printf '1.%s\n' "$mid"
		fi
		;;
	esac
}

fail() {
	echo "::error::$*" >&2
	exit 1
}

# --- Rust: the LOCK is the pin, not the manifest --------------------------------
[ -f rust/Cargo.lock ] || fail "rust/Cargo.lock is missing — the Rust DuckDB version is unpinned, so this guard cannot mean anything"
rust_crate="$(awk '/^name = "duckdb"$/{getline; if ($0 ~ /^version = /) {gsub(/[",]/,"",$3); print $3; exit}}' rust/Cargo.lock)"
[ -n "$rust_crate" ] || fail "could not read the resolved duckdb crate version from rust/Cargo.lock"
rust_line="$(duckdb_line "$rust_crate")"

# --- Go: the engine comes from the bindings ROOT, not from go-duckdb -------------
go_bindings="$(grep -oE 'github\.com/duckdb/duckdb-go-bindings v[0-9.]+' go.mod | head -1 | grep -oE 'v[0-9.]+' || true)"
[ -n "$go_bindings" ] || fail "could not read github.com/duckdb/duckdb-go-bindings from go.mod"
go_line="$(duckdb_line "$go_bindings")"

echo "Rust  rust/Cargo.lock   duckdb crate $rust_crate        -> DuckDB $rust_line"
echo "Go    go.mod            duckdb-go-bindings $go_bindings -> DuckDB $go_line"

if [ "$rust_line" != "$go_line" ]; then
	fail "DuckDB engine drift: Rust writes with $rust_line, Go reads with $go_line. A file written by one may be unreadable — or silently upgraded — by the other. Align rust/store/Cargo.toml + rust/Cargo.lock with go.mod's duckdb-go-bindings, then re-run the Rust-writes/Go-reads round-trip."
fi

# --- Platform modules must not straddle two lines --------------------------------
#
# duckdb-go-bindings/lib/<plat> carries the static libraries linked when building
# WITHOUT -tags duckdb_use_lib. If one sits on a different line than the bindings
# root — which supplies the headers — the static build links one engine's objects
# against another engine's declarations.
#
# ONLY lib/. The legacy duckdb-go-bindings/<plat> modules are a SEPARATE family
# with its own version scheme: its newest release is v0.1.24 while the root and
# lib/ moved to v0.105xx, and no v0.10503.0 of it exists to "align" to. Matching
# both families here read v0.1.24 as "DuckDB 1.4 straddling a 1.5 root" and
# failed CI on a module go-duckdb v2 no longer links for its engine — while
# advising a `go get` that cannot resolve. The authority on whether the engines
# actually agree is the round-trip test (TestPhase0RustGoRoundTrip), which passes
# with this exact go.mod; this script is the cheap drift guard in front of it,
# and a cheap guard that cries wolf gets switched off.
plat_versions="$(grep -oE 'github\.com/duckdb/duckdb-go-bindings/lib/[a-z0-9-]+ v[0-9.]+' go.mod | grep -oE 'v[0-9.]+$' | sort -u || true)"
if [ -n "$plat_versions" ]; then
	while read -r pv; do
		[ -n "$pv" ] || continue
		pl="$(duckdb_line "$pv")"
		if [ "$pl" != "$go_line" ]; then
			fail "prebuilt libs duckdb-go-bindings/lib/* are at $pv (DuckDB $pl) while the bindings root ($go_bindings) targets $go_line. A static build (no -tags duckdb_use_lib) would link $pl objects against $go_line headers. Align them: 'go get github.com/duckdb/duckdb-go-bindings/lib/...@$go_bindings && go mod tidy'."
		fi
	done <<<"$plat_versions"
fi

echo "pin OK: Rust and Go both on the DuckDB $go_line line"
