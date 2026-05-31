<!-- updated: 2026-05-30T08:38:41Z -->
# internal/htmlparse — LibreOffice-HTML Spec Parser

## Purpose

Turns a LibreOffice-converted 3GPP spec (HTML) into the domain model: a `Spec`,
its `SpecVersion`, clause-level chunks, and Change-History rows. This is the
**primary** parser path.

## Structure

```text
htmlparse/
└── parse.go   # HTML → ParsedSpec (headings, clauses, tables, change history)
```

## Why HTML and not DOCX

~55% of the corpus is legacy binary `.doc` that `encoding/xml` cannot read, so
`scripts/corpus.sh` converts every spec to HTML via LibreOffice
(**DECISION 2026-05-25, overrides CLAUDE.md §13's DOCX-only rule**). LibreOffice
renders Heading1..Heading7 as `<h1>..<h6>` whose text is
`"<clause-number>\t<title>"`; body is `<p>/<li>/<td>`; tables are `<table>`.

## Conventions

- Chunk by **clause**, never a token window (CLAUDE.md §13): one leaf heading +
  text until the next heading.
- Emits the same `ParsedSpec` shape as `internal/ooxml` — the two are swappable.
- Pure parsing; no DB or network.
