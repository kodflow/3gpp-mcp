#!/usr/bin/env bash
# check-duckdb-pin.sh — guard that the Rust and Go sides pin the SAME DuckDB engine.
#
# The migration has Rust WRITE the .duckdb (duckdb-rs, bundled libduckdb) and Go READ it
# back (go-duckdb). They MUST share the on-disk storage format, which means the same
# DuckDB engine line. go-duckdb v2.4.3 bundles DuckDB engine v1.4.3; the Rust `duckdb`
# crate's 1.4.x line bundles DuckDB 1.4.x — storage-compatible.
#
# The authoritative proof is the runtime round-trip test (a separate Go process opening
# a Rust-written fixture — TestPhase0RustGoRoundTrip / the rust-go-roundtrip workflow).
# This script is a cheap, fast drift guard for CI.
set -euo pipefail

go_duckdb="$(grep -oE 'go-duckdb/v2 v[0-9.]+' go.mod | head -1 | grep -oE 'v[0-9]+\.[0-9.]+' || true)"
rust_duckdb="$(grep -oE 'duckdb = \{ version = "[0-9.]+"' rust/store/Cargo.toml | grep -oE '"[0-9.]+"' | tr -d '"' || true)"

echo "go-duckdb (go.mod):        ${go_duckdb:-?}   (v2.4.3 bundles DuckDB engine 1.4.3)"
echo "duckdb crate (rust/store): ${rust_duckdb:-?}"

[ -n "$rust_duckdb" ] || { echo "::error::could not read the duckdb crate version from rust/store/Cargo.toml"; exit 1; }

case "$rust_duckdb" in
  1.4 | 1.4.*) echo "pin OK: Rust + Go both on DuckDB 1.4.x" ;;
  *) echo "::error::DuckDB pin drift: rust duckdb crate='$rust_duckdb' but go-duckdb bundles engine 1.4.3 — re-align rust/store/Cargo.toml (and re-run the round-trip)"; exit 1 ;;
esac
