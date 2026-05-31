<!-- updated: 2026-05-30T08:38:41Z -->
# internal/registry — Subject Wiring

## Purpose

The single place that knows which domain verticals exist. The core (ingest, mcp)
depends on this package, **never** on a concrete subject — so adding a domain is
adding it here plus its package, with zero edits to the core seams.

## Structure

```text
registry/
└── registry.go   # Default() → subject.New(li.New(), glossary.New())
```

## Conventions

- This is the only allowed import edge from "list of subjects" to concrete
  subjects (`li`, `glossary`). Keep the core ignorant of concrete subjects.
- To add a vertical: create its package under `internal/subject/`, then add one
  line here. Nothing else in the core changes.
