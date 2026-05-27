// Package ingest orchestrates the offline ingestion pipeline.
//
// Steps (CLAUDE.md §6):
//  1. Scrape 3GPP FTP (https://www.3gpp.org/ftp/Specs/archive/) — TS/TR only.
//  2. Parse DOCX via `internal/docx`.
//  3. Compute BGE-M3 embeddings via `internal/embed` (ONNX Runtime).
//  4. Seed glossary from TS 21.905 + regex miner.
//  5. (V2) CR pipeline from /ftp/tsg_*/.
//  6. Write everything into DuckDB through `internal/store`.
//
// Determinism is a goal: a given (corpus_state, ingest_version) must
// produce the same DB hash. Builds MUST be reproducible.
package ingest
