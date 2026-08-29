# Installing the 3GPP MCP server

`mcp-3gpp` as released is a single self-contained binary. It exposes the 3GPP
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
| `3gpp.duckdb` (indexed corpus) | **12.36 GB** (~7.9 GB on the wire) | **private GHCR package** | always |
| `etsi.duckdb` (ETSI LI suite) | 23 MB | private GHCR package | ETSI deliverables (`--etsi`) |
| BGE-M3 + reranker models + ONNX Runtime | ~5 GB | HuggingFace + ORT release | semantic search only |

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

**Full semantic** (hybrid BM25 + BGE-M3 vectors + cross-encoder rerank, +~5 GB):

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

To point at a fork or a dated tag without rebuilding:

```sh
export MCP3GPP_GHCR_OWNER=your-org      # default: kodflow
export MCP3GPP_CORPUS_TAG=2026-08-26    # default: latest
```

## 4. Wire it into your MCP client

`mcp.json` — **lexical / default**:

```json
{
  "mcpServers": {
    "3gpp": { "command": "mcp-3gpp", "args": ["serve"] }
  }
}
```

With a locally built corpus, point at it directly and skip the cache:

```json
{
  "mcpServers": {
    "3gpp": { "command": "mcp-3gpp",
      "args": ["serve", "--db", "data/3gpp.duckdb", "--etsi-db", "data/etsi.duckdb"] }
  }
}
```

`serve` auto-detects cached models after `bootstrap --semantic`; the flags are
otherwise identical. To pin a baseline release, add `"--release", "Rel-19"`.

`serve` provisions the cache itself when it is empty, and keeps serving a cached
corpus when no token is present or the registry is unreachable — it degrades
rather than refusing to start. It never re-hashes 12.36 GB to decide whether an
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

## 6. Verify

```sh
mcp-3gpp version
mcp-3gpp serve            # prints: serving MCP on stdio (db=…, fts=true, hnsw=…)
```

Every answer carries an exact citation `{spec_id, release, version, clause, url}`.
If the server can't cite, it doesn't answer.
