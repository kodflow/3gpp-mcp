#!/usr/bin/env bash
#
# publish-corpus.sh — push the corpus this machine built to GHCR, WITHOUT Docker.
#
#   ./scripts/local/publish-corpus.sh --dry-run     # do everything except the push
#   ./scripts/local/publish-corpus.sh               # publish 3gpp-corpus + etsi-corpus
#   ./scripts/local/publish-corpus.sh --only etsi   # just the ETSI one (23 MB, fast)
#
# WHY THIS EXISTS. The corpus is built locally (ADR 0003) and the images are baked
# by .github/workflows/corpus-data-image.yml, which does NOT read this machine: it
# pulls ghcr.io/<owner>/3gpp-corpus and ghcr.io/<owner>/etsi-corpus and copies
# /3gpp.duckdb and /etsi.duckdb out of them. What published those images was
# corpus-matrix.yml, retired with the ten other Kaggle workflows. So the corpus
# now has no way to reach the bake, and running the bake today would rebuild the
# images from whatever STALE corpus is still sitting in GHCR — the exact
# stale-data-layer failure Dockerfile warns about at length.
#
# WHY CRANE. This machine has no container runtime at all — no Docker, no Podman,
# and WSL carries no distribution. crane is a static binary that talks to the
# registry API directly, so it needs no daemon. etsi-matrix.yml used exactly this,
# and its two hard-won details are kept verbatim:
#
#   - `--oci-empty-base` and NOT `-b scratch`. crane treats "scratch" as a real
#     repository and tries to pull docker.io/library/scratch, which fails with
#     MANIFEST_UNKNOWN and publishes an empty corpus. The empty base IS scratch.
#   - the package must be PRIVATE. It carries verbatim 3GPP/ETSI text.
#
# AFTER THIS, in .github/workflows: run `corpus-data-image` then `corpus-image`
# (both workflow_dispatch). They do the rest on a runner that has Docker.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$ROOT" || exit 1

OWNER="${GHCR_OWNER:-kodflow}"
DRY=0
ONLY="both"
FORCE_UNVERIFIED=0
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)            DRY=1; shift;;
    --only)               ONLY="$2"; shift 2;;
    --owner)              OWNER="$2"; shift 2;;
    --allow-unverified-visibility) FORCE_UNVERIFIED=1; shift;;
    -h|--help)            sed -n '2,32p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

log() { printf '[publish] %s\n' "$*"; }
die() { printf '[publish][error] %s\n' "$*" >&2; exit 1; }

CRANE="$ROOT/.local/bin/crane.exe"; [ -x "$CRANE" ] || CRANE="$ROOT/.local/bin/crane"
[ -x "$CRANE" ] || die "crane is not at .local/bin/crane — fetch it from
  https://github.com/google/go-containerregistry/releases (v0.20.2), it is a single static binary"

# THE TOKEN, AND WHY IT IS CHECKED BEFORE ANYTHING IS COMPRESSED.
#
# The 3GPP corpus is ~8 GB compressed. Discovering a missing scope after the
# upload is a wasted hour, so the scope is asserted first. `gh auth token` yields
# whatever the CLI holds; GHCR_PAT overrides it for the case where a separate PAT
# is used (which is what CI does, to keep the package born-private).
# THREE SOURCES, IN THIS ORDER, AND THE FILE EXISTS FOR A REASON.
#
#   1. $GHCR_PAT              — what CI uses
#   2. .local/ghcr.pat        — a file, so the token never has to be typed into a
#                               shell (history) or pasted into a chat transcript.
#                               .local/ is gitignored, so it cannot be committed.
#   3. `gh auth token`        — convenient, but the CLI's own token is an OAuth
#                               token whose scopes are granted by a DEVICE FLOW:
#                               `gh auth refresh` prints a code and waits for a
#                               browser. Run non-interactively it just dies with
#                               "context deadline exceeded" and grants nothing.
#
# A classic PAT is also the posture CI deliberately chose: pushing with a PAT
# rather than the workflow token is what makes the package born-PRIVATE, which
# matters because this is verbatim standards text.
TOKEN="${GHCR_PAT:-}"
if [ -z "$TOKEN" ] && [ -f "$ROOT/.local/ghcr.pat" ]; then
  TOKEN="$(tr -d ' \t\r\n' < "$ROOT/.local/ghcr.pat")"
  log "token read from .local/ghcr.pat"
fi
[ -n "$TOKEN" ] || TOKEN="$(gh auth token 2>/dev/null)"
[ -n "$TOKEN" ] || die "no token. Either:
   - write a classic PAT with write:packages into .local/ghcr.pat, or
   - export GHCR_PAT=<that PAT>, or
   - run 'gh auth refresh -h github.com -s write:packages,read:packages' in a REAL
     terminal (it needs a browser and will not work backgrounded)"
# grep+cut, not awk: `awk -F': '` with tolower() returns EMPTY for this header on
# the awk that ships with Git Bash here, which would report "no scopes" for a
# perfectly good token and refuse to publish. Verified both ways against the live
# header before choosing.
SCOPES="$(curl -sS -I -H "Authorization: token $TOKEN" https://api.github.com/user 2>/dev/null \
          | tr -d '\r' | grep -i '^x-oauth-scopes:' | cut -d: -f2- | sed 's/^ *//')"
log "token scopes: ${SCOPES:-<none reported>}"
case ",$(echo "$SCOPES" | tr -d ' '),"  in
  *,write:packages,*) ;;
  *) die "this token cannot write packages (it has: ${SCOPES:-none}).

   Easiest, and what CI does — a classic PAT, no browser dance:
     1. https://github.com/settings/tokens/new  -> tick write:packages + read:packages
     2. write it to .local/ghcr.pat   (gitignored; keeps it out of shell history)
     3. re-run this script

   Or, in a REAL terminal (it opens a browser and will NOT work backgrounded):
     gh auth refresh -h github.com -s write:packages,read:packages";;
esac

# publish_one <package> <local file> <name inside the image>
publish_one() {
  local pkg="$1" src="$2" member="$3"
  local repo="ghcr.io/$OWNER/$pkg"
  [ -s "$src" ] || { log "no $src — skipping $pkg"; return 0; }

  local work; work="$(mktemp -d "$ROOT/.local/publish-XXXXXX")" || die "mktemp"
  # The member sits at the ROOT of the tar so it lands at /$member in the image:
  # corpus-data-image.yml does `docker cp "$cid:/$member"` and nothing else.
  log "$pkg: staging $member ($(du -h "$src" | cut -f1))"
  cp -f "$src" "$work/$member" || die "copy $src"
  ( cd "$work" && tar -cf layer.tar "$member" ) || die "tar $member"
  rm -f "$work/$member"
  log "$pkg: layer is $(du -h "$work/layer.tar" | cut -f1) uncompressed (crane gzips it on the wire)"

  local date_tag; date_tag="$(date -u +%F)"
  if [ "$DRY" = 1 ]; then
    log "DRY RUN — would push $repo:$date_tag and retag :latest"
    rm -rf "$work"; return 0
  fi

  printf %s "$TOKEN" | "$CRANE" auth login ghcr.io -u "$OWNER" --password-stdin \
    || die "crane auth login failed"
  log "$pkg: pushing $repo:$date_tag — this is the long part"
  # --oci-empty-base, never -b scratch. See the header.
  "$CRANE" append --oci-empty-base -f "$work/layer.tar" -t "$repo:$date_tag" \
    || die "crane append failed for $pkg"
  "$CRANE" tag "$repo:$date_tag" latest || die "crane tag latest failed for $pkg"
  rm -rf "$work"
  log "$pkg: published $repo:$date_tag (+ :latest)"

  # ANTI-LEAK. Verbatim standards text must not become a public package.
  local vis
  vis="$(GH_TOKEN=$TOKEN gh api "/users/$OWNER/packages/container/$pkg" --jq '.visibility' 2>/dev/null || echo unknown)"
  case "$vis" in
    private) log "$pkg: visibility private ✓";;
    public)
      log "$pkg: PUBLIC — flipping to private"
      GH_TOKEN=$TOKEN gh api -X PATCH "/user/packages/container/$pkg/visibility" -f visibility=private >/dev/null 2>&1 \
        || die "$pkg is PUBLIC and could not be made private. It carries verbatim standards text.
   Fix it by hand NOW: https://github.com/users/$OWNER/packages/container/$pkg/settings";;
    *)
      printf '[publish][WARNING] %s\n' "cannot read $pkg visibility (needs read:packages).
   It carries verbatim standards text. CHECK IT:
   https://github.com/users/$OWNER/packages/container/$pkg/settings" >&2;;
  esac
}

# The gate. Baking a corpus that fails its own contract produces an image that
# serves lexically while claiming semantic capability, and a registry is a much
# worse place to discover that than a terminal.
VALIDATE="$ROOT/.local/bin/validate.exe"; [ -x "$VALIDATE" ] || VALIDATE="$ROOT/.local/bin/validate"
if [ -x "$VALIDATE" ] && [ "$ONLY" != "etsi" ]; then
  log "checking the corpus contract before publishing it"
  "$VALIDATE" --db "$ROOT/data/3gpp.duckdb" --report text --require-fts --require-hnsw \
      --require-embed-complete --embed-floor "${EMBED_FLOOR:-Rel-99}" \
    || die "the corpus does not satisfy its own contract — refusing to publish it"
elif [ "$ONLY" != "etsi" ]; then
  [ "$FORCE_UNVERIFIED" = 1 ] || die "validate is not built and the contract cannot be checked;
   build it, or pass --allow-unverified-visibility if you really mean to publish unchecked"
fi

case "$ONLY" in
  both) publish_one etsi-corpus "$ROOT/data/etsi.duckdb" etsi.duckdb
        publish_one 3gpp-corpus "$ROOT/data/3gpp.duckdb" 3gpp.duckdb;;
  etsi) publish_one etsi-corpus "$ROOT/data/etsi.duckdb" etsi.duckdb;;
  3gpp) publish_one 3gpp-corpus "$ROOT/data/3gpp.duckdb" 3gpp.duckdb;;
  *) die "--only takes: both | etsi | 3gpp";;
esac

cat <<EOF

Next, on GitHub (both are workflow_dispatch):

  gh workflow run corpus-data-image.yml -f corpus_tag=latest
  gh workflow run corpus-image.yml   -f release_tag=latest

The first bakes 3gpp-data from the images just pushed (it copies /3gpp.duckdb and
/etsi.duckdb out of them); the second builds 3gpp-mcp on top, inheriting that data
layer by digest.
EOF
