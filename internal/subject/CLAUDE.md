<!-- updated: 2026-05-30T08:38:41Z -->
# internal/subject — Domain-Vertical Plugin Contract

## Purpose

Defines the domain-vertical plugin contract. The generic retrieval core (ingest,
MCP tools, store schema, search) stays domain-agnostic; each business vertical —
Lawful Interception today, charging / roaming / security tomorrow — is a
self-contained Subject that registers here. The core iterates the registry
instead of hardcoding spec ids or tools: **adding a domain is adding a package,
not editing the core.**

## Structure

```text
subject/
├── subject.go    # contract: Subject, IngestContext, ToolRegistration, Registry
├── glossary/     # reference subject: seeds acronyms from TS 21.905
└── li/           # Lawful Interception (TS 33.128) + asn1/ scanner
```

## Import rule (no cycles)

- This package imports **only** `store`, `model`, and the mcp-go types.
- Concrete subjects import THIS package.
- The wiring that lists all subjects lives in `internal/registry` (which imports
  the subjects); the core (ingest/mcp) imports `registry` — **never** a subject
  directly.

## Contract surface

- `IngestContext` — everything a subject needs to extract structured artefacts
  from one parsed spec during the offline ingest pass.
- `ToolRegistration` — one MCP tool a subject contributes (tool + handler).
- `Registry` — the iterable set of enabled subjects.

## Conventions

- A subject is self-contained: its spec ids, ASN.1/registry data, tools and
  enrichment all live under its own package.
- Cite-or-silent applies inside subjects too (CLAUDE.md §1).
