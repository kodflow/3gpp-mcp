package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Pipeline component versions. BUMP the relevant one whenever the corresponding
// indexing mechanic changes (parser output shape, chunking strategy, DB schema):
// the change flows into PipelineVersion, and a delta merge whose base carries a
// different PipelineVersion is rebuilt from scratch instead of reused — you can't
// incrementally mix data produced by different indexing mechanics (CLAUDE.md /
// plan §15 invariant #2).
const (
	ParserVersion   = "html-v1"        // htmlparse / ooxml clause+table extraction
	ChunkingVersion = "clause-leaf-v1" // one chunk per clause leaf (no token windows)
	SchemaVersion   = "1"              // mirrors store schema_meta "schema_version"
)

// PipelineVersion is a short, stable digest of every mechanic that, if changed,
// invalidates an existing index: parser, chunking, schema, and the embedding
// model (empty for a lexical build). Two DBs with the same PipelineVersion are
// merge-compatible; a mismatch means "rebuild, don't reuse the base".
func PipelineVersion(embeddingModelID string) string {
	parts := []string{ParserVersion, ChunkingVersion, SchemaVersion, embeddingModelID}
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])[:12]
}
