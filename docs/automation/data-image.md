# The image — built on the machine that has the corpus

## Where it comes from

```sh
make image          # cross-build, compose, push  ghcr.io/kodflow/3gpp-mcp:latest
make image-local    # same, stopping at .local/image/image.tar
make image-toolchain# fetch the Linux cross-toolchain, once
```

`scripts/local/build-image.sh` does all of it. There is no build workflow any
more: the two that used to bake this image moved ~14 GB per run, which is exactly
the resource this project does not have. The corpus is built here (ADR 0003), so
the image is built here too.

## No container runtime is involved

An OCI image is a base manifest, a list of layer tarballs and a config blob, and
`crane` composes all three. Nothing needs `docker build`:

| A Dockerfile would | Here |
|---|---|
| `RUN apt-get install libstdc++6 libgomp1` | the two `.so` files travel in a layer |
| `RUN groupadd/useradd mcp` | `/etc/passwd` is read out of the base and re-shipped with one line appended |
| `RUN mcp-3gpp prefetch-extensions` | the same `fts`/`vss` files are downloaded from the DuckDB extension repository |
| `COPY --from=builder` | the cross-compiled binary is staged and packed |

## The Linux artefacts are cross-compiled from Windows

`cmd/server` needs cgo for DuckDB, and this machine has no Linux toolchain and no
WSL distribution. `zig cc` supplies one. Three details each produce a **broken
image rather than an error**, so each has a guard:

1. **DuckDB's prebuilt `linux-amd64` archive is compiled against GNU libstdc++**,
   and zig ships LLVM's libc++. Linking against zig's C++ runtime fails with
   hundreds of undefined `std::__cxx11` symbols — the two do not share an ABI.
   The link uses **Debian bookworm's own** `libstdc++.so.6` / `libgomp.so.1`,
   the exact pair the runtime image carries, and those files are then shipped in
   the image. Pinning them to the runtime base is not cosmetic: building against
   a newer libstdc++ than the image has is how you get `version 'GLIBCXX_3.4.xx'
   not found` at startup.

2. **`cc-rs` passes `--target=x86_64-unknown-linux-gnu`** of its own accord,
   which zig rejects (`UnknownOperatingSystem`). `scripts/local/zigcc` drops any
   target the caller supplies and substitutes the configured one.

3. **Without an explicit `-soname`**, lld records the *absolute build path* as
   the Rust cdylib's `NEEDED` entry. The binary links here and the container dies
   looking for `C:/Users/.../libembed_core.so`.
   `scripts/local/elfneeded --require-sonames` asserts every `NEEDED` entry is a
   bare SONAME, and is what caught it.

`tar` is not used at all. GNU tar here treats a `C:` path as a remote rmt host,
the toolchain's is busybox's (no `--force-local`), and `crane export` on Windows
writes member names with **backslashes** — a layer packed that way would carry
`usr\local\bin\mcp-3gpp` as one member name, and the image would pull, start and
report `docker-entrypoint.sh: not found`. `scripts/local/imgtar` uses
`archive/tar` and always writes POSIX names.

## One image, not two

The old split (`3gpp-data` inherited by `3gpp-mcp` via `FROM`) existed because
"the bake produces non-reproducible bytes": each build re-created the ~14 GB data
layer with a fresh digest, so a three-line code change cost a 15 GB push.

`imgtar` stamps a **fixed timestamp** on every entry instead of the file's own,
so an unchanged corpus packs to byte-identical bytes, the same digest, and the
registry already has that blob — nothing is uploaded. (Measured: packing the same
tree twice, with the files `touch`ed in between, gives one sha256.) A single
image is then simpler and costs nothing, because the layer order runs from most
stable to least:

```text
10-runtime     /etc/passwd, /etc/group, libstdc++, libgomp
20-duckdb-ext  the fts + vss extensions, prefetched
30-ort         ONNX Runtime (the only arch-specific piece)
40-models      bge-m3-sparse + the reranker
50-corpus      3gpp.duckdb + etsi.duckdb          ← the big one, and the last
60-bin         mcp-3gpp, the entrypoint, libembed_core.so
```

A code-only rebuild moves layer 60. A corpus rebuild moves 50 and 60.

## What is in it, and why each piece has to be

- **The corpus, in place.** `serve` reads it read-only; there is no `VOLUME`, so
  a `docker run --rm` does not copy 11 GB into a fresh volume.
- **`bge-m3-sparse`, not `bge-m3`.** Only ONE registry entry is active at serve
  time (`SparseCapable()` reads `ActiveModel()`), so a dense-only image drops the
  learned-lexical arm even with a corpus full of sparse postings — silently,
  because `search.Engine` just leaves `e.sp` nil. The dual-head export serves
  both and stamps the same dense identity; that claim is measured, and
  `internal/embed/registry_dual_head_test.go` locks it.
- **The reranker**, so `search_spec(rerank: true)` works.
- **The DuckDB `fts` and `vss` extensions**, for the version the *Linux* binary
  links (read from the `go.mod` pin, currently 1.5.3 — not this machine's 1.4.3,
  which is what `duckdb_use_lib` gives the Windows build). Without them a
  no-egress container degrades BM25 to `LIKE` and HNSW to an exact scan, and says
  nothing.

## The guards that run before a push

- **The corpus contract**: `validate --require-fts --require-hnsw
  --require-embed-complete`. Baking a corpus that fails its own contract produces
  an image that serves lexically while claiming semantic capability.
- **Dense identity**: the baked registry's `EmbedIdentity` must equal the
  corpus's `embedding_model`. A mismatch is not a label problem — it is the serve
  guard about to disable vector search in the image being published.
- **Sparse identity**, when the corpus has a sparse layer: the registry must
  declare a sparse head, and it must be the one the postings were built with.
  Scoring a query against another model's vocabulary is wrong in silence.
