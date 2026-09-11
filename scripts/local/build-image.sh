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
# RUN steps we stage a rootfs overlay on disk, tar it, append the tarballs to the
# base (imgtar oci, step 8), and hand the result to `crane push` and the config to
# `crane mutate`. Everything a Dockerfile RUN would have done at build time is
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

# EVERY PHASE LINE CARRIES ITS WALL-CLOCK TIME AND THE TIME SINCE THE START.
#
# The publish of 2026-09-10 spent 24m40 between its first line and its first
# uploaded blob, and nothing in this log could say where: the phase lines had no
# timestamps, so "re-running the contract", "gzipping 40 GB for crane" and
# "repacking layers that did not change" were three equally plausible guesses
# for a cost estimated at 3-5 days to remove. A number on every line turns the
# next publish into the measurement. SECONDS is bash's own counter; it costs
# nothing and cannot be skewed by the host clock moving mid-build.
say() { printf '\n\033[1m[image %s +%dm%02ds] %s\033[0m\n' "$(date +%H:%M:%S)" $((SECONDS / 60)) $((SECONDS % 60)) "$*"; }
die() { printf '\033[31m[image %s +%dm%02ds] %s\033[0m\n' "$(date +%H:%M:%S)" $((SECONDS / 60)) $((SECONDS % 60)) "$*" >&2; exit 1; }

# field <key> <text> — the value of the "<key>=…" line, or empty.
#
# THIS WAS THREE sed CALLS, AND MSYS REWROTE THEIR SCRIPTS BEFORE sed SAW THEM.
#
# Git Bash rewrites an argument shaped like NAME=/posix/path into a Windows path
# on its way to a native .exe. `s/^embedding_model=//p` matches that shape — the
# `=` is followed by what looks like a path — so w64devkit's sed.exe received a
# mangled script and answered "sed: unmatched '/'". Measured here: `s/a=/b/` and
# `s/^a/b/` both work, `s/^a=/b/p` does not, and MSYS2_ARG_CONV_EXCL='*' makes it
# work again. It only bites when a native sed is first on PATH, which is exactly
# what scripts/local/toolchain-env.sh arranges — so the guard block passed when it
# was driven standalone and failed when the pipeline ran it.
#
# The damage was not the message. DB_ID came back EMPTY, and the guard below
# refuses an empty identity by design, because an absent identity is not a
# matching one. So the build died claiming the corpus states no embedding_model —
# on a corpus stamped 38067f8c6efe, after every data gate had passed. A guard that
# cannot read its input reports the very failure it exists to prevent.
#
# Shell built-ins pass no arguments through MSYS, so there is nothing left to
# mangle and nothing left to depend on which sed happens to be first on PATH.
# Same reasoning that replaced tar with imgtar here.
field() {
  local k="$1" line
  while IFS= read -r line; do
    case "$line" in
      "$k="*) printf '%s\n' "${line#"$k="}"; return 0 ;;
    esac
  done <<<"$2"
  return 0
}

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
#
# --locked, because this lockfile decides what ships and nothing else does.
# rust/embed-core is outside the rust/ workspace, so rust/embed-core/Cargo.lock
# alone picks the ort, tokenizers and ndarray baked into libembed_core.so. Without
# the flag cargo is free to re-resolve and REWRITE it as a side effect of the
# build: the drift that moved rust/discover/Cargo.lock mid-step on 2026-09-10,
# closed for build-rust and test by #323 (internal/goal/pipeline.go) and left open
# here. The image would carry versions no commit names, and the lockfile publish
# fingerprints would move after publish hashed it, so the next plan replays the
# whole publish for a change no commit made. With the flag, a manifest that needs
# a new resolution stops the publish and asks for a deliberate `cargo update`.
# Verified before it was added (2026-09-11): `cargo metadata --locked
# --manifest-path rust/embed-core/Cargo.toml` exits 0, so this changes no build
# that was already correct. TestEveryCargoBuildInTheImageIsLocked holds every
# cargo command in this script to it.
CC_x86_64_unknown_linux_gnu="$CC_SHIM" \
CXX_x86_64_unknown_linux_gnu="$CXX_SHIM" \
CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER="$CC_SHIM" \
CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUSTFLAGS="-C link-arg=-Wl,-soname,libembed_core.so" \
  cargo build --release --locked --target x86_64-unknown-linux-gnu \
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
# -buildvcs=false -trimpath: THE SAME SOURCE MUST BE THE SAME BINARY ON EVERY COMMIT.
#
# imgtar's layer cache keys every layer on the hash of imgtar's own executable
# (layerKey: a change to how layers are written must invalidate them). A plain
# `go build` in a git checkout stamps vcs.revision, vcs.time and vcs.modified into
# the binary — the one published on 2026-09-11 carries vcs.revision=2cfa4a70… —
# so ANY commit, to any file, gave imgtar a new hash and every cached layer a new
# key: the ~12 minutes of packing came back on the first publish after every
# merge, for byte-identical layers. Without the stamp (and without the checkout's
# path) the binary moves only when its source or the Go toolchain does, which is
# what the key is meant to follow. TestImgtarIsBuiltWithoutTheCommitInIt runs
# this exact command and holds it to that.
go build -trimpath -buildvcs=false -o "$ROOT/.local/bin/imgtar.exe" ./scripts/local/imgtar
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
# A VERSION IS DIGITS AND DOTS, AND NOTHING ELSE MAY REACH THE sed PROGRAM BELOW.
#
# ORT_VERSION is an operator override, and the pin lookup interpolates it into a
# sed script. Unchecked, `ORT_VERSION='x//;e id;#'` would have run `id` on the
# build host — GNU sed's `e` command — before the empty-pin guard could refuse the
# version, and the same value flows into the download URL. Found by review of
# #324, in a line this branch had just added. Rejecting anything that is not a
# dotted number closes it for every later use at once, rather than escaping it
# for one.
case "$ORT_VERSION" in
  ''|*[!0-9.]*|.*|*.|*..*) die "ORT_VERSION='$ORT_VERSION' is not a dotted version number" ;;
esac
# THE CHECKSUM TOO, AND FROM THE SAME PLACE AS THE VERSION.
#
# The comment above said "pinned and checksummed exactly as fetch-model.sh pins
# it", and until 2026-09-11 only the first half was true: this block downloaded the
# tarball with a bare curl and untarred it, while fetch-model.sh — which fetches the
# SAME package for the local embedder — verifies a pinned sha256 and refuses an
# unverified native runtime. So the libonnxruntime.so every published image loads
# into the server process was the one artefact in the image nothing checked. An
# independent review of the publish step found it.
#
# The sha is read out of fetch-model.sh's pin table rather than copied here, for
# the reason the version already is: one place decides, so the image and the
# embedder cannot drift apart. A version with no pin there fails closed.
ORT_PKG="onnxruntime-linux-x64-${ORT_VERSION}"
ORT_SHA="$(sed -n "s/^[[:space:]]*${ORT_PKG})[[:space:]]*ORT_SHA=\([0-9a-f]\{64\}\)[[:space:]]*;;.*/\1/p" scripts/fetch-model.sh | head -1)"
[ -n "$ORT_SHA" ] || die "scripts/fetch-model.sh pins no sha256 for $ORT_PKG — refusing to bake an unverified native runtime into the image"
say "ONNX Runtime $ORT_VERSION (linux x64, sha256 ${ORT_SHA:0:12}…)"
install -d "$ROOTFS/data/mcp-3gpp/models/onnxruntime"
curl -fsSL -o "$STAGE/ort.tgz" \
  "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${ORT_PKG}.tgz"
ORT_GOT="$(sha256sum "$STAGE/ort.tgz" | cut -d' ' -f1)"
[ "$ORT_GOT" = "$ORT_SHA" ] \
  || die "ONNX Runtime $ORT_PKG: downloaded sha256 $ORT_GOT, pinned $ORT_SHA in scripts/fetch-model.sh — refusing it"
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
  # -l links instead of copying, for the same reason and under the same two
  # assumptions as stage() below: 6.85 GB of model weights that nothing writes
  # to after this point. Falls back to a plain copy where links are unavailable.
  cp -al data/models/bge-m3-sparse "$ROOTFS/data/mcp-3gpp/models/" 2>/dev/null     || cp -a data/models/bge-m3-sparse "$ROOTFS/data/mcp-3gpp/models/"
  if [ -d data/models/bge-reranker-v2-m3 ]; then
    cp -al data/models/bge-reranker-v2-m3 "$ROOTFS/data/mcp-3gpp/models/" 2>/dev/null       || cp -a data/models/bge-reranker-v2-m3 "$ROOTFS/data/mcp-3gpp/models/"
  fi

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
    # DATA_CONTRACT picks the level (dense | dense+sparse | dense+sparse+etsi),
    # and data-contract.sh alone decides what an UNSET one means. The two lines
    # below used to print `${DATA_CONTRACT:-dense}` — the third opinion the
    # paragraph above forbids, and a wrong one: the default became
    # dense+sparse+etsi on 2026-09-07, and all seven publishes since logged
    # "corpus contract (dense)" above --require-sparse --require-etsi.
    # internal/goal reads the real default out of data-contract.sh for publish's
    # fingerprint; a label here must not be a second one.
    # DATA_ETSI_DB points --require-etsi at the local layout rather than the
    # image's absolute path.
    CONTRACT_FLAGS="$(DATA_ETSI_DB="$ROOT/data/etsi.duckdb" \
                      DATA_EMBED_FLOOR="${EMBED_FLOOR:-Rel-99}" \
                      bash scripts/data-contract.sh)" \
      || die "scripts/data-contract.sh refused DATA_CONTRACT='${DATA_CONTRACT:-}'"
    say "corpus contract (DATA_CONTRACT='${DATA_CONTRACT:-}', empty = its default): $CONTRACT_FLAGS"
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
    #
    # NOT RE-RUN WHEN THE PIPELINE CERTIFIED THESE BYTES. `goal`'s validate step
    # runs this same binary under these same flags minutes earlier and leaves a
    # certificate naming the flags, the floor and the size+mtime of both corpus
    # files; the publish step checks all of it against the files as they are now
    # and passes CORPUS_CONTRACT_CERTIFIED only when nothing differs
    # (internal/goal/contract_certificate.go). It was 8m15 of a 40m38 publish on
    # 2026-09-11, spent re-deriving a verdict nothing could have changed. A run of
    # this script by hand has no certificate and validates as it always did.
    if [ -n "${CORPUS_CONTRACT_CERTIFIED:-}" ]; then
      say "corpus contract NOT re-run: $CORPUS_CONTRACT_CERTIFIED"
    else
      # shellcheck disable=SC2086 # intentional word-split: the contract is a flag list
      EMBED_MODEL=bge-m3-sparse "$VALIDATE" --db data/3gpp.duckdb --report text $CONTRACT_FLAGS \
        || die "the corpus does not satisfy its own contract — refusing to bake it"
    fi
  else
    say "WARNING: validate is not built; baking WITHOUT the contract check"
  fi

# stage <src> <dst> — put a large file in the rootfs WITHOUT copying its bytes.
#
# Staging the corpus used to be `cp`, and the corpus is 42.8 GB. Measured on the
# 2026-09-05 publish, which took 54m37s end to end:
#
#     13:55:46 -> 14:04:57   ~9 min   staging (this copy, and the models)
#     14:04:57 -> 14:11:11   ~6 min   packing the layer tars
#     14:11:11 -> 14:19:12   ~8 min   crane reading them back to digest
#     14:19:12 -> 14:50:16  ~31 min   the upload itself
#
# So 23 of the 54 minutes never touched the network, and the largest single piece
# of that was writing a second copy of 42.8 GB onto the same disk it was read
# from. A hard link is the same bytes under a second name: imgtar reads the file
# content and cannot tell the difference, and the tar it writes is identical.
#
# WHY THIS IS SAFE HERE, and it is worth being explicit because a hard link to a
# 23 GB corpus is a loaded gun if either assumption stops holding:
#
#   - NOTHING WRITES TO THE STAGED FILE. After this point the rootfs copy is only
#     read — by imgtar when it packs, and by elfneeded which does not look at
#     .duckdb at all. The identity guards below read data/3gpp.duckdb, the
#     ORIGINAL, not this name. A write through the link would corrupt the corpus.
#   - THE ROOTFS IS DELETED, NEVER TRUNCATED. `rm -rf "$STAGE"` at the top of this
#     script unlinks; the original keeps its own link and its bytes.
#
# It falls back to cp when the link cannot be made — a different volume, or a
# filesystem without hard links — so the build still works, just at the old cost.
stage() {
  local src="$1" dst="$2"
  rm -f "$dst"
  ln "$src" "$dst" 2>/dev/null && return 0
  cp "$src" "$dst"
}

  say "corpus: $(du -h data/3gpp.duckdb | cut -f1) 3GPP + $(du -h data/etsi.duckdb 2>/dev/null | cut -f1) ETSI"
  # ETSI travels in the SAME image and stays a SEPARATE file: the entrypoint adds
  # -etsi-db when it finds one, so get_spec, list_specs and the LI tools cover
  # both corpora without either being merged into the other.
  stage data/3gpp.duckdb "$ROOTFS/data/mcp-3gpp/3gpp.duckdb"
  [ -s data/etsi.duckdb ] && stage data/etsi.duckdb "$ROOTFS/data/mcp-3gpp/etsi.duckdb"
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
  DB_ID="$(field embedding_model "$counters")"
  DB_SPARSE="$(field sparse_model "$counters")"
  SPARSE_ROWS="$(field clauses_with_sparse "$counters")"
  unset EMBED_MODELS_CONFIG
  echo "  dense  registry=$EMBED_ID corpus=${DB_ID:-<none>}"
  echo "  sparse registry=${SPARSE_ID:-<none>} corpus=${DB_SPARSE:-<none>} rows=${SPARSE_ROWS:-0}"
  # AN ABSENT IDENTITY IS NOT A MATCHING ONE.
  #
  # Both guards used to lead with `[ -n "$DB_…" ] &&`, so a corpus that stated no
  # identity at all skipped the comparison and passed. That is not hypothetical
  # for the sparse half: `embed-io --import-sparse` writes sparse_model only AFTER
  # its import loop, so a killed import leaves millions of postings and no stamp —
  # the state this very corpus was in on 2026-09-01. With the default
  # DATA_CONTRACT=dense, --require-sparse is not even in the flag list, and this
  # was the only thing left looking. The image would then bake postings whose
  # model nobody could name, and the sparse arm scores a query against whatever
  # vocabulary they came from, silently.
  #
  # Same defect cmd/server/checkdata.go was fixed for in eb0c159 ("two empty
  # identities compare equal"), in the script that actually publishes.
  [ -n "$DB_ID" ] ||
    die "the corpus states no embedding_model — it has an identity to declare and does not; the serve guard would disable vector search"
  [ "$DB_ID" = "$EMBED_ID" ] ||
    die "the baked registry is $EMBED_ID but the corpus was embedded with $DB_ID — the serve guard would disable vector search"
  if [ "${SPARSE_ROWS:-0}" -gt 0 ]; then
    [ -n "$SPARSE_ID" ] ||
      die "the corpus carries $SPARSE_ROWS sparse posting(s) but the baked registry declares no sparse head"
    [ -n "$DB_SPARSE" ] ||
      die "the corpus carries $SPARSE_ROWS sparse posting(s) and no sparse_model — an import that was killed before it stamped; re-run it"
    [ "$DB_SPARSE" = "$SPARSE_ID" ] ||
      die "baked sparse model $SPARSE_ID but the postings were built with $DB_SPARSE"
  fi
fi

# ---- 7. layers ---------------------------------------------------------------
# Split most-stable first so a re-push moves only what changed. Ownership is set
# in the tar rather than by a RUN chown: uid/gid 10001 is what the entrypoint and
# the read-only data path expect.

# LAYERS ARE WRITTEN GZIPPED, AND REUSED WHEN NOTHING IN THEM MOVED.
#
# Measured on the 2026-09-11 publish (40m38): 6m18 packing 42 GB of tars, then
# ~15 minutes of crane gzipping them again to compute digests — for layers the
# registry answered "existing blob" to. imgtar now writes the gzip stream crane
# itself would (compress/gzip, BestSpeed, default header: the same blob digest,
# measured), so crane uses each .tar.gz as-is; and --cache hands back last run's
# .tar.gz, by hard link, when every file of the layer has the same name, size and
# content-or-mtime (see layerKey in scripts/local/imgtar). An unchanged corpus half
# is then neither re-read nor re-compressed. The cache lives OUTSIDE $STAGE, which
# `rm -rf` clears on every run.
#
# EACH LAYER LEAVES ITS IDENTITY BESIDE IT (<layer>.id: digest, size, diff_id),
# hashed by imgtar on the way out while it writes the layer, and kept in the cache
# with the blob. Step 8 assembles the image from those records instead of letting
# crane re-read 26 GB of blobs and gunzip 42 GB to learn them (see identity.go).
LAYER_CACHE="$ROOT/.local/image-cache"
layer() { # layer <name> <uid> <path…>
  local name="$1" uid="$2"; shift 2
  local present=0 p
  for p in "$@"; do [ -e "$ROOTFS/$p" ] && present=1; done
  [ "$present" = 1 ] || return 0
  "$IMGTAR" pack --root "$ROOTFS" --out "$LAYERS/$name.tar.gz" --gzip --cache "$LAYER_CACHE" \
    --uid "$uid" --gid "$uid" "$@"
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
# ONE LAYER PER HALF, not one for both.
#
# The registry dedupes by layer digest, so a half that did not change is answered
# with "existing blob" and never crosses the wire. Both halves in one tar threw
# that away: any byte written to either corpus rewrote a single 42.8 GB layer.
#
# Measured on the 2026-09-05 publish. Seeding 679 glossary rows grew
# 3gpp.duckdb by 9.7 MB and left etsi.duckdb IDENTICAL TO THE BYTE
# (19 707 736 064 before and after, recorded in .local/state/published.json) —
# and 19.7 GB of unchanged ETSI went up the wire anyway, because it shared a tar
# with the half that moved. The push took 54m37s; roughly 25 minutes of it
# carried data the registry already had.
#
# This is the rule stated at the top of this section — "split most-stable first
# so a re-push moves only what changed" — applied to the one layer where it pays.
# The first publish after this change re-uploads everything once, because the
# split gives both halves new digests; every publish after it moves only the half
# that actually changed.
[ "$WITH_CORPUS" = 1 ] && layer 50-corpus-3gpp 10001 data/mcp-3gpp/3gpp.duckdb
[ "$WITH_CORPUS" = 1 ] && layer 51-corpus-etsi 10001 data/mcp-3gpp/etsi.duckdb
layer 60-bin        0     usr/local/bin usr/local/lib

LAYER_FILES=()
for f in "$LAYERS"/*.tar.gz; do [ -e "$f" ] && LAYER_FILES+=("$f"); done
[ "${#LAYER_FILES[@]}" -gt 0 ] || die "no layers were produced"

# ---- 8. assemble + push ------------------------------------------------------

if [ "$PUSH" = 0 ]; then
  LAYER_ARGS=()
  for f in "${LAYER_FILES[@]}"; do LAYER_ARGS+=(-f "$f"); done
  say "assembling locally (no push) → $STAGE/image.tar"
  "$CRANE" append --platform linux/amd64 -b "$BASE" "${LAYER_ARGS[@]}" -t "$TAG" -o "$STAGE/image.tar"
  echo "wrote $STAGE/image.tar"
  exit 0
fi

# RETRY THE PUSH. This moves ~25 GB (two DuckDB corpora plus 6.4 GB of models),
# over a link this build does not control, in one shot — and a single transient
# error would otherwise throw all of it away and leave :latest pointing at the
# previous image with no sign that anything was attempted.
#
# Retrying is cheap and safe, and that is a property of two earlier decisions
# rather than an assumption: the registry dedupes by digest, and imgtar stamps a
# FIXED timestamp on every member, so re-packing the same tree yields the same
# sha256 (verified: two packs with the files touched between them gave one
# digest). A second attempt therefore re-checks the blobs already stored and
# uploads only what is missing.
push_retry() {
  local attempt=1 max="${IMAGE_PUSH_ATTEMPTS:-4}" delay=20
  while :; do
    if "$@"; then return 0; fi
    if [ "$attempt" -ge "$max" ]; then return 1; fi
    say "push attempt $attempt/$max failed — retrying in ${delay}s (stored blobs are skipped by digest)"
    sleep "$delay"
    attempt=$((attempt + 1))
    delay=$((delay * 2))
  done
}

# THE IMAGE IS ASSEMBLED FROM THE LAYERS' RECORDS; crane ONLY MOVES BYTES.
#
# This was `crane append -b "$BASE" -f <layer>… -t "$TAG"`, and crane cannot be
# told what a layer is: every -f goes through tarball.LayerFromFile, which reads
# the blob to sha256 it and reads it again, gunzipped, for the diff_id — before the
# first request. On 2026-09-11 that was 7m42 (12:27:50 -> 12:35:32 in
# .local/logs/20260911T101313Z-publish.log) to learn digests imgtar had just
# produced, for seven layers of which the registry already held all but the binary.
#
# Now the same three things happen, in the open:
#   - the base is pulled as an OCI layout (the ~28 MB image crane append pulled);
#   - `imgtar oci` appends each layer from the identity recorded beside it and
#     writes the image crane append would have built, byte for byte — the
#     manifest it pushed that day, sha256:778f0d31…, is the fixture of
#     TestAssemblyIsByteIdenticalToCraneAppend. A layer with no valid record is
#     read to compute one, the old cost, and says so; none is guessed;
#   - `crane push` sends the layout. It takes a layout's descriptors as written and
#     opens a layer only to upload it, so a blob the registry holds is answered
#     "existing blob" without a byte of it being read.
# Nothing else changes: the same registry calls, the same "existing blob" dedupe,
# the same `crane mutate` below, the same config read-back.
pull_base() {
  # Fresh every attempt: crane pull ADDS to a layout that exists, and a base
  # layout holding two images is one imgtar oci refuses.
  rm -rf "$STAGE/base-oci"
  "$CRANE" pull --format=oci --platform linux/amd64 "$BASE" "$STAGE/base-oci"
}
say "assembling onto $BASE from the layers' identity records (no layer is read)"
push_retry pull_base || die "cannot pull $BASE after ${IMAGE_PUSH_ATTEMPTS:-4} attempts — nothing was published"
rm -rf "$STAGE/oci"
"$IMGTAR" oci --base "$STAGE/base-oci" --out "$STAGE/oci" "${LAYER_FILES[@]}" \
  || die "could not assemble the image — nothing was published"
# The manifest digest imgtar computed; the tag must name exactly this after the push.
OCI_INDEX="$(<"$STAGE/oci/index.json")"
ASSEMBLED="${OCI_INDEX##*'"digest":"'}"
ASSEMBLED="${ASSEMBLED%%'"'*}"

say "pushing $TAG"
push_retry "$CRANE" push "$STAGE/oci" "$TAG" \
  || die "crane push failed after ${IMAGE_PUSH_ATTEMPTS:-4} attempts — nothing was published"

say "setting the image config"
DIGEST="$("$CRANE" digest "$TAG")"
[ "$DIGEST" = "$ASSEMBLED" ] \
  || die "after the push $TAG names $DIGEST, not the image assembled here ($ASSEMBLED) — refusing to set a config on it"
# The config mutation is a manifest write, not a blob upload, but it lands on the
# same link — and a tag left carrying layers with no entrypoint is worse than one
# that was never moved.
# EVERY PATH BELOW IS A PATH *INSIDE A LINUX IMAGE*, AND MSYS REWRITES THEM.
#
# This is the trap `field()` documents 370 lines above, hitting a second time and
# far harder. Git Bash rewrites an argument shaped like NAME=/posix/path into a
# Windows path on its way to a native .exe, and crane.exe is a native .exe. Every
# one of these arguments has that shape. What was actually published on
# 2026-09-03 was:
#
#   PATH=C:\Program Files\Git\usr\local\sbin;C:\Program Files\Git\usr\local\bin;…
#   MCP3GPP_CACHE=C:/Program Files/Git/data/mcp-3gpp
#   ORT_DYLIB_PATH=C:/Program Files/Git/data/mcp-3gpp/models/onnxruntime/lib/…
#   WorkingDir=C:/Program Files/Git/home/mcp
#
# Note the ':' list separators turned into ';' — that is MSYS's PATH-list
# conversion, and it is the signature. The entrypoint is resolved through that
# PATH, so the container did not merely misbehave: it could not start at all. The
# corpus was correct, every data gate was green, the local server proved every
# retrieval arm over real JSON-RPC — and the artefact a consumer pulls was dead.
# No producer-side check could see it, because none of them reads the config.
MSYS2_ARG_CONV_EXCL='*' MSYS_NO_PATHCONV=1 \
push_retry "$CRANE" mutate "$TAG" -t "$TAG" \
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
  >/dev/null \
  || die "crane mutate failed after ${IMAGE_PUSH_ATTEMPTS:-4} attempts — the tag carries layers but no config"

# READ THE CONFIG BACK. Setting it is not evidence that it landed.
#
# The mutation above exited 0 while writing Windows paths into a Linux image, and
# stayed that way through a publish, a `prove` and a runbook that called it done —
# because nothing ever looked. This is the same shape as the guard that could not
# read its own input: an assertion that never runs is not an assertion.
say "verifying the published config"
CFG="$("$CRANE" config "$TAG" 2>/dev/null)" \
  || die "cannot read back the config just written to $TAG"
for want in \
  '"PATH=/usr/local/sbin:' \
  '"MCP3GPP_CACHE=/data/mcp-3gpp"' \
  '"LD_LIBRARY_PATH=/usr/local/lib"' \
  '"ORT_DYLIB_PATH=/data/mcp-3gpp/models/onnxruntime/lib/libonnxruntime.so"' \
  '"EMBED_MODELS_CONFIG=/data/mcp-3gpp/models/models.yaml"' \
  '"WorkingDir":"/home/mcp"'
do
  case "$CFG" in
    *"$want"*) ;;
    *) die "the published config does not carry ${want} — a Linux image was written with
  host paths (MSYS argument conversion). The tag now points at an image that
  cannot start. Re-run with MSYS2_ARG_CONV_EXCL='*' in the environment." ;;
  esac
done
case "$CFG" in
  *'C:'*|*'Program Files'*)
    die "the published config contains a Windows path — see above; refusing to call this published" ;;
esac
echo "  config OK — entrypoint, workdir and all five paths are POSIX"

say "published $TAG"
echo "  base   $BASE (appended, $DIGEST before config)"
echo "  digest $("$CRANE" digest "$TAG")"
"$CRANE" config "$TAG" | head -c 400; echo
