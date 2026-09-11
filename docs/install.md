# Installing the 3GPP MCP server

## The short way: pull the image

```jsonc
// .mcp.json
{
  "mcpServers": {
    "3gpp": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/kodflow/3gpp-mcp:latest"]
    }
  }
}
```

That is the whole installation. The image carries the 3GPP and ETSI corpora, the
dual-head BGE-M3 (dense + learned-lexical), the cross-encoder reranker and the
DuckDB `fts`/`vss` extensions, so every retrieval arm works with **no network at
run time and nothing downloaded on first start**.

Two things to know:

- The package is **private** — it holds verbatim specification text (see
  [`DATA_NOTICE.md`](../DATA_NOTICE.md)). `docker login ghcr.io` with a token
  carrying `read:packages` before pulling.
- **No `VOLUME` is declared, deliberately.** `serve` reads the baked corpus in
  place, read-only; a volume would make Docker copy ~50 GB into a fresh one on
  every `--rm` run.

`docker run --rm ghcr.io/kodflow/3gpp-mcp:latest version` prints the build, and
the `server_info` tool reports which retrieval arms are actually live — ask it
rather than assuming.

## The long way: the binary

> **Releases are no longer published from CI** — the workflows that built them
> are gone (they cost resources this project does not have). Build the binary
> yourself with `make build-bin`, or use the image above, which is the supported
> path.

`mcp-3gpp` is a single self-contained binary. It exposes the 3GPP
corpus to any MCP client (Claude Code, etc.) over stdio. There is **no service to
run, no Python, and no Ollama/LLM to install** — the binary is a *retrieval*
engine; your MCP client does the reasoning.

The *semantic* build is the one exception to "self-contained": it loads the
`embed-core` cdylib and ONNX Runtime at run time. The published archives are the
lexical and reranker builds — see "the semantic pair" below.

It needs data it does not ship, downloaded once into a per-user cache
(`~/.cache/mcp-3gpp/`, override with `MCP3GPP_CACHE`):

| Artifact | Size | Source | Needed for |
|---|---|---|---|
| `3gpp.duckdb` (indexed corpus) | **24.1 GiB** | **private GHCR package** | always |
| `etsi.duckdb` (ETSI: 5 142 TS/TR/EN deliverables in 11 822 published versions, not just the LI suite) | **18.4 GiB** | private GHCR package | ETSI deliverables (`--etsi-db`) |
| BGE-M3 (dense + sparse heads) + reranker + ONNX Runtime | **6.4 GB** (4.0 GiB gzipped) | HuggingFace + ORT release | semantic search only |

## Why the corpus needs a credential

The corpus stores **verbatim 3GPP/ETSI specification text**. 3GPP specs are free
to *download* from 3gpp.org; that is not a right to *redistribute* them. So the
published corpus lives in a **private** GHCR package, and pulling it requires a
GitHub token — see [`DATA_NOTICE.md`](../DATA_NOTICE.md).

This is not a temporary state: no release asset of this repository carries the
corpus, and the corpus would not fit in one anyway (GitHub caps an asset at
2 GB). If you have no access to the package, build the corpus yourself — see
[`local-pipeline.md`](./local-pipeline.md) — and point `--db` at the result.

## 1. Install the binary

```sh
curl -fsSL https://raw.githubusercontent.com/kodflow/3gpp-mcp/main/scripts/install.sh | sh
# installs mcp-3gpp into ~/.local/bin
```

Or grab the archive for your platform from the Releases page and extract
`mcp-3gpp` onto your `PATH`. The release carries **binaries and metadata only**.

## 2. Get a token

Create a classic token at <https://github.com/settings/tokens/new> and tick
**`read:packages`**. Then make it visible to the binary, by any of:

```sh
export GHCR_PAT=<token>            # or GITHUB_TOKEN on a CI runner
mcp-3gpp bootstrap --ghcr-token <token>
echo <token> > .local/ghcr.pat     # when running from a checkout; gitignored
```

## 3. Provision the cache

**Lexical only** (BM25 keyword search — no models):

```sh
mcp-3gpp bootstrap
```

**With the ETSI Lawful-Interception corpus** alongside:

```sh
mcp-3gpp bootstrap --etsi
```

**Full semantic** (hybrid BM25 + BGE-M3 dense/sparse vectors + cross-encoder
rerank, +6.4 GB of models):

```sh
mcp-3gpp bootstrap --semantic
```

The corpus pull is large. It **resumes** if interrupted: the compressed layer is
written to disk and continued with an HTTP `Range` request, then verified against
the digest the registry manifest names before it is unpacked. Re-running
`bootstrap` once the cache is populated costs one manifest request.

To serve from a mirror you host yourself, bypass the package entirely:

```sh
mcp-3gpp bootstrap --db-url https://your-host/3gpp.duckdb.zst --db-sha256 <sha>
```

The binary follows `latest` on purpose: `serve` checks it at each start (one
manifest request) and pulls a newer corpus when one is published — logging the
digest it resolved to. To point elsewhere without rebuilding:

```sh
export MCP3GPP_GHCR_OWNER=your-org      # default: kodflow
export MCP3GPP_CORPUS_TAG=2026-08-26    # one TAG for both packages; default: latest
# To FREEZE a corpus, pin it by digest — one variable per package, because a
# digest names one manifest of one package (MCP3GPP_CORPUS_TAG refuses a digest):
export MCP3GPP_CORPUS_REF_3GPP=sha256:<64 hex>   # `@sha256:…` is accepted too
export MCP3GPP_CORPUS_REF_ETSI=sha256:<64 hex>
```

A digest reference is verified: the manifest the registry serves must hash to
it, or nothing is transferred. `crane digest --full-ref ghcr.io/kodflow/3gpp-corpus:latest`
prints the current one. `--no-update` / `MCP3GPP_NO_UPDATE=1` keeps whatever the
cache already holds.

The **pipeline** (`make build`) does not follow `latest`: its `seed` steps pull
the digests committed in `contracts/corpus-pin.txt`, which
`scripts/local/publish-corpus.sh` bumps when it pushes a snapshot. The same
variables override the pin there.

## 4. Wire it into your MCP client

`mcp.json` — **lexical / default**:

```json
{
  "mcpServers": {
    "3gpp": { "command": "mcp-3gpp", "args": ["serve"] }
  }
}
```

With a locally built corpus, point at it directly and skip the cache — and set
**both** ONNX variables:

```json
{
  "mcpServers": {
    "3gpp": {
      "command": "mcp-3gpp",
      "args": ["serve", "--db", "data/3gpp.duckdb", "--etsi-db", "data/etsi.duckdb"],
      "env": {
        "EMBED_MODEL": "bge-m3-sparse",
        "EMBED_MODEL_DIR": "data/models/bge-m3-sparse",
        "ORT_DYLIB_PATH": ".local/toolchain/ort/onnxruntime-win-x64-gpu-1.20.1/lib/onnxruntime.dll",
        "ONNXRUNTIME_SHARED_LIBRARY_PATH": "data/models/onnxruntime/lib/onnxruntime.dll"
      }
    }
  }
}
```

Those are Windows paths. On Linux/macOS point both at the matching
`libonnxruntime.so` / `.dylib`: the two **pins** still differ, only the file
extension changes.

**TWO BINDINGS, TWO RUNTIMES, AND THEY ARE NOT INTERCHANGEABLE.**
`ORT_DYLIB_PATH` is the RUST crate's variable — `rust/embed-core`, the query
embedder — pinned to the build it was compiled against. `ONNXRUNTIME_SHARED_LIBRARY_PATH`
is the GO binding's, used by the cross-encoder reranker, on a different pin. One
file cannot satisfy both.

Setting only the first does not fail. The server starts, answers every query, and
serves with one of the four retrieval arms missing:

```text
The requested API version [25] is not available, only API versions [1, 20]
are supported in this build. Current ORT Version is: 1.20.1
… embedder=true reranker=false
```

`server_info` is the only place that says so, and it names the cause:

```json
{"reranker": false,
 "reranker_reason": "the ONNX runtime would not initialise: Platform-specific initialization failed: Error setting ORT API base: 2"}
```

Ask it after wiring, before concluding the install is good.

`serve` auto-detects cached models after `bootstrap --semantic`; the flags are
otherwise identical. To pin a baseline release, add `"--release", "Rel-19"`.

`serve` provisions the cache itself when it is empty, and keeps serving a cached
corpus when no token is present or the registry is unreachable — it degrades
rather than refusing to start. It never re-hashes 24 GiB to decide whether an
update exists: the published layer digests are recorded beside the DB, so the
check is one manifest request.

## 5. Which builds can actually do semantic search

The corpus carries 821 146 vectors and a frozen HNSW index, but reaching them at
query time needs a **query embedder**, and not every build has one. Since the
write-side→Rust cutover, `-tags onnx` alone means *reranker, no embed*.

| What you run | Lexical | Reranker | Semantic |
|---|:--:|:--:|:--:|
| `mcp-3gpp_*` release archive | ✅ | — | — |
| `mcp-3gpp-onnx_*` release archive | ✅ | ✅ | **—** |
| `ghcr.io/kodflow/3gpp-mcp:edge` (the `full` image) | ✅ | ✅ | ✅ |
| local build, `-tags onnx,embed_ffi` + `embed-core --features ort` | ✅ | ✅ | ✅ |

A build without an embedder does not fail a `mode=semantic` request — it answers
lexically. It now **says so**: the response carries `mode` (what actually ran)
and, when that differs from what you asked, `mode_requested` and
`mode_degraded`. `server_info` gives the reason.

The same goes for each retrieval arm of a call. `search_spec` answers with
`arms` — per corpus half and pass, every requested arm (`lexical`, `dense`,
`sparse`, `rerank`) with whether it ran, its hit count and its milliseconds — and
an arm that contributed nothing is listed in `degraded` and appended to
`mode_degraded`, with why: the search budget ran out before it
(`SEARCH_BUDGET`, default 20 s, ONE budget for the whole call across both
halves), its model or store call failed, or the capability is absent. A page
that was not cross-encoded never comes back looking as if it had been.

The budget is a SOFT deadline: it decides which arms still START, and it never
interrupts one that is already running — a cross-encoder pass is a blocking call
into ONNX Runtime, and cancelling a DuckDB query aborts the process instead of
returning an error. So a call can answer after its budget has expired; what it
cannot do is answer as if an arm the budget cut had run.

To build the semantic pair yourself:

```sh
cargo build --release --manifest-path rust/embed-core/Cargo.toml --features ort
go build -tags "duckdb_use_lib,onnx,embed_ffi" -o server ./cmd/server
# at run time, the cdylib and libonnxruntime must be loadable:
export ORT_DYLIB_PATH=<…>/libonnxruntime.so   # onnxruntime.dll on Windows
export EMBED_MODEL_DIR=<…>/models/bge-m3
```

Check it took: `server_info` must report `semantic: true` **and**
`embedding_model_client == embedding_model_db`. Equal ids are what guarantee the
query vector was produced by the same model as the corpus; different ids mean
the vectors cannot be compared and the server refuses rather than pretending.

## 6. Verify, and then use it

```sh
mcp-3gpp version
mcp-3gpp serve            # prints: serving MCP on stdio (db=…, fts=true, hnsw=…)
```

**The startup line is not the verification.** It says the server came up; it does
not say every retrieval arm did. Ask the server.

`serve` speaks newline-delimited JSON-RPC on stdio, so each frame is ONE line and
`initialize` comes first — a bare `tools/call` gets nothing:

```sh
printf '%s
'   '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"check","version":"1"}}}'   '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"server_info","arguments":{}}}'   | mcp-3gpp serve --db data/3gpp.duckdb --etsi-db data/etsi.duckdb
```

A **full semantic install carrying both corpora** answers:

```json
{"lexical": true, "semantic": true, "sparse": true, "reranker": true,
 "hnsw": true, "fts": true,
 "etsi": {"attached": true, "embedding_model_ok": true, "fts": true,
          "hnsw": true, "sparse": true}}
```

**Not every `false` is a fault**, and §5 says which build gives which. A lexical
archive legitimately reports `semantic: false` and `reranker: false`; started
without `--etsi-db`, `etsi.attached` is `false` by choice. What decides is the
motive: every disabled capability carries a `reason` / `reranker_reason` /
`sparse_reason`. The one that is easy to misread is a build that SHOULD do
semantic reporting `reranker: false` with `Error setting ORT API base` — that is
`ONNXRUNTIME_SHARED_LIBRARY_PATH` unset, see §4.

### Then: `help`

`help` counts inside the database actually being served rather than repeating
this document, and returns the question → tool map. It is the right first call in
a session, and the right call whenever a number here looks stale.

### The one rule that shapes every answer

Every answer that returns specification CONTENT carries an exact citation
`{spec_id, release, version, clause, url}` — `search_spec`, `get_spec`,
`search_api`, `trace_clause`, `find_cross_references`, `resolve_term`,
`li_events`, `trace_evolution`. **If the server cannot cite, it does not answer**
— it says what it does not hold instead. `help`, `server_info`, `list_specs` and
`list_releases` describe the server or its catalogue rather than the corpus, so
they carry no citations and do not pretend to.

A `count` of 0 from `get_changelog` means "not recorded here", never "this never
changed". It carries a `note` saying which silence you hit — with one gap worth
knowing: the ETSI explanation is only attached when an ETSI store is actually
attached, so asking for an ETSI deliverable on a server started without
`--etsi-db` gets the bare 0.

For evolution questions, reach for `trace_clause` rather than `get_changelog`:
it diffs the clause TEXT paragraph by paragraph, out of the corpus itself, so it
works on both halves and cannot go stale against a table nothing writes any more.
