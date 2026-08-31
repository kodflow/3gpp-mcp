package search

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// passCounter is embed.Local with a counter on every route into the model, so
// a test can assert how many forward passes ONE query costs.
type passCounter struct {
	embed.Local
	dense, sparse, both atomic.Int32
}

func (c *passCounter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.dense.Add(1)
	return c.Local.Embed(ctx, texts)
}

func (c *passCounter) EmbedSparse(ctx context.Context, texts []string) ([]model.SparseVec, error) {
	c.sparse.Add(1)
	return c.Local.EmbedSparse(ctx, texts)
}

func (c *passCounter) EmbedBoth(ctx context.Context, text string) ([]float32, model.SparseVec, bool) {
	c.both.Add(1)
	d, err := c.Local.Embed(ctx, []string{text})
	if err != nil || len(d) != 1 {
		return nil, nil, false
	}
	s, err := c.Local.EmbedSparse(ctx, []string{text})
	if err != nil || len(s) != 1 {
		return nil, nil, false
	}
	return d[0], s[0], true
}

// TestHybridQueryCostsOneForwardPass is the whole point of the combined path.
//
// BGE-M3 emits the dense embedding and the learned-lexical weights from the same
// encoder, and ONNX Runtime computes that encoder whether one output is read or
// two — so a hybrid query that called Embed and then EmbedSparse ran the
// transformer twice over the same string. Measured on the real model: ~166 ms for
// the pair, against a BM25 arm costing ~10 ms.
//
// Correctness is asserted elsewhere (internal/embed proves the combined outputs
// are bit-identical to the separate ones). What this pins is that the ENGINE
// actually takes the combined route when both arms are live: the wiring is easy to
// undo by accident, and undoing it costs latency without failing anything.
func TestHybridQueryCostsOneForwardPass(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "AMF registration", Text: "amf registration procedure"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2", Heading: "SMF session", Text: "pdu session establishment"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
	sp := embed.Local{}
	for _, c := range clauses {
		vecs, verr := sp.EmbedSparse(ctx, []string{c.Heading + "\n" + c.Text})
		if verr != nil {
			t.Fatal(verr)
		}
		if err := st.SetSparse(ctx, c.ChunkID, vecs[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.LoadSparse(ctx); err != nil {
		t.Fatal(err)
	}

	ce := &passCounter{}
	eng := New(st)
	// Replace the engine's embedder with the counting one, front to back: the dense
	// path goes through the LRU decorator (which is where EmbedBoth is intercepted)
	// and the sparse path is the base, exactly as New() wires it.
	eng.emb = withQueryCache(ce, 512)
	eng.sp = ce

	if _, err := eng.Search(ctx, Request{Text: "amf registration", Mode: "hybrid", TopK: 5}); err != nil {
		t.Fatal(err)
	}
	if got := ce.both.Load(); got != 1 {
		t.Errorf("EmbedBoth called %d time(s), want 1 — the engine is not taking the combined route", got)
	}
	if got := ce.dense.Load(); got != 0 {
		t.Errorf("Embed called %d time(s) on a cold cache, want 0 (the combined path supplies the dense vector)", got)
	}
	if got := ce.sparse.Load(); got != 0 {
		t.Errorf("EmbedSparse called %d time(s), want 0 (the combined path supplies the sparse vector)", got)
	}

	// SECOND, IDENTICAL QUERY. The dense vector is now cached, so the combined path
	// must NOT run again — recomputing a vector the LRU already holds would turn a
	// speed-up into a regression on exactly the repeated queries the cache exists
	// for. Only the sparse head runs.
	ce.both.Store(0)
	if _, err := eng.Search(ctx, Request{Text: "amf registration", Mode: "hybrid", TopK: 5}); err != nil {
		t.Fatal(err)
	}
	if got := ce.both.Load(); got != 0 {
		t.Errorf("EmbedBoth called %d time(s) on a cache HIT, want 0", got)
	}
	if got := ce.sparse.Load(); got != 1 {
		t.Errorf("EmbedSparse called %d time(s) on a cache hit, want 1", got)
	}
}
