# Vision: 3gpp-mcp

## Purpose

A **Model Context Protocol (MCP) server** that exposes the entire 3GPP corpus —
from Phase 1 (1992) to the latest published Release — as an instantly queryable
index for Claude Code and any MCP client, **running locally, with zero
hallucination**.

The server never summarizes or paraphrases: it returns **cited specification
fragments** (`spec_id`, `release`, `version`, `clause`, `url`). Claude reasons;
the index serves.

## Problem Statement

- Telecom engineers (EPC/5GC core, RAN, LI, IMS) need exact, dated, sourced spec
  text — not a plausible approximation from a general LLM.
- 3GPP specs live as ~37 GB of DOCX across thousands of versions on an FTP
  archive; finding the right clause in the right release is slow and error-prone.
- General LLMs hallucinate 3GPP terminology: they confuse releases, invent IEs,
  and mix up N1/N2/N4 interfaces.
- Versions are non-monotonic and acronyms are context-dependent (AMF = Access
  and Mobility Management Function in 5GC, *or* a legacy IMS function).

## Target Users

Telecom engineers and tooling that need authoritative 3GPP retrieval inside an
AI workflow: "what does TS 33.128 say about LI_X2 toward MDF2 in Rel-19", "diff
TS 23.501 between Rel-18 and Rel-19", "what replaced the MME".

## Goals

1. **No hallucination** — every response carries `{spec_id, release, version,
   clause}` and the source URL. If it can't be cited, it isn't returned.
2. **Local-first** — everything runs on the user's machine at query time; the
   network is touched only during offline ingestion.
3. **Single binary** — `mcp-3gpp` is one static Go binary; `bootstrap` pulls the
   prebuilt index + models, then `serve` runs offline.
4. **Reproducible ingestion** — a pinned corpus state produces a deterministic
   index (stable hash), gated on retrieval-quality benchmarks.
5. **MCP returns documents, never summaries** — the server is a retrieval
   engine; synthesis happens client-side in Claude.

## Success Criteria

| Criterion | Target |
|-----------|--------|
| Query latency (BM25) | ~10 ms |
| Query latency (HNSW vector) | ~3 ms |
| Citation coverage | 100% of responses carry a citation block |
| Client setup | `bootstrap` + `serve`, < 1 GB index download, no rebuild |
| Index distribution | GitHub Release asset, SHA-256 verified |
| Corpus durability | mirrored to ghcr.io, independent of 3gpp.org uptime |
| Retrieval quality | `make bench` gate (Recall@k / nDCG@k) before any index ships |

## Scope

- **V1**: Rel-17/18/19, series 23, 24, 29, 33, 38 (~150 specs); FTS BM25 + HNSW
  vectors; glossary (TS 21.905 seed); changelog (Change History annex); 8 MCP
  tools. Lawful Interception (TS 33.128) is the proving use case.
- **V2**: KuzuDB NE↔NF evolution graph, full CR pipeline, historical ingestion
  back to Rel-15, reranker enabled by default.
- **V3**: Phase 1 → Rel-16 ingestion, multi-user deployment.

## Non-Goals

- Not a deployment platform — local-first MCP server.
- No server-side summarization — Claude synthesizes.
- No PDF parsing, no OCR — DOCX/HTML only.
- No local LLM (Ollama/vLLM) in the query path.

## Key Decisions

See [`docs/architecture.md`](./architecture.md) for the frozen technical stack
and [`docs/data-pipeline.md`](./data-pipeline.md) for the storage + CI/CD
strategy. Both are migrated from the original GitLab project and adapted for
GitHub distribution.
