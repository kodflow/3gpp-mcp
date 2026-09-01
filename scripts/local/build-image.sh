#!/usr/bin/env bash
#
# build-image.sh — build the 3gpp-mcp container image and push it to GHCR,
# ENTIRELY ON THIS MACHINE, with no Docker, no WSL and no CI runner.
#
#   scripts/local/build-image.sh [--tag ghcr.io/kodflow/3gpp-mcp:latest]
#                                [--no-push] [--no-corpus] [--no-models]
#
# WHY THIS EXISTS. The image used to be baked by two GitHub workflows that moved
# ~14 GB per run. Those are gone: the build happens here and the result is pushed
# to ghcr.io/<owner>/3gpp-mcp:latest. Nothing about the image changes — same base,
# same layout, same entrypoint — only who assembles it.
#
# HOW IT WORKS WITHOUT A CONTAINER RUNTIME. An OCI image is a base manifest plus
# layer tarballs plus a config blob, and crane can compose all three. So instead of
# RUN steps we stage a rootfs overlay on disk, tar it, and hand the tarballs to
# `crane append`. Everything a Dockerfile RUN would have done at build time is
# either done here (the extension prefetch becomes a download of the same files)
# or does not need doing (apt: the two libraries the binary needs travel in the
# overlay rather than being installed).
#
# THE LINUX BINARY IS CROSS-COMPILED. cmd/server needs cgo for DuckDB, so a Linux
# toolchain is required. zig cc provides one (see scripts/local/zigcc), with two
# details that are not optional:
#
#   - DuckDB's prebuilt linux-amd64 archive is compiled against GNU libstdc++,
#     and zig ships libc++. Linking against zig's C++ runtime fails with hundreds
#     of undefined std::__cxx11 symbols. The link therefore uses Debian's own
#     libstdc++.so.6 / libgomp.so.1 — the exact pair the runtime image carries.
#   - The Rust cdylib must be built with an explicit -soname. Without it lld
#     records the ABSOLUTE BUILD PATH as the NEEDED entry, so the binary links on
#     this machine and dies in the container looking for a Windows path.
#
# LAYER ORDER IS DELIBERATE, from most stable to least: a re-push only transfers
# the blobs that actually changed, and the corpus (the 11 GB one) is last so a
# code-only rebuild re-uploads a few hundred MB rather than everything.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

TAG="${IMAGE_TAG:-ghcr.io/kodflow/3gpp-mcp:latest}"
BASE="${IMAGE_BASE:-debian:bookworm-slim}"
PUSH=1
WITH_CORPUS=1
WITH_MODELS=1

while [ $# -gt 0 ]; do
  case "$1" in
    --tag) TAG="$2"; shift 2 ;;
    --base) BASE="$2"; shift 2 ;;
    --no-push) PUSH=0; shift ;;
    --no-corpus) WITH_CORPUS=0; shift ;;
    --no-models) WITH_MODELS=0; shift ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

say() { printf '\n\033[1m[image] %s\033[0m\n' "$*"; }
die() { printf '\033[31m[image] %s\033[0m\n' "$*" >&2; exit 1; }

# ---- toolchain ---------------------------------------------------------------

CRANE="$ROOT/.local/bin/crane.exe"
[ -x "$CRANE" ] || CRANE="$(command -v crane || true)"
[ -n "$CRANE" ] && [ -x "$CRANE" ] || die "crane not found (.local/bin/crane.exe)"

ZIG_DIR="$(ls -d "$ROOT"/.local/toolchain/zig-* 2>/dev/null | head -1 || true)"
[ -n "$ZIG_DIR" ] || die "zig not found under .local/toolchain (see scripts/local/fetch-linux-toolchain.sh)"
export ZIG
ZIG="$(cd "$ZIG_DIR" && pwd -W 2>/dev/null || echo "$ZIG_DIR")/zig.exe"
export ZIG_TARGET="${ZIG_TARGET:-x86_64-linux-gnu.2.36}"

SYSROOT="$ROOT/.local/toolchain/sysroot-linux/lib"
SYSROOT_W="$(cd "$ROOT/.local/toolchain/sysroot-linux/lib" 2>/dev/null && (pwd -W 2>/dev/null || pwd) || true)"
[ -s "$SYSROOT/libstdc++.so.6" ] && [ -s "$SYSROOT/libgomp.so.1" ] \
  || die "Debian libstdc++.so.6 / libgomp.so.1 missing under $SYSROOT (see scripts/local/fetch-linux-toolchain.sh)"

# shellcheck source=scripts/local/toolchain-env.sh
. "$ROOT/scripts/local/toolchain-env.sh" >/dev/null 2>&1 || true
export PATH="$ROOT/.local/toolchain/cargo/bin:$PATH"

STAGE="$ROOT/.local/image"
ROOTFS="$STAGE/rootfs"
LAYERS="$STAGE/layers"
rm -rf "$STAGE"
mkdir -p "$ROOTFS" "$LAYERS"

# ---- 1. cross-build ----------------------------------------------------------

say "cross-building the Linux artefacts (zig, target $ZIG_TARGET)"

go build -o "$ROOT/.local/bin/zigcc.exe" ./scripts/local/zigcc
cp -f "$ROOT/.local/bin/zigcc.exe" "$ROOT/.local/bin/zigcxx.exe"
CC_SHIM="$(cd "$ROOT/.local/bin" && (pwd -W 2>/dev/null || pwd))/zigcc.exe"
CXX_SHIM="$(cd "$ROOT/.local/bin" && (pwd -W 2>/dev/null || pwd))/zigcxx.exe"

# The cdylib FIRST: the server links against it. -soname is what stops lld from
# writing this machine's absolute path into the binary's NEEDED list.
CC_x86_64_unknown_linux_gnu="$CC_SHIM" \
CXX_x86_64_unknown_linux_gnu="$CXX_SHIM" \
CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER="$CC_SHIM" \
CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUSTFLAGS="-C link-arg=-Wl,-soname,libembed_core.so" \
  cargo build --release --target x86_64-unknown-linux-gnu \
    --manifest-path rust/embed-core/Cargo.toml --features ort

CDYLIB="$ROOT/rust/embed-core/target/x86_64-unknown-linux-gnu/release/libembed_core.so"
[ -s "$CDYLIB" ] || die "the embed-core cdylib was not produced"
CDYLIB_W="$(cd "$(dirname "$CDYLIB")" && (pwd -W 2>/dev/null || pwd))"

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  CC="$CC_SHIM" CXX="$CXX_SHIM" \
  CGO_LDFLAGS="-L$CDYLIB_W -L$SYSROOT_W -l:libstdc++.so.6 -l:libgomp.so.1 -Wl,-rpath,/usr/local/lib" \
  go build -tags "onnx,embed_ffi" -trimpath \
    -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$STAGE/mcp-3gpp" ./cmd/server

# A binary that links here and dies there is the failure this check exists for:
# every NEEDED entry must be a bare SONAME the container can resolve, never a
# path from this filesystem.
go run ./scripts/local/elfneeded "$STAGE/mcp-3gpp" --require-sonames

# ---- 2. rootfs overlay -------------------------------------------------------

say "staging the rootfs overlay"

install -d "$ROOTFS/usr/local/bin" "$ROOTFS/usr/local/lib" \
           "$ROOTFS/usr/lib/x86_64-linux-gnu" \
           "$ROOTFS/data/mcp-3gpp/models" "$ROOTFS/home/mcp" "$ROOTFS/etc"

cp "$STAGE/mcp-3gpp"        "$ROOTFS/usr/local/bin/mcp-3gpp"
cp docker-entrypoint.sh     "$ROOTFS/usr/local/bin/docker-entrypoint.sh"
chmod +x "$ROOTFS/usr/local/bin/mcp-3gpp" "$ROOTFS/usr/local/bin/docker-entrypoint.sh"
cp "$CDYLIB"                "$ROOTFS/usr/local/lib/libembed_core.so"
cp "$SYSROOT/libstdc++.so.6" "$SYSROOT/libgomp.so.1" "$ROOTFS/usr/lib/x86_64-linux-gnu/"

# The mcp user. A Dockerfile would groupadd/useradd; with no runtime to execute
# them, the base's /etc/passwd and /etc/group are read out of the base image and
# re-shipped with one line appended. Reading them rather than writing fresh files
# matters: a hand-written passwd would drop root, daemon and nobody, and anything
# in the image that resolves a uid would start answering wrong.
say "deriving /etc/passwd and /etc/group from $BASE"
go build -o "$ROOT/.local/bin/imgtar.exe" ./scripts/local/imgtar
IMGTAR="$ROOT/.local/bin/imgtar.exe"
"$CRANE" export "$BASE" "$STAGE/base.tar" --platform linux/amd64
"$IMGTAR" cat --in "$STAGE/base.tar" etc/passwd > "$STAGE/passwd"
"$IMGTAR" cat --in "$STAGE/base.tar" etc/group  > "$STAGE/group"
# base.tar is NOT deleted here any more: step 7b reads the libraries the base
# carries, and re-exporting it would be a second multi-hundred-MB pull.
[ -s "$STAGE/passwd" ] && [ -s "$STAGE/group" ] || die "could not read /etc/passwd from $BASE"
grep -q '^mcp:' "$STAGE/passwd" || echo 'mcp:x:10001:10001::/home/mcp:/usr/sbin/nologin' >> "$STAGE/passwd"
grep -q '^mcp:' "$STAGE/group"  || echo 'mcp:x:10001:' >> "$STAGE/group"
cp "$STAGE/passwd" "$ROOTFS/etc/passwd"
cp "$STAGE/group"  "$ROOTFS/etc/group"

# DuckDB's loadable extensions, baked so a no-egress container still gets BM25 and
# HNSW instead of silently degrading to LIKE and an exact scan. The Dockerfile ran
# `mcp-3gpp prefetch-extensions`; here the same files are fetched directly, for the
# version the LINUX binary actually links — which is NOT this machine's DuckDB. The
# Windows build uses duckdb_use_lib against a local 1.4.3; the Linux build links the
# bundled duckdb-go-bindings archive, so the version is read from go.mod.
DUCK_PIN="$(sed -n 's#.*duckdb-go-bindings/lib/linux-amd64 v0\.\([0-9]*\)\.0.*#\1#p' go.mod | head -1)"
[ -n "$DUCK_PIN" ] || die "cannot read the duckdb-go-bindings linux-amd64 pin from go.mod"
DUCK_VER="v$(( 10#${DUCK_PIN:0:1} )).$(( 10#${DUCK_PIN:1:2} )).$(( 10#${DUCK_PIN:3:2} ))"
say "DuckDB extensions for $DUCK_VER / linux_amd64 (from go.mod pin v0.$DUCK_PIN.0)"
EXTDIR="$ROOTFS/home/mcp/.duckdb/extensions/$DUCK_VER/linux_amd64"
install -d "$EXTDIR"
for ext in fts vss; do
  curl -fsSL "http://extensions.duckdb.org/$DUCK_VER/linux_amd64/$ext.duckdb_extension.gz" \
    | gunzip > "$EXTDIR/$ext.duckdb_extension" \
    || die "cannot fetch the $ext extension for $DUCK_VER/linux_amd64"
done

# ---- 3. ONNX Runtime ---------------------------------------------------------
# The only piece that is arch-specific and not built here. Pinned and checksummed
# exactly as scripts/fetch-model.sh pins it; the layout matches bootstrap.ORTLibPath.
# The pin is read from fetch-model.sh's default-expansion, which is the single
# place this project decides an ORT version — sourcing the number rather than
# copying it is what stops the image and the local embedder from drifting apart.
ORT_VERSION="${ORT_VERSION:-$(sed -n 's/^ORT_VERSION="\${ORT_VERSION:-\([0-9][0-9.]*\)}"$/\1/p' scripts/fetch-model.sh | head -1)}"
[ -n "$ORT_VERSION" ] || die "cannot read the ORT_VERSION pin from scripts/fetch-model.sh"
say "ONNX Runtime $ORT_VERSION (linux x64)"
install -d "$ROOTFS/data/mcp-3gpp/models/onnxruntime"
curl -fsSL -o "$STAGE/ort.tgz" \
  "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz"
"$IMGTAR" untar --in "$STAGE/ort.tgz" --dest "$ROOTFS/data/mcp-3gpp/models/onnxruntime" --strip 1
rm -f "$STAGE/ort.tgz"

# ---- 4. models + registry ----------------------------------------------------

if [ "$WITH_MODELS" = 1 ]; then
  # THE DUAL-HEAD MODEL, not the dense-only one. Only one registry entry is active
  # at serve time (SparseCapable reads ActiveModel), so a dense-only image drops
  # the learned-lexical arm even when the corpus is full of sparse postings. The
  # two stamp the same dense identity — measured, and locked by
  # internal/embed/registry_dual_head_test.go.
  [ -d data/models/bge-m3-sparse ] || die "data/models/bge-m3-sparse missing (WITH_SPARSE=1 scripts/fetch-model.sh)"
  say "models: bge-m3-sparse (dense + sparse heads) and the reranker"
  cp -a data/models/bge-m3-sparse "$ROOTFS/data/mcp-3gpp/models/"
  [ -d data/models/bge-reranker-v2-m3 ] && cp -a data/models/bge-reranker-v2-m3 "$ROOTFS/data/mcp-3gpp/models/"

  cat > "$ROOTFS/data/mcp-3gpp/models/models.yaml" <<'YAML'
active: bge-m3-sparse
models:
  - name: bge-m3-sparse
    family: bge-m3
    dir: /data/mcp-3gpp/models/bge-m3-sparse
    precision: fp32
    dim: 1024
    normalization: l2
    windowing: mean_pool
    max_tokens: 2048
    revision: 5617a9f
    tokenizer_revision: 5617a9f
    inputs: [input_ids, attention_mask]
    output: sentence_embedding
    sparse_output: sparse_weights
YAML
fi

# ---- 5. corpus ---------------------------------------------------------------

if [ "$WITH_CORPUS" = 1 ]; then
  [ -s data/3gpp.duckdb ] || die "data/3gpp.duckdb missing — run: make build"

  # The gate that decides whether this corpus is worth shipping. Baking one that
  # fails its own contract produces an image that serves lexically while claiming
  # semantic capability, and a container is a much worse place to discover that
  # than a shell. Kept from the Docker-based version of this script, which is the
  # only part of it that was about the corpus rather than about docker build.
  VALIDATE="$ROOT/.local/bin/validate.exe"
  [ -x "$VALIDATE" ] || VALIDATE="$ROOT/.local/bin/validate"
  if [ -x "$VALIDATE" ]; then
    # The flags come from scripts/data-contract.sh, never from a copy here. That
    # script is the single source of the completeness contract precisely so the
    # two gates that enforce it cannot drift; hardcoding a third opinion in the
    # thing that actually publishes would defeat the arrangement.
    #
    # DATA_CONTRACT picks the level (dense | dense+sparse | dense+sparse+etsi).
    # DATA_ETSI_DB points --require-etsi at the local layout rather than the
    # image's absolute path.
    CONTRACT_FLAGS="$(DATA_ETSI_DB="$ROOT/data/etsi.duckdb" \
                      DATA_EMBED_FLOOR="${EMBED_FLOOR:-Rel-99}" \
                      bash scripts/data-contract.sh)" \
      || die "scripts/data-contract.sh refused DATA_CONTRACT=${DATA_CONTRACT:-dense}"
    say "corpus contract (${DATA_CONTRACT:-dense}): $CONTRACT_FLAGS"
    # THE GATE MUST RESOLVE THE SPARSE IDENTITY, OR --require-sparse CHECKS NOTHING.
    #
    # cmd/validate compares schema_meta.sparse_model against embed.SparseModelID(),
    # which reads the ACTIVE registry entry — and the default one is bge-m3, which
    # is dense-only, so it returned "" and the comparison used to be skipped. This
    # call sat BEFORE the EMBED_MODELS_CONFIG export below, so the one check written
    # to catch a sparse layer built by another model was inert in the only place it
    # mattered. Measured: `embedid --sparse` prints nothing by default and
    # b13103bce7ae under EMBED_MODEL=bge-m3-sparse, while the DENSE identity stays
    # 38067f8c6efe under both — so selecting the dual-head entry costs nothing and
    # names the model the image actually bakes.
    # shellcheck disable=SC2086 # intentional word-split: the contract is a flag list
    EMBED_MODEL=bge-m3-sparse "$VALIDATE" --db data/3gpp.duckdb --report text $CONTRACT_FLAGS \
      || die "the corpus does not satisfy its own contract — refusing to bake it"
  else
    say "WARNING: validate is not built; baking WITHOUT the contract check"
  fi

  say "corpus: $(du -h data/3gpp.duckdb | cut -f1) 3GPP + $(du -h data/etsi.duckdb 2>/dev/null | cut -f1) ETSI"
  # ETSI travels in the SAME image and stays a SEPARATE file: the entrypoint adds
  # -etsi-db when it finds one, so get_spec, list_specs and the LI tools cover
  # both corpora without either being merged into the other.
  cp data/3gpp.duckdb "$ROOTFS/data/mcp-3gpp/3gpp.duckdb"
  [ -s data/etsi.duckdb ] && cp data/etsi.duckdb "$ROOTFS/data/mcp-3gpp/etsi.duckdb"
fi

# ---- 6. identity guards ------------------------------------------------------
# The same two comparisons the workflow made, for the same reason: an image whose
# baked registry disagrees with its own corpus disables vector search at serve
# time, and one whose sparse head disagrees scores against another model's
# vocabulary. Both fail silently at run time, so they are checked here.

if [ "$WITH_CORPUS" = 1 ] && [ "$WITH_MODELS" = 1 ]; then
  say "identity guards"
  export EMBED_MODELS_CONFIG="$ROOTFS/data/mcp-3gpp/models/models.yaml"
  EMBED_ID="$(go run ./cmd/embedid)"
  SPARSE_ID="$(go run ./cmd/embedid --sparse)"
  counters="$(go run ./cmd/dbcount --db data/3gpp.duckdb)"
  DB_ID="$(printf '%s\n' "$counters" | sed -n 's/^embedding_model=//p' | head -1)"
  DB_SPARSE="$(printf '%s\n' "$counters" | sed -n 's/^sparse_model=//p' | head -1)"
  SPARSE_ROWS="$(printf '%s\n' "$counters" | sed -n 's/^clauses_with_sparse=//p' | head -1)"
  unset EMBED_MODELS_CONFIG
  echo "  dense  registry=$EMBED_ID corpus=${DB_ID:-<none>}"
  echo "  sparse registry=${SPARSE_ID:-<none>} corpus=${DB_SPARSE:-<none>} rows=${SPARSE_ROWS:-0}"
  [ -n "$DB_ID" ] && [ "$DB_ID" != "$EMBED_ID" ] &&
    die "the baked registry is $EMBED_ID but the corpus was embedded with $DB_ID — the serve guard would disable vector search"
  if [ "${SPARSE_ROWS:-0}" -gt 0 ]; then
    [ -n "$SPARSE_ID" ] ||
      die "the corpus carries $SPARSE_ROWS sparse posting(s) but the baked registry declares no sparse head"
    [ -n "$DB_SPARSE" ] && [ "$DB_SPARSE" != "$SPARSE_ID" ] &&
      die "baked sparse model $SPARSE_ID but the postings were built with $DB_SPARSE"
  fi
fi

# ---- 7. layers ---------------------------------------------------------------
# Split most-stable first so a re-push moves only what changed. Ownership is set
# in the tar rather than by a RUN chown: uid/gid 10001 is what the entrypoint and
# the read-only data path expect.

layer() { # layer <name> <uid> <path…>
  local name="$1" uid="$2"; shift 2
  local present=0 p
  for p in "$@"; do [ -e "$ROOTFS/$p" ] && present=1; done
  [ "$present" = 1 ] || return 0
  "$IMGTAR" pack --root "$ROOTFS" --out "$LAYERS/$name.tar" --uid "$uid" --gid "$uid" "$@"
}

# ---- 7b. will the container's loader find everything? ------------------------
#
# The last unproven property of this image, and the one with no local substitute:
# there is no container runtime and no WSL distribution on this machine, so the
# image cannot be started before it is published. crane validate proves the
# manifest and blobs are well formed; server-full.exe proves THIS corpus answers;
# neither can notice an ELF whose DT_NEEDED names a library that exists here and
# in no layer of the image. That container pulls, starts, and dies immediately
# with "cannot open shared object file".
#
# --require-sonames above is the sibling check and not a substitute: it proves the
# entry is a name rather than a Windows build path, and it only ever looked at the
# server binary. A well-formed SONAME for a library nobody shipped fails exactly
# the same way, and the cdylib and the ONNX Runtime objects travel here too.
#
# The search path given is the one the image config actually sets
# (LD_LIBRARY_PATH=/usr/local/lib), so a library staged somewhere the loader does
# not look is reported missing rather than counted present.
say "loader resolution (every ELF we ship, against the overlay + $BASE)"
go run ./scripts/local/elfneeded --resolve \
  --rootfs "$ROOTFS" \
  --base-tar "$STAGE/base.tar" \
  --ld-library-path usr/local/lib \
  || die "an ELF in the image needs a library the image does not carry"
rm -f "$STAGE/base.tar"

say "packing layers"
layer 10-runtime    0     etc/passwd etc/group usr/lib/x86_64-linux-gnu
layer 20-duckdb-ext 10001 home/mcp
layer 30-ort        10001 data/mcp-3gpp/models/onnxruntime
[ "$WITH_MODELS" = 1 ] && layer 40-models 10001 \
  data/mcp-3gpp/models/bge-m3-sparse data/mcp-3gpp/models/bge-reranker-v2-m3 data/mcp-3gpp/models/models.yaml
[ "$WITH_CORPUS" = 1 ] && layer 50-corpus 10001 data/mcp-3gpp/3gpp.duckdb data/mcp-3gpp/etsi.duckdb
layer 60-bin        0     usr/local/bin usr/local/lib

LAYER_ARGS=()
for f in "$LAYERS"/*.tar; do LAYER_ARGS+=(-f "$f"); done
[ "${#LAYER_ARGS[@]}" -gt 0 ] || die "no layers were produced"

# ---- 8. assemble + push ------------------------------------------------------

if [ "$PUSH" = 0 ]; then
  say "assembling locally (no push) → $STAGE/image.tar"
  "$CRANE" append --platform linux/amd64 -b "$BASE" "${LAYER_ARGS[@]}" -t "$TAG" -o "$STAGE/image.tar"
  echo "wrote $STAGE/image.tar"
  exit 0
fi

say "appending onto $BASE and pushing $TAG"
"$CRANE" append --platform linux/amd64 -b "$BASE" "${LAYER_ARGS[@]}" -t "$TAG"

say "setting the image config"
DIGEST="$("$CRANE" digest "$TAG")"
"$CRANE" mutate "$TAG" -t "$TAG" \
  --entrypoint docker-entrypoint.sh \
  --cmd serve \
  --user 10001:10001 \
  --workdir /home/mcp \
  -e MCP3GPP_CACHE=/data/mcp-3gpp \
  -e MCP_TRANSPORT=stdio \
  -e MCP_PORT=8765 \
  -e PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  -e LD_LIBRARY_PATH=/usr/local/lib \
  -e ORT_DYLIB_PATH=/data/mcp-3gpp/models/onnxruntime/lib/libonnxruntime.so \
  -e EMBED_MODELS_CONFIG=/data/mcp-3gpp/models/models.yaml \
  -l org.opencontainers.image.source=https://github.com/kodflow/3gpp-mcp \
  -l org.opencontainers.image.version="$VERSION" \
  -l io.kodflow.3gpp.built=local \
  >/dev/null

say "published $TAG"
echo "  base   $BASE (appended, $DIGEST before config)"
echo "  digest $("$CRANE" digest "$TAG")"
"$CRANE" config "$TAG" | head -c 400; echo
