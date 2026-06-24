#!/usr/bin/env bash
# =============================================================================
#  scripts/ci/publish-vec.sh — publish a dense vector shard to GHCR 3gpp-vec.
# =============================================================================
#  Runs on the trusted CI RUNNER with the WRITE GHCR_PAT (the rented box never gets it).
#  Takes a directory holding the embedded shard (s<NN>.duckdb[.zst]) that scripts/ci/vastai.sh
#  pulled back, and publishes it ADDITIVELY to ghcr.io/<owner>/3gpp-vec:latest-fp16 — the
#  same read-pull-union-push contract corpus-rust-embed-kaggle.yml uses, extracted here so
#  both the Kaggle and vast.ai providers share ONE publisher.
#
#  Usage: scripts/ci/publish-vec.sh <shard-dir>
#  Env:   GHCR_PAT (write:packages), GHCR_OWNER.
#
#  KNOWN RACE (plan §6.3 / risk #3): the latest-fp16 manifest is read-union-push, NOT atomic.
#  Two concurrent publishers (e.g. a Kaggle cron + a vast.ai run) can last-writer-win and drop
#  a series. Mitigation IN THIS SCRIPT: also push an immutable per-series tag s<NN>-fp16 as a
#  race-free record. Do NOT run two providers against latest-fp16 concurrently until a
#  single-flight lock or per-series-tag-merge-at-bake lands.
# =============================================================================
set -euo pipefail

: "${GHCR_PAT:?write GHCR token required}"
GHCR_OWNER="${GHCR_OWNER:-kodflow}"
DIR="${1:?usage: publish-vec.sh <shard-dir>}"
MODEL="bge-m3-fp16"
TAG="latest-fp16"
REF="ghcr.io/${GHCR_OWNER}/3gpp-vec:${TAG}"

command -v oras >/dev/null 2>&1 || { curl -fsSL "https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz" | sudo tar -xz -C /usr/local/bin oras; }
command -v zstd >/dev/null 2>&1 || sudo apt-get install -y --no-install-recommends zstd jq

# Locate the shard DB (accept s<NN>.duckdb or its .zst, at any depth under DIR).
RAW="$(find "$DIR" -name 's[0-9][0-9].duckdb' 2>/dev/null | head -1 || true)"
if [ -z "$RAW" ]; then
  Z="$(find "$DIR" -name 's[0-9][0-9].duckdb.zst' 2>/dev/null | head -1 || true)"
  [ -n "$Z" ] || { echo "::error::no s<NN>.duckdb[.zst] under $DIR"; exit 1; }
  RAW="${Z%.zst}"; zstd -d -f "$Z" -o "$RAW"
fi
SERIES="$(basename "$RAW" .duckdb)"; SERIES="${SERIES#s}"
# Integrity: DUCK magic + a non-zero embedded count, before it touches the channel.
[ "$(dd if="$RAW" bs=1 skip=8 count=4 2>/dev/null)" = DUCK ] || { echo "::error::$RAW is not a DuckDB"; exit 1; }
command -v duckdb >/dev/null 2>&1 && {
  N="$(duckdb "$RAW" -noheader -list 'SELECT count(*) FROM clauses WHERE embedding IS NOT NULL;' 2>/dev/null || echo 0)"
  [ "${N:-0}" -gt 0 ] || { echo "::error::shard s${SERIES} has 0 embedded clauses — refusing to publish"; exit 1; }
  echo "shard s${SERIES}: ${N} embedded clauses"
}

printf %s "$GHCR_PAT" | oras login ghcr.io -u "$GHCR_OWNER" --password-stdin
work="$(mktemp -d)/vec"; mkdir -p "$work"
# 1) seed from the existing channel so OTHER series survive this single-series push.
if oras pull "$REF" -o "$work" 2>/dev/null; then echo "pulled existing channel"; else echo "first publish"; fi
existing='[]'
[ -f "$work/vec-manifest.json" ] && existing="$(jq -c '[(.sub_bases//[])[] | select(.!="3gpp-embedded.duckdb")]' "$work/vec-manifest.json")"
rm -f "$work/vec-manifest.json"
# 2) overlay this run's shard.
zstd -19 -T0 -f "$RAW" -o "$work/s${SERIES}.duckdb.zst"
new="$(jq -cn --arg n "s${SERIES}.duckdb" '[$n]')"
# 3) union existing ∪ new, keep only entries whose .zst is present.
subs="$(jq -cn --argjson e "$existing" --argjson n "$new" '($e+$n)|unique')"
present="$(cd "$work" && ls -1 ./*.duckdb.zst 2>/dev/null | sed 's#^\./##; s#\.zst$##' | jq -R . | jq -cs .)"
subs="$(jq -cn --argjson s "$subs" --argjson p "$present" '[ $s[] | select(. as $x | $p | any(.==$x)) ]')"
printf %s "$subs" | jq --arg m "$MODEL" '{embedding_model:$m, embed_floor:"", sub_bases:.}' > "$work/vec-manifest.json"
cat "$work/vec-manifest.json"
( cd "$work"
  args="vec-manifest.json:application/json"
  for z in *.duckdb.zst; do args="$args $z:application/zstd"; done
  oras push "$REF" --artifact-type application/vnd.kodflow.3gpp-vec.v1 \
    --annotation "org.opencontainers.image.source=https://github.com/${GHCR_OWNER}/3gpp-mcp" $args
  # race-free per-series record (immutable-ish): a single-shard artifact under its own tag.
  oras push "ghcr.io/${GHCR_OWNER}/3gpp-vec:s${SERIES}-fp16" \
    --artifact-type application/vnd.kodflow.3gpp-vec.v1 "s${SERIES}.duckdb.zst:application/zstd" || true )

# anti-leak: 3gpp-vec carries verbatim clause text → must stay PRIVATE.
# Read visibility under BOTH the user and org scope (the owner may be either).
vis=absent
for scope in "users/${GHCR_OWNER}" "orgs/${GHCR_OWNER}"; do
  v="$(GH_TOKEN=$GHCR_PAT gh api "/${scope}/packages/container/3gpp-vec" --jq .visibility 2>/dev/null || true)"
  [ -n "$v" ] && { vis="$v"; break; }
done
if [ "$vis" = public ]; then
  GH_TOKEN=$GHCR_PAT gh api -X PATCH "/user/packages/container/3gpp-vec/visibility" -f visibility=private >/dev/null 2>&1 ||
    GH_TOKEN=$GHCR_PAT gh api -X PATCH "/orgs/${GHCR_OWNER}/packages/container/3gpp-vec/visibility" -f visibility=private >/dev/null 2>&1 || true
fi
echo "published s${SERIES} → ${REF} (+ per-series tag)"
