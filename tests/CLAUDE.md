<!-- updated: 2026-05-30T08:38:41Z -->
# tests/ — End-to-End Proofs

## Purpose

Cross-package, full-stack tests that exercise the whole pipeline in-process.
Unit tests live next to the code they cover (`*_test.go` per package); this tree
holds only the e2e proofs that need ingest + server + client together.

## Structure

```text
tests/
└── e2e/
    └── li_poc_test.go   # proof for "how many LI events does each NE/NF report
                          # over X2 to the MDF2?": ingest 33.128 → MCP server
                          # in-process → real client → answer from get_spec
```

## Conventions

- E2E flow: ingest converted HTML → DuckDB → stand up the MCP server in-process →
  drive it with a real mcp-go client → compose the answer **purely from tool
  output** (no test-side shortcuts) → assert exact citations.
- This validates the core promise: the service yields per-NF X2→MDF2 events with
  `{spec_id, release, version, clause, url}` citations (CLAUDE.md §1).
- Keep e2e tests hermetic (use fixtures / a small converted spec, not the full
  37GB corpus).
