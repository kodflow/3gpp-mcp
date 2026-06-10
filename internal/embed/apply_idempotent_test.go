package embed

import (
	"context"
	"strings"
	"testing"
)

// TestApplyIsIdempotent locks the re-embed gate that makes a no-change rebuild
// cheap (the "0 diff on re-run" contract): Apply embeds a clause only when its
// StoredHash differs from ClauseHash(text, model). So a second pass over an
// already-embedded, unchanged corpus must embed ZERO, and a single text change
// must re-embed exactly that one clause — micro-granular, never the whole DB.
func TestApplyIsIdempotent(t *testing.T) {
	modelID := Local{}.ModelID()

	// mk builds 50 clauses; `stored` decides each item's StoredHash.
	mk := func(stored func(Item) string) []Item {
		items := make([]Item, 0, 50)
		for i := 0; i < 50; i++ {
			it := Item{
				ChunkID: uint64(i + 1),
				Heading: "clause",
				Text:    "body text " + strings.Repeat("x ", i+1),
			}
			it.StoredHash = stored(it)
			items = append(items, it)
		}
		return items
	}
	embed := func(items []Item) int {
		n, err := Apply(context.Background(), Local{}, items,
			func(context.Context, uint64, []float32, string) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 1) Fresh corpus (StoredHash == "" → never embedded): all 50 embed.
	if n := embed(mk(func(Item) string { return "" })); n != 50 {
		t.Fatalf("fresh embed: want 50, got %d", n)
	}

	// 2) Re-run over the SAME, up-to-date corpus (StoredHash already matches the
	//    target hash): ZERO re-embedded. This is the cache / 0-diff guarantee.
	upToDate := mk(func(it Item) string { return ClauseHash(it.Heading, it.Text, modelID) })
	if n := embed(upToDate); n != 0 {
		t.Fatalf("idempotent re-run: want 0 re-embed, got %d", n)
	}

	// 3) Exactly one clause's text drifts → only that clause re-embeds.
	drift := mk(func(it Item) string { return ClauseHash(it.Heading, it.Text, modelID) })
	drift[7].Text = "a DIFFERENT body that no longer matches the stored hash"
	if n := embed(drift); n != 1 {
		t.Fatalf("single-clause drift: want 1 re-embed, got %d", n)
	}

	// 4) A model-identity change re-embeds everything (vectors from another model
	//    must never be served against this one). Stored hashes keyed to a different
	//    modelID never match the current target → all 50 re-embed.
	otherModel := mk(func(it Item) string { return ClauseHash(it.Heading, it.Text, "some-other-model-identity") })
	if n := embed(otherModel); n != 50 {
		t.Fatalf("model-identity change: want 50 re-embed, got %d", n)
	}
}
