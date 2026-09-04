# 3gpp-mcp — documentation

An **MCP server** that exposes the 3GPP corpus — Phase 1 (1992) to the latest
Release — as a locally queryable index, returning **cited specification
fragments**, never summaries. Claude reasons; the index serves.

> This page used to be the landing page of the `devcontainer-template` this
> repository was generated from ("79 AI agents, 17 commands, 25 languages"). It
> documented a different project.

## What is in the corpus today

Measured on the built corpus, not estimated — see [`local-pipeline.md`](./local-pipeline.md):

| | |
|---|---:|
| Clause occurrences | 2 752 688 |
| Distinct specs / versions | 3 568 / 20 163 |
| Series / releases | 31 / Rel-4 → Rel-20 (plus Phase 1–2 on the old series) |
| 5GC API operations | 8 562 (+27 889 schemas) |
| LI events (Rel-19) | 405 |
| ETSI deliverables (separate DB) | 5 142 (11 822 versions, 3 169 614 clauses) |
| MCP tools | 13 |

## Start here

| If you want to… | Read |
|---|---|
| Use the server as a client | [`install.md`](./install.md) |
| Understand what it is for | [`vision.md`](./vision.md) |
| Understand how it is built | [`architecture.md`](./architecture.md) |
| Build the corpus yourself, on one machine with a GPU | [`local-pipeline.md`](./local-pipeline.md) |
| Know how retrieval is scored | [`eval-baseline.md`](./eval-baseline.md) |
| Know how the index is shaped | [`INDEXING.md`](./INDEXING.md) |
| Know how data is stored and shipped | [`data-pipeline.md`](./data-pipeline.md) |

## Decisions of record

| ADR | Decision |
|---|---|
| [0001](./adr/0001-write-side-rust-read-side-go.md) | Write side in Rust, read side in Go |
| [0002](./adr/0002-data-completeness-contract.md) | The data-completeness contract |
| [0003](./adr/0003-local-goal-pipeline.md) | The corpus is built on one machine, by `goal` |
| [0004](./adr/0004-content-addressed-corpus.md) | The corpus is content-addressed at paragraph granularity |

The frozen stack and the explicit lock-ins ("no Python at query time", "ETSI
stays separate", "merge before embed") live in [`../CLAUDE.md`](../CLAUDE.md).

## A word on the data

The corpus holds **verbatim 3GPP/ETSI specification text**. Free to download is
not free to redistribute: the published corpus is a **private** package, and no
release asset of this repository carries it. Read
[`../DATA_NOTICE.md`](../DATA_NOTICE.md) before moving any artifact anywhere.
