#!/usr/bin/env bash
# Offline check: EVERY Rust crate is rustfmt-clean, including the ones the
# workspace excludes.
# Run: bash scripts/rust-fmt_test.sh
#
# Why this exists: `cargo fmt --all` covers the WORKSPACE, and rust/Cargo.toml
# excludes embedder, embed-core and discover on purpose (heavy ort/CUDA toolchain,
# a cdylib, and a CI-matrix tool). CI checks them one by one in separate jobs, so
# the local command was strictly narrower than the gate it was meant to satisfy:
# two pull-request rounds were burnt discovering, one crate at a time, that
# "cargo fmt --all says clean" did not mean "CI will be green".
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# "I cannot measure" is not "the condition holds". A missing rustfmt is reported
# and skips the run rather than printing a green line nobody should trust.
if ! command -v cargo >/dev/null 2>&1; then
	echo "SKIP  cargo is not on PATH — cannot check Rust formatting"
	exit 0
fi
if ! cargo fmt --version >/dev/null 2>&1; then
	echo "SKIP  rustfmt is not installed (rustup component add rustfmt) — cannot check"
	exit 0
fi

found=0
while IFS= read -r manifest; do
	crate="$(basename "$(dirname "$manifest")")"
	found=$((found + 1))
	if cargo fmt --manifest-path "$manifest" --check >/dev/null 2>&1; then
		pass "$crate is rustfmt-clean"
	else
		fail "$crate is not rustfmt-clean — run: cargo fmt --manifest-path $manifest"
	fi
done < <(find "$ROOT/rust" -mindepth 2 -maxdepth 2 -name Cargo.toml | sort)

# A loop that silently iterates nothing is the failure mode this whole file is
# about, so the count is asserted too.
if [ "$found" -ge 5 ]; then
	pass "checked $found crate(s), workspace members and excluded alike"
else
	fail "only $found crate(s) found under rust/ — the search is wrong, not the tree"
fi

[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all good"
