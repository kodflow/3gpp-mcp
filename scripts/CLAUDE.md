<!-- updated: 2026-05-30T08:38:41Z -->
# scripts/ — Corpus & Tooling Shell Scripts

## Purpose

Project-owned shell scripts for building the local 3GPP corpus, fetching models,
installing the binary, and demoing the server. These are the only offline steps
outside the Go binaries (CLAUDE.md §6). The query server itself stays pure-Go.

## Structure

```text
scripts/
├── corpus.sh          # single entry point: build the local 3GPP corpus
├── convert-origin.sh  # convert every mirrored spec ZIP → HTML (data/sources/convert)
├── lib/convert.sh      # shared DOC/DOCX → HTML conversion (LibreOffice)
├── recover-fails.sh    # re-pass specs that failed conversion (FAILCV in .run.log)
├── fetch-5g-apis.sh    # download 5GC OpenAPI YAMLs from 3GPP Forge (pinned SHA)
├── fetch-model.sh      # bootstrap BGE-M3 ONNX (only for `-tags onnx` builds)
├── install.sh          # fetch the mcp-3gpp binary into ~/.local/bin
└── demo.sh             # drive the server over stdio; print all 8 tools' output
```

## Conventions

- LibreOffice→HTML conversion is the corpus build path (DECISION 2026-05-25,
  overrides the §13 DOCX-only rule). `lib/convert.sh` is the shared converter;
  `corpus.sh` orchestrates, `recover-fails.sh` retries failures with a long timeout.
- Corpus artifacts live under `data/` (gitignored, ~37GB raw + ~37GB HTML); only
  `corpus.lock` is tracked. Never commit corpus or `*.duckdb`.
- External fetches pin immutable versions (Forge commit SHA, model revision) for
  reproducibility (CLAUDE.md §1: reproducible ingestion).
- `install.sh` is pure retrieval — no Python, no Ollama, no daemon.
