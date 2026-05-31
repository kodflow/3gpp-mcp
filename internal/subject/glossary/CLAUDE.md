<!-- updated: 2026-05-30T08:38:41Z -->
# internal/subject/glossary — Acronym Glossary Subject

## Purpose

The reference subject that seeds the acronym glossary from TS 21.905 (the 3GPP
vocabulary). It populates the generic `acronyms` table (served by the core
`resolve_term` tool) and **contributes no MCP tool of its own**. It exists as a
subject so the ingest core carries no hardcoded spec id.

## Structure

```text
glossary/
└── glossary.go   # parse TS 21.905 abbreviations → model.Acronym rows
```

## Conventions

- Owns spec id `21.905`; extraction runs during the ingest pass via
  `subject.IngestContext`.
- `reAbbrev` matches `ABBR<TAB|2+ spaces>Expansion` — the TAB/multi-space
  separator keeps prose out. Acronyms are domain/release-qualified (CLAUDE.md §8
  #5: `AMF` differs between 5GC and IMS legacy).
- Simplest reference implementation of the Subject contract — read it first when
  adding a new vertical.
