# Installing the 3GPP MCP server

`mcp-3gpp` is a single static binary. It exposes the 3GPP corpus to any MCP
client (Claude Code, etc.) over stdio. There is **no service to run, no Python,
and no Ollama/LLM to install** — the binary is a *retrieval* engine; your MCP
client does the reasoning.

The binary needs two data artifacts, downloaded once into a per-user cache
(`~/.cache/mcp-3gpp/`, override with `MCP3GPP_CACHE`):

| Artifact | Size | Source | Needed for |
|---|---|---|---|
| `3gpp.duckdb` (indexed corpus) | ~1.7 GB | GitHub Release | always |
| BGE-M3 + reranker models + ONNX Runtime | ~5 GB | HuggingFace + ORT release | semantic search only |

## 1. Install the binary

```sh
curl -fsSL https://raw.githubusercontent.com/<OWNER>/<REPO>/main/scripts/install.sh | sh
# installs mcp-3gpp into ~/.local/bin
```

Or grab the archive for your platform from the Releases page and extract
`mcp-3gpp` onto your `PATH`.

## 2. Provision the cache

**Lexical only** (BM25 keyword search — light, no models, ~0.6 GB download):

```sh
mcp-3gpp bootstrap --db-url https://github.com/<OWNER>/<REPO>/releases/latest/download/3gpp.duckdb.zst \
                   --db-sha256 <sha>
```

**Full semantic** (hybrid BM25 + BGE-M3 vectors + cross-encoder rerank, ~6.5 GB):

```sh
mcp-3gpp bootstrap --semantic \
  --db-url https://github.com/<OWNER>/<REPO>/releases/latest/download/3gpp.duckdb.zst \
  --db-sha256 <sha>
```

`bootstrap` is resumable, verifies SHA-256, and is a no-op once the cache is
populated. `serve` also auto-falls back to the cache and, if models are absent,
degrades to lexical search (never blocks).

## 3. Wire it into your MCP client

`mcp.json` — **lexical / default**:

```json
{
  "mcpServers": {
    "3gpp": { "command": "mcp-3gpp", "args": ["serve"] }
  }
}
```

`mcp.json` — **semantic** (after `bootstrap --semantic`): identical; `serve`
auto-detects the cached models. To pin a baseline release, add
`"args": ["serve", "--release", "Rel-19"]`.

## 4. Verify

```sh
mcp-3gpp version
mcp-3gpp serve            # prints: serving MCP on stdio (db=…, fts=true, hnsw=…)
```

Every answer carries an exact citation `{spec_id, release, version, clause, url}`.
If the server can't cite, it doesn't answer.
