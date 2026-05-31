package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ClauseHash is the per-clause embedding fingerprint: it changes iff the embedded
// text (heading + body, exactly what EmbedText feeds the model) OR the EmbedIdentity
// changes. Stored next to the vector so the decoupled embed step can re-embed
// ONLY clauses whose hash drifted — making repeat runs proportional to the clause
// delta, not the spec or corpus. Keep this the single definition of "what text we
// embed" so ingest and cmd/embed never disagree.
//
// The model component is the canonical model.EmbedIdentity (plan PR-3/PR-6), so
// the re-embed gate keys on the SAME identity as DB meta and serve-time
// compatibility checks. modelID is the embedder's ModelID(): for the production
// BGE-M3 backend that is ALREADY the full EmbedIdentity digest (model family +
// weight revision + tokenizer revision + dim + normalisation + precision — see
// embed.bgeModelID), so a change in any of those components re-embeds every clause
// WITHOUT re-ingesting. Wrapping it in EmbedParts.ModelID keeps a single
// definition of the embed component and stays deterministic for the lexical
// ("hash-local") and disabled ("") embedders too.
func ClauseHash(heading, text, modelID string) string {
	embedID := model.EmbedIdentity(model.EmbedParts{ModelID: modelID})
	h := sha256.Sum256([]byte(EmbedText(heading, text) + "|" + embedID))
	return hex.EncodeToString(h[:])[:16]
}

// EmbedText is the canonical text fed to the embedder for a clause. Centralised
// so ClauseHash and the actual Embed call always use the identical string.
func EmbedText(heading, text string) string { return heading + "\n" + text }

// Item is one clause to consider for embedding: its key, the text inputs, and the
// hash currently stored against its vector ("" when never embedded).
type Item struct {
	ChunkID    uint64
	Heading    string
	Text       string
	StoredHash string
}

// SetFunc persists a freshly-computed vector + its hash for one clause (e.g.
// store.SetEmbeddingWithHash). Returning an error aborts the batch.
type SetFunc func(ctx context.Context, chunkID uint64, vec []float32, hash string) error

// Apply embeds the items whose current hash differs from their StoredHash (or that
// were never embedded) and persists each via set. Items already up to date are
// skipped — this is the micro-granular re-embed. It batches in groups of 32
// (the BGE-M3 reproducibility contract) and returns the number of clauses
// actually embedded. A disabled embedder embeds nothing (returns 0, nil).
//
// Shared by the inline ingest path and the standalone cmd/embed binary so the
// batching, hashing, and skip logic live in exactly one place.
func Apply(ctx context.Context, e Embedder, items []Item, set SetFunc) (int, error) {
	if !e.Enabled() || len(items) == 0 {
		return 0, nil
	}
	modelID := e.ModelID()
	const batch = 32
	embedded := 0
	// Accumulate only the items that need embedding, then flush in fixed-size
	// batches so the model always sees full batches regardless of how many were
	// skipped.
	pending := make([]Item, 0, batch)
	hashes := make([]string, 0, batch)

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		texts := make([]string, len(pending))
		for i, it := range pending {
			texts[i] = EmbedText(it.Heading, it.Text)
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return err
		}
		for i, v := range vecs {
			if err := set(ctx, pending[i].ChunkID, v, hashes[i]); err != nil {
				return err
			}
			embedded++
		}
		pending = pending[:0]
		hashes = hashes[:0]
		return nil
	}

	for _, it := range items {
		want := ClauseHash(it.Heading, it.Text, modelID)
		if it.StoredHash == want {
			continue // already embedded with this exact text + model — skip.
		}
		pending = append(pending, it)
		hashes = append(hashes, want)
		if len(pending) == batch {
			if err := flush(); err != nil {
				return embedded, err
			}
		}
	}
	if err := flush(); err != nil {
		return embedded, err
	}
	return embedded, nil
}
