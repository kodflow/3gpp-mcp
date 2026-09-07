#!/usr/bin/env bash
#
# etsi-common.sh — the prelude scripts/etsi-fetch.sh and scripts/etsi-ingest.sh
# share. SOURCEABLE ONLY; it runs nothing on its own.
#
# It exists because those two scripts were ONE script until the fetch and the
# ingest were given separate pipeline steps. Everything below was already common
# to both halves; duplicating it into each would have been the usual way for two
# copies of a path default to drift apart.

# convert.sh is deliberately NOT sourced here. Only the fetch calls convert_pdf,
# and sourcing it from the shared prelude would make the ingest depend on it —
# which would put scripts/lib/convert.sh back into the ingest step's provenance
# and recreate exactly the false positive this split removes.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

OUT="${ETSI_OUT:-$ROOT/data/etsi.duckdb}"
CONVERT="${ETSI_CONVERT:-$ROOT/data/sources/convert-etsi}"
ORIGIN="${ETSI_ORIGIN:-$ROOT/data/sources/etsi-origin}"
INDEX="${ETSI_INDEX:-}"
BUCKET="$CONVERT/ETSI" # ingest globs <convert>/*/*.html
mkdir -p "$BUCKET" "$ORIGIN"

retry() { local n=0; until "$@"; do n=$((n + 1)); [ "$n" -ge 5 ] && return 1; sleep $((n * 3)); done; }

# tmpfile_ext <ext> — a temp file that actually ends in ".<ext>".
#
# `mktemp --suffix=` is GNU coreutils only. On Windows the toolchain's mktemp is
# w64devkit's BUSYBOX build, which takes nothing but a TEMPLATE ending in XXXXXX
# and exits 1 on --suffix — so the very first deliverable killed the whole step.
# The extension is not cosmetic here: convert_pdf dispatches on it, and pdftotext
# refuses a file it cannot recognise. Make the name ourselves and stay portable.
tmpfile_ext() {
	local t n
	t="$(mktemp)" || return 1
	# THE RENAME IS RETRIED, BECAUSE LOSING IT ONCE KILLS THE WHOLE RUN.
	#
	# Measured 2026-09-01 with four shards crawling into one temp directory:
	#
	#   mv: can't rename '…/Temp/tmp.a07236': Permission denied
	#
	# A Windows file lock (indexer, scanner, the other worker's mktemp) holds the
	# new file for a moment. The script runs under `set -e`, so that one lost
	# rename ended the shard — after 183 of 2 955 deliverables, and it then sat
	# dead for an hour while the other three ran on, because a dead shard and a
	# slow one look identical from the outside.
	#
	# This matters MORE now than it did: the fetch loop is a worker pool, so
	# several mktemp calls land in the same directory at the same instant.
	for n in 1 2 3 4 5; do
		if mv "$t" "$t.$1" 2>/dev/null; then
			printf '%s\n' "$t.$1"
			return 0
		fi
		sleep "$n"
	done
	rm -f "$t"
	return 1
}

# etsi_resolve_bins — binaries supplied by the caller, or built here as a fallback.
#
# internal/goal already builds every tool with the pinned local toolchain and knows
# where they live, so when it drives these scripts it passes them in. Building them
# again here would compile a SECOND copy with whatever toolchain happens to be on
# PATH — and the rustup bootstrap below would install a Rust the pipeline never
# chose. The fallback stays for a standalone/CI invocation.
#
# `need` says WHICH binaries this half actually uses, so the fetch does not build
# a Rust ingest it never runs and the ingest does not build a Go discoverer it
# never runs. That asymmetry is the whole point of the split.
etsi_resolve_bins() {
	local need="$1"
	DISCOVER_ETSI_BIN="${DISCOVER_ETSI_BIN:-}"
	INGEST_BIN="${INGEST_BIN:-}"
	case "$need" in
	discover)
		if [ -x "$DISCOVER_ETSI_BIN" ]; then
			echo "[etsi] using supplied binaries"
			return 0
		fi
		echo "[etsi] building tools…"
		go build -o "$ROOT/bin/discover-etsi" ./cmd/discover-etsi
		DISCOVER_ETSI_BIN="$ROOT/bin/discover-etsi"
		;;
	ingest)
		if [ -x "$INGEST_BIN" ]; then
			echo "[etsi] using supplied binaries"
			return 0
		fi
		echo "[etsi] building tools…"
		# ingest is the RUST bin (parse3gpp + store-rs; --etsi mode). libduckdb is bundled.
		command -v cargo >/dev/null 2>&1 || curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
		PATH="$HOME/.cargo/bin:$PATH" cargo build --release --manifest-path "$ROOT/rust/ingest/Cargo.toml" --bin ingest
		cp "$ROOT/rust/target/release/ingest" "$ROOT/bin/ingest"
		INGEST_BIN="$ROOT/bin/ingest"
		;;
	*)
		echo "etsi_resolve_bins: unknown need '$need'" >&2
		return 2
		;;
	esac
}
