#!/usr/bin/env bash
#
# build-image.sh — build the 3gpp-mcp images from the corpus THIS machine just
# produced, without GitHub Actions and without a registry.
#
#   ./scripts/local/build-image.sh              # data image + full image
#   ./scripts/local/build-image.sh --data-only  # stop after the data layer
#   ./scripts/local/build-image.sh --tag v1     # tag suffix (default: local)
#
# Why this exists: the corpus is built locally (ADR 0003) but the only path that
# ever turned it into an image was .github/workflows/corpus-data-image.yml, which
# needs a runner, a registry and secrets. On a machine that HAS the corpus, that
# is the long way round.
#
# The split is the one the workflows use, and it matters: `full` does
# `FROM 3gpp-data`, never `COPY`, so its manifest references the data layer BY
# DIGEST. A code-only rebuild re-creates only the small top layers and the
# multi-gigabyte data blob is inherited verbatim.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$ROOT" || exit 1

TAG="local"
DATA_ONLY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --tag)       TAG="$2"; shift 2;;
    --data-only) DATA_ONLY=1; shift;;
    -h|--help)   sed -n '2,20p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

log()  { printf '[image] %s\n' "$*"; }
die()  { printf '[image][error] %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "docker is not on PATH — this script builds, it does not install"

CORPUS="$ROOT/data/3gpp.duckdb"
ETSI="$ROOT/data/etsi.duckdb"
[ -s "$CORPUS" ] || die "no corpus at $CORPUS — run the pipeline first (.local/bin/goal.exe run)"

# The gate that decides whether this corpus is worth shipping. Baking a corpus
# that fails its own contract produces an image that serves lexically while
# claiming semantic capability — the exact failure `smoke` exists to catch, and
# a container is a much worse place to discover it.
if [ -x "$ROOT/.local/bin/validate.exe" ] || [ -x "$ROOT/.local/bin/validate" ]; then
  VALIDATE="$ROOT/.local/bin/validate.exe"; [ -x "$VALIDATE" ] || VALIDATE="$ROOT/.local/bin/validate"
  log "checking the corpus contract before baking it"
  "$VALIDATE" --db "$CORPUS" --report text --require-fts --require-hnsw \
      --require-embed-complete --embed-floor "${EMBED_FLOOR:-Rel-99}" \
    || die "the corpus does not satisfy its own contract — refusing to bake it"
else
  log "WARNING: validate is not built; baking WITHOUT the contract check"
fi

log "staging image-data/"
mkdir -p image-data
cp -f "$CORPUS" image-data/3gpp.duckdb
# ETSI travels in the SAME image and stays a SEPARATE file: the entrypoint adds
# -etsi-db when it finds one, so get_spec, list_specs and the LI tools cover both
# corpora without either being merged into the other.
if [ -s "$ETSI" ]; then
  cp -f "$ETSI" image-data/etsi.duckdb
  log "ETSI corpus included ($(du -h "$ETSI" | cut -f1))"
else
  log "no etsi.duckdb — the image will serve 3GPP only"
fi
log "image-data/ = $(du -sh image-data | cut -f1)"

# The label is io.kodflow.3gpp.duckdb.rows, so it has to carry a row count.
# `dbcount | head -1` gave it "spec_versions=20163" — the first line of a
# multi-line report, which is a catalogue size, not the corpus's rows. An
# operator reading that label off a pulled image would be told the wrong thing
# about what they pulled, which is the whole reason the label exists.
#
# Ask the corpus directly. On a converted corpus `clauses` is a view over the
# occurrences and count(*) through it is 0.62 s, because DuckDB prunes the text
# column it never selects (ADR 0004).
ROWS="$(.local/bin/q.exe "$CORPUS" "SELECT count(*) FROM clauses" 2>/dev/null | tail -1 | tr -d '\r')"
case "$ROWS" in ''|*[!0-9]*) ROWS="unknown";; esac
log "corpus carries $ROWS clause rows"

log "building 3gpp-data:$TAG"
docker build -f Dockerfile.data \
  --build-arg "DUCKDB_ROWS=${ROWS:-unknown}" \
  -t "3gpp-data:$TAG" . || die "data image build failed"

if [ "$DATA_ONLY" = 1 ]; then
  log "done (data image only): 3gpp-data:$TAG"
  exit 0
fi

log "building 3gpp-mcp:$TAG on top of it"
# --target full is NOT optional. `smoketest` is the LAST stage in the Dockerfile,
# on purpose (a bare `docker build .` must produce the one target that needs no
# data image), so omitting --target here silently builds the CI smoke stage and
# ignores DATA_IMAGE entirely — a corpus-less image wearing the product tag.
#
# DATA_CONTRACT_FLAGS comes from scripts/data-contract.sh, the same source CI
# uses, so the in-image guard checks the same contract here as there instead of
# falling back to its two-flag default. That guard runs `check-data` against the
# inherited layer and fails the BUILD — the last place an incomplete corpus can
# be caught before it is answering queries.
CONTRACT="$(bash "$ROOT/scripts/data-contract.sh" 2>/dev/null)"
log "data contract: ${CONTRACT:-<Dockerfile default>}"
# AN ARRAY, NOT A STRING. `${CONTRACT:+--build-arg "DATA_CONTRACT_FLAGS=$CONTRACT"}`
# looks quoted and is not: the expansion is word-split afterwards, so docker
# received --build-arg DATA_CONTRACT_FLAGS=--require-fts followed by --require-hnsw
# and --require-embed-complete as loose positional arguments. Caught by running
# this script against a stub `docker` that prints its argv, which is the only way
# a quoting bug like this shows itself short of a real build.
build_args=(--build-arg "DATA_IMAGE=3gpp-data:$TAG")
[ -n "$CONTRACT" ] && build_args+=(--build-arg "DATA_CONTRACT_FLAGS=$CONTRACT")
docker build -f Dockerfile --target full "${build_args[@]}" \
  -t "3gpp-mcp:$TAG" . || die "full image build failed"

log "done:"
docker images --format '  {{.Repository}}:{{.Tag}}  {{.Size}}' | grep -E '^  3gpp-(data|mcp):' || true
cat <<EOF

Run it the way a client would:

  docker run -i --rm 3gpp-mcp:$TAG serve

Or wire it into any mcpServers client:

  { "mcpServers": { "3gpp": { "command": "docker",
      "args": ["run", "-i", "--rm", "3gpp-mcp:$TAG", "serve"] } } }
EOF
