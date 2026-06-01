package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"

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

// applyBatch is the fixed batch size handed to the embedder (BGE-M3 reproducibility
// contract; overridable on the GPU path via BGE_BATCH inside the onnx backend).
const applyBatch = 32

// defaultBucketWindow bounds how many needing-embed items are buffered before a
// length-bucket + flush. A window large enough to span many batches lets us group
// similar-length clauses together (tight padding) while staying a contiguous band
// of the recent-first work-list, so coarse recency priority survives.
const defaultBucketWindow = 4096

// bucketWindow reads EMBED_BUCKET_WINDOW (default defaultBucketWindow). 0/1 (or any
// value < applyBatch) disables length-bucketing and restores the legacy in-order,
// streamed batching. Build-tag-free (apply.go is in the default build, so it cannot
// borrow envInt from the onnx-only embed_onnx.go).
func bucketWindow() int {
	if v := os.Getenv("EMBED_BUCKET_WINDOW"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return defaultBucketWindow
}

// embedLen is the cheap length proxy used to bucket clauses: byte length of the
// embedded text. It correlates closely enough with token count for padding to be
// the dominant cost, and avoids allocating a token/word split per item.
func embedLen(it Item) int { return len(it.Heading) + len(it.Text) }

// Apply embeds the items whose current hash differs from their StoredHash (or that
// were never embedded) and persists each via set. Items already up to date are
// skipped — this is the micro-granular re-embed. Returns the number of clauses
// actually embedded. A disabled embedder embeds nothing (returns 0, nil).
//
// Throughput: needing-embed items are buffered into a bounded window and
// LENGTH-BUCKETED before batching, so each fixed-size batch has near-uniform
// sequence length and the padded tensor is tight. On 3GPP — where a 12-token
// heading and a 512-token table can otherwise share a batch padded to 512 — this
// is the single biggest GPU win and is identity-safe: padding is attention-masked
// (a vector never depends on its batch-mates) and set() is keyed by chunk_id, so
// the reordering changes nothing stored. Disable with EMBED_BUCKET_WINDOW<32.
//
// Shared by the inline ingest path and the standalone cmd/embed binary so the
// batching, hashing, and skip logic live in exactly one place.
func Apply(ctx context.Context, e Embedder, items []Item, set SetFunc) (int, error) {
	if !e.Enabled() || len(items) == 0 {
		return 0, nil
	}
	modelID := e.ModelID()
	embedded := 0

	// job pairs a needing-embed item with its already-computed target hash.
	type job struct {
		item Item
		hash string
	}

	windowSize := bucketWindow()
	sortWithin := windowSize > applyBatch
	if windowSize < applyBatch {
		windowSize = applyBatch // never emit short batches just because the window is tiny
	}

	// processWindow length-sorts (when enabled) then hands the whole window to the
	// embedder in ONE call, so the backend can internally batch (by batchSize) and
	// pipeline tokenisation against GPU inference across those batches. set() is
	// then applied per clause (keyed by chunk_id), so output order is irrelevant.
	processWindow := func(jobs []job) error {
		if sortWithin {
			sort.SliceStable(jobs, func(i, j int) bool {
				return embedLen(jobs[i].item) < embedLen(jobs[j].item)
			})
		}
		texts := make([]string, len(jobs))
		for i, j := range jobs {
			texts[i] = EmbedText(j.item.Heading, j.item.Text)
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return err
		}
		if len(vecs) != len(jobs) {
			return fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(jobs))
		}
		for i, v := range vecs {
			if err := set(ctx, jobs[i].item.ChunkID, v, jobs[i].hash); err != nil {
				return err
			}
			embedded++
		}
		return nil
	}

	window := make([]job, 0, windowSize)
	for _, it := range items {
		want := ClauseHash(it.Heading, it.Text, modelID)
		if it.StoredHash == want {
			continue // already embedded with this exact text + model — skip.
		}
		window = append(window, job{item: it, hash: want})
		if len(window) >= windowSize {
			if err := processWindow(window); err != nil {
				return embedded, err
			}
			window = window[:0]
		}
	}
	if len(window) > 0 {
		if err := processWindow(window); err != nil {
			return embedded, err
		}
	}
	return embedded, nil
}
