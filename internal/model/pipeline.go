package model

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Pipeline component versions. BUMP the relevant one whenever the corresponding
// indexing mechanic changes (parser output shape, chunking strategy, DB schema):
// the change flows into the build identities below, and a delta merge whose base
// carries a different identity is rebuilt from scratch instead of reused — you
// can't incrementally mix data produced by different indexing mechanics
// (CLAUDE.md / plan §15 invariant #2).
const (
	ParserVersion   = "html-v2"        // htmlparse / ooxml clause+table extraction (v2: release from the convert/<Rel>/ dir, multi-part -N + 6-digit version codes — forces a full re-ingest so already-'done' specs are re-attributed)
	ChunkingVersion = "clause-leaf-v1" // one chunk per clause leaf (no token windows)
	SchemaVersion   = "1"              // mirrors store schema_meta "schema_version"
)

// digest12 is the canonical short identity digest used by every build identity:
// the first 12 hex chars of sha256 over the '|'-joined parts. Deterministic and
// order-sensitive — callers that fold an unordered set (e.g. subject footprints)
// must sort it before passing it in.
func digest12(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])[:12]
}

// SpecIngestParts are the inputs that, if any of them change, mean a spec's
// ingested CONTENT (clauses, tables, subject rows) is no longer comparable to a
// previously-ingested copy — so the ingest_log "done" ledger for the owning
// specs must be invalidated and those specs re-ingested.
//
// Deliberately EXCLUDES the embedding model: a model bump must re-embed (carried
// by EmbedIdentity / the per-clause embedding_hash), NOT re-ingest — folding the
// model into the ingest ledger would invalidate the whole ingest_log on a model
// bump and destroy the micro-granular embed (plan PR-3 §"Why the split").
//
// Likewise EXCLUDES the global enrichers (catalog/openapi/evolutions): those are
// post-merge, corpus-global passes — they belong to GlobalEnrichmentIdentity and
// must not gate the per-spec loop.
type SpecIngestParts struct {
	ParserVersion       string   // parser code/output-shape tag
	ParserSourceHash    string   // optional content-derived parser hash (empty = rely on ParserVersion)
	ChunkingVersion     string   // chunking strategy tag
	SchemaVersion       string   // DB schema tag
	SubjectFootprints   []string // per-subject footprints (subjectmeta.IngestFootprints)
	SubjectSourceHashes []string // optional per-subject source hashes (extractor content)
	ASN1ScannerHash     string   // TS 33.128 ASN.1 scanner tag (subjectmeta.ASN1ScannerVersion)
}

// SpecIngestIdentity is the canonical gate for the ingest_log ledger. Two ingests
// with the same identity produced comparable spec content; a mismatch means the
// resume "done" rows are stale and must be wiped + re-ingested. It is stamped per
// ingest_log row (via MarkIngestStarted) and checked by ResetIngestLog /
// IsIngestDone. A subject footprint shift (incl. an ASN.1 scanner change) or a
// parser/chunking/schema change flips it; a model bump does NOT.
func (p SpecIngestParts) Identity() string {
	subj := append([]string(nil), p.SubjectFootprints...)
	sort.Strings(subj)
	src := append([]string(nil), p.SubjectSourceHashes...)
	sort.Strings(src)
	parts := []string{
		"spec-ingest-v1",
		p.ParserVersion, p.ParserSourceHash,
		p.ChunkingVersion, p.SchemaVersion,
		p.ASN1ScannerHash,
		strings.Join(subj, ","),
		strings.Join(src, ","),
	}
	return digest12(parts...)
}

// SpecIngestIdentity is a convenience wrapper over SpecIngestParts.Identity for
// callers that build the parts inline.
func SpecIngestIdentity(p SpecIngestParts) string { return p.Identity() }

// GlobalEnrichmentParts are the inputs to the post-merge, corpus-GLOBAL enricher
// passes (catalog overlay, OpenAPI ingest, evolutions seed). A change here means
// the enrichers must re-run on the merged DB — but it must NOT re-ingest specs,
// so this identity gates discover/merge refresh, never the per-spec loop.
type GlobalEnrichmentParts struct {
	CatalogFootprint     string
	CatalogSourceHash    string
	OpenAPIIngestVersion string
	OpenAPISourceHash    string
	EvolutionsSeedHash   string
}

// GlobalEnrichmentIdentity is the canonical gate for the global enricher refresh.
func (p GlobalEnrichmentParts) Identity() string {
	return digest12(
		"global-enrich-v1",
		p.CatalogFootprint, p.CatalogSourceHash,
		p.OpenAPIIngestVersion, p.OpenAPISourceHash,
		p.EvolutionsSeedHash,
	)
}

// GlobalEnrichmentIdentity is a convenience wrapper over the parts struct.
func GlobalEnrichmentIdentity(p GlobalEnrichmentParts) string { return p.Identity() }

// EmbedParts are the inputs that, if any change, mean a clause's stored vector is
// no longer comparable to a freshly-computed one — so the vector must be
// recomputed (re-embed), but the clause text/structure is untouched (no
// re-ingest). Carried at the clause level by embed.ClauseHash / embedding_hash.
type EmbedParts struct {
	ModelID           string // embedder identity, e.g. "bge-m3" (empty = lexical)
	ModelRevision     string // pinned weights revision (HF commit); empty when N/A
	TokenizerRevision string
	VectorDim         string // e.g. "1024"
	NormalizationMode string // e.g. "l2"
	Precision         string // e.g. "fp32"; an execution mode that changes numeric output
	// Windowing is the long-clause strategy: "truncate" (embed only the first
	// MaxTokens tokens) or "mean_pool" (window the whole clause and average). It
	// CHANGES the vector of any clause longer than MaxTokens, so a truncate corpus
	// and a mean_pool corpus must NEVER share one HNSW — hence it is an identity
	// component. (Was previously absent, letting the Rust embedder (truncate) and
	// the Go embedder (EMBED_WINDOWING=mean_pool) mix silently under one identity.)
	Windowing string
	// MaxTokens is the tokenizer truncation length, e.g. "1024". It decides which
	// tail of a long clause is embedded (truncate) or how windows are cut, so it is
	// an identity component too (Go used 512, Rust 1024 — a silent divergence).
	MaxTokens string
}

// EmbedIdentity is the canonical re-embed gate. A change flips it so the embed
// pass recomputes vectors; ingest is untouched (the split keeps a model bump from
// invalidating the ingest_log). PR-6 widens the parts that are actually populated;
// PR-3 establishes the canonical identity so ClauseHash, DB meta and serve-time
// compatibility checks all key on the SAME value.
func (p EmbedParts) Identity() string {
	return digest12(
		"embed-v1",
		p.ModelID, p.ModelRevision, p.TokenizerRevision,
		p.VectorDim, p.NormalizationMode, p.Precision,
		p.Windowing, p.MaxTokens,
	)
}

// EmbedIdentity is a convenience wrapper over the parts struct.
func EmbedIdentity(p EmbedParts) string { return p.Identity() }

// SparseParts are the inputs that define the BGE-M3 learned-lexical (sparse)
// postings. It is DELIBERATELY separate from EmbedParts/EmbedIdentity: the sparse
// arm is an ADDITIVE pass over the same clauses, so a sparse model/head change must
// trigger a sparse-only re-pass WITHOUT invalidating the (expensive, already-done)
// dense vectors. Folding sparse into EmbedIdentity would force a full dense
// re-embed — exactly what we must avoid (the dense BGE-M3 is already computed).
type SparseParts struct {
	ModelID           string // sparse model family, e.g. "bge-m3"
	ModelRevision     string // pinned weights revision
	TokenizerRevision string
	SparseOutput      string // ONNX sparse head node, e.g. "sparse_weights"; empty ⇒ no sparse head
}

// Identity is the canonical sparse re-pass gate. It is EMPTY when SparseOutput is
// empty (the model has no sparse head ⇒ no sparse arm to produce), so callers can
// treat "" as "not sparse-capable". A change to the sparse model/revision/head flips
// it, so the bake can detect a stale/missing sparse layer and re-run --sparse-only.
func (p SparseParts) Identity() string {
	if p.SparseOutput == "" {
		return ""
	}
	return digest12("sparse-v1", p.ModelID, p.ModelRevision, p.TokenizerRevision, p.SparseOutput)
}

// SparseIdentity is a convenience wrapper over the parts struct.
func SparseIdentity(p SparseParts) string { return p.Identity() }

// BuildIndex is the published identity manifest (plan PR-3): merge writes it
// alongside corpus-index.json / subject-index.json, and discover compares all
// three identities to decide whether a refresh is forced even when no spec
// version moved. It is plain JSON data so the CGO-free discover binary can read
// it without importing the store. An empty/missing field reads as "" and is
// treated as a mismatch by Differs, so a legacy publish with no build index
// self-heals on the next discover.
type BuildIndex struct {
	SpecIngestIdentity       string `json:"spec_ingest_identity"`
	GlobalEnrichmentIdentity string `json:"global_enrichment_identity"`
	EmbedIdentity            string `json:"embed_identity"`
}

// CurrentBuildIndex composes the three canonical identities for the CURRENT code
// from the inputs the caller can supply CGO-free (subject footprints + ASN.1
// scanner tag from subjectmeta, the embed model id from the DB meta or the
// configured embedder, and the global-enricher parts). Centralised so ingest,
// merge and discover compose the SAME SpecIngestIdentity instead of drifting —
// TestBuildIndexComposersAgree (and the per-binary uses) gate on this single
// definition. Parser/chunking/schema come from this package's constants.
func CurrentBuildIndex(subjectFootprints []string, asn1ScannerHash, embedModelID string, enrich GlobalEnrichmentParts) BuildIndex {
	return BuildIndex{
		SpecIngestIdentity: SpecIngestIdentity(SpecIngestParts{
			ParserVersion:     ParserVersion,
			ChunkingVersion:   ChunkingVersion,
			SchemaVersion:     SchemaVersion,
			SubjectFootprints: subjectFootprints,
			ASN1ScannerHash:   asn1ScannerHash,
		}),
		GlobalEnrichmentIdentity: GlobalEnrichmentIdentity(enrich),
		EmbedIdentity:            EmbedIdentity(EmbedParts{ModelID: embedModelID}),
	}
}

// Differs reports which of the three identities in the published build index
// disagree with the current code's expectation, returning the human-readable
// names of the mismatched identities (empty slice = fully up to date). discover
// uses this to force the affected refresh: a SpecIngestIdentity mismatch means
// re-ingest, a GlobalEnrichmentIdentity mismatch means re-run the enrichers, an
// EmbedIdentity mismatch means re-embed.
func (published BuildIndex) Differs(current BuildIndex) []string {
	var out []string
	if published.SpecIngestIdentity != current.SpecIngestIdentity {
		out = append(out, "spec_ingest_identity")
	}
	if published.GlobalEnrichmentIdentity != current.GlobalEnrichmentIdentity {
		out = append(out, "global_enrichment_identity")
	}
	if published.EmbedIdentity != current.EmbedIdentity {
		out = append(out, "embed_identity")
	}
	return out
}

// PipelineVersion is the LEGACY identity: a digest of parser, chunking, schema and
// the embedding model. It is retained ONLY as a read-compat alias for DBs/merge
// gates written before the SpecIngestIdentity split — new ingest writes use
// SpecIngestIdentity, new embed gates use EmbedIdentity. A DB stamped only with
// pipeline_version (no spec_ingest_identity) is treated as legacy by the resume
// gate, which prefers SpecIngestIdentity and never runs two gates that can
// diverge. Do not add new callers; migrate to the canonical identities instead.
func PipelineVersion(embeddingModelID string) string {
	return digest12(ParserVersion, ChunkingVersion, SchemaVersion, embeddingModelID)
}
