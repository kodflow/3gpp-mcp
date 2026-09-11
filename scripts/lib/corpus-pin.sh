#!/usr/bin/env bash
# corpus-pin.sh — rewrite ONE package's line of contracts/corpus-pin.txt.
#
# The pin names, by digest, the corpus snapshot each pipeline arm seeds from
# (`seed` / `seed-etsi`, internal/goal/seed_pin.go). Its one writer is
# scripts/local/publish-corpus.sh, right after a push: the thing that creates a new
# snapshot is the thing that says which one a fresh clone should start from. It is
# a library so that scripts/lib/corpus-pin_test.sh can exercise the real
# definition, and internal/goal checks that what it writes is what the seed reads.
#
# Format: one `ghcr.io/<owner>/<package>@sha256:<64 lowercase hex>` per line;
# blank lines and `#` comments are kept byte for byte.
#
# PURE BASH, no sed/awk: under scripts/local/toolchain-env.sh the PATH's sed and
# awk are w64devkit's BusyBox builds, which is how a script that "works" in one
# shell stops working inside the pipeline.

# pin_corpus_snapshot FILE PACKAGE REF
#   Replace the line of FILE that pins PACKAGE with REF. Refuses — and leaves FILE
#   untouched — when REF is not a digest reference of PACKAGE on ghcr.io, or when
#   FILE does not hold exactly one line for PACKAGE. A CRLF line keeps its CR.
pin_corpus_snapshot() {
	local file="$1" pkg="$2" ref="$3"
	local rest="${ref#ghcr.io/}"
	local repo="${rest%%@*}"
	local owner="${repo%%/*}"
	local hex="${ref##*@sha256:}"

	if [ "$rest" = "$ref" ] || [ -z "$owner" ] || [ "$repo" != "$owner/$pkg" ] ||
		[ "$ref" != "ghcr.io/$repo@sha256:$hex" ]; then
		printf 'pin: %s is not ghcr.io/<owner>/%s@sha256:<digest> — a pin must name a digest of that package\n' "$ref" "$pkg" >&2
		return 1
	fi
	if [ "${#hex}" -ne 64 ] || [[ "$hex" == *[!0-9a-f]* ]]; then
		printf 'pin: %s — want 64 lowercase hex characters after sha256:\n' "$ref" >&2
		return 1
	fi
	if [ ! -f "$file" ]; then
		printf 'pin: %s does not exist\n' "$file" >&2
		return 1
	fi

	local out="" line bare cr found=0
	while IFS= read -r line || [ -n "$line" ]; do
		bare="${line%$'\r'}"
		cr="${line:${#bare}}"
		if [[ "$bare" == ghcr.io/*/"$pkg"@* ]]; then
			found=$((found + 1))
			out+="$ref$cr"$'\n'
		else
			out+="$line"$'\n'
		fi
	done <"$file"
	if [ "$found" -ne 1 ]; then
		printf 'pin: %s has %d line(s) for %s, want exactly 1 — not rewriting it\n' "$file" "$found" "$pkg" >&2
		return 1
	fi
	local tmp="$file.tmp.$$"
	printf '%s' "$out" >"$tmp" && mv -f "$tmp" "$file"
}
