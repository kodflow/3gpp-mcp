// Package store wraps the persistence layer.
//
//   - DuckDB (FTS + VSS HNSW) is the V1 backbone for catalog, clauses,
//     vectors, change records and acronyms.
//   - KuzuDB (embedded property graph) is added in V2 for NE↔NF and
//     RAT generation evolutions.
//
// V1 connectors only expose read-only query paths; writes go through the
// ingest pipeline (`internal/ingest`) which writes Parquet/CSV batches
// and lets DuckDB COPY them in bulk.
package store
