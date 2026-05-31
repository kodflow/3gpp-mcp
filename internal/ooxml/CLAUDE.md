<!-- updated: 2026-05-30T08:38:41Z -->
# internal/ooxml — Native DOCX Spec Parser

## Purpose

Parses a 3GPP spec `.docx` directly from `word/document.xml` (`archive/zip` +
`encoding/xml` — the stack CLAUDE.md §2 prescribed), producing the same
`ParsedSpec` shape as `internal/htmlparse` so `internal/ingest` is a drop-in swap.

## Structure

```text
ooxml/
├── parse.go   # zip → document.xml → headings/clauses/ASN.1 blocks
└── table.go   # merged-table geometry: w:gridSpan (h) + w:vMerge (v)
```

## Why it exists alongside htmlparse

Unlike the LibreOffice→HTML round-trip, the native path:

- reconstructs merged **table geometry** (`w:gridSpan` horizontal, `w:vMerge`
  vertical);
- keeps full **Heading1..9** depth from `w:pStyle`;
- preserves `pStyle="PL"` **ASN.1 / protocol blocks verbatim** (axis #4).

Used for the ~45% of the corpus that is real `.docx`; the legacy `.doc` majority
goes through `htmlparse` (see its note on the 2026-05-25 decision).

## Conventions

- Clause-aware chunking only (CLAUDE.md §13); no PDF, no OCR.
- Stdlib-only parsing (`archive/zip`, `encoding/xml`) — no new dependency.
