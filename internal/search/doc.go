// Package search hosts the intent router and the retrieval backends.
//
// Routing rules (CLAUDE.md §3, regex-based):
//
//	TS \d\d\.\d\d\d                                  → BM25 filtered by spec
//	remplace|équivalent|évolution|migration|maps to → Graph (V2, KuzuDB)
//	diff|change|évolution entre Rel-\d+ et Rel-\d+   → SQL on `changes`
//	defined|definition|expansion + ACRONYM           → Glossary lookup
//	(fallback)                                       → Hybrid BM25 + Vector + RRF
//
// Hybrid fusion uses Reciprocal Rank Fusion with k=60.
// Optional reranking via BGE-reranker-v2-m3 (V2).
package search
