<!-- updated: 2026-05-30T08:38:41Z -->
# internal/search — Intent Router & Retrieval

## Purpose

Hosts the regex-based intent router and the retrieval backends, fusing them with
Reciprocal Rank Fusion (RRF, k=60). Optional cross-encoder reranking via
`internal/rerank` (BGE-reranker-v2-m3, V2). See CLAUDE.md §3.

## Structure

```text
search/
├── search.go   # router + backends (BM25 / vector) + RRF fusion + rerank hook
└── doc.go      # routing-rules contract
```

## Routing rules (CLAUDE.md §3)

| Pattern | Backend |
|---------|---------|
| `TS \d\d\.\d\d\d` | BM25 filtered by spec |
| `remplace\|équivalent\|évolution\|migration\|maps to` | Graph (V2, KuzuDB) |
| `diff\|change\|évolution entre Rel-\d+ et Rel-\d+` | SQL on `changes` |
| `defined\|definition\|expansion` + ACRONYM | Glossary lookup |
| (fallback) | Hybrid BM25 + Vector + RRF |

## Conventions

- Routing is intentionally **simple regex** — no ML classifier. Keep it that way.
- Fusion is RRF with k=60; do not silently change k (it is part of the contract).
- Reranker is optional and degrades to a no-op when disabled — never block on it.
- `mode` (`hybrid`/`lexical`/`semantic`) selects backends; `li_test` /
  `mode_test` / `rerank_test` / `engine_shards_test` pin the routing behaviour.
