package search

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// A CANCELLED CALLER MUST NOT REACH DuckDB, and the reason is not politeness.
//
// Cancelling a running DuckDB query makes it raise duckdb::InterruptException;
// that C++ exception crosses cgo and ABORTS THE PROCESS. It never becomes a Go
// error, so there is no degraded answer, no message to the client, and no server
// left. Measured 2026-09-06 against the published image, on the published corpus.
//
// The first fix moved the store calls off the BUDGET's context and onto the
// caller's. That was not enough, and the review of PR #288 said so: an HTTP
// client that disconnects, or a deadline in the transport, cancels the caller's
// context just as effectively. So the store calls now take
// context.WithoutCancel — values kept, cancellation dropped.
//
// This test exercises the whole path with a context that is ALREADY cancelled.
// Before the fix the lexical arm (which runs on the caller's ctx by design)
// would fail and, on Linux, the vector arm would abort the process. After it,
// the search still answers.
func TestACancelledCallerStillGetsAnAnswer(t *testing.T) {
	t.Setenv("EMBEDDER", "off")

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0",
			ClausePath: "5.2", Heading: "Registration", Text: "the ue registration procedure"},
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is already gone
	if ctx.Err() == nil {
		t.Fatal("the fixture context is not cancelled — the test would prove nothing")
	}

	eng := New(st)
	hits, err := eng.Search(ctx, Request{Text: "registration", Mode: "hybrid", TopK: 5})
	if err != nil {
		t.Fatalf("a cancelled caller produced an error instead of a degraded answer: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("a cancelled caller produced no hits — the store calls saw the cancellation")
	}
}

// The values on the caller's context must survive the shield, or tracing and
// anything else carried on it silently disappears from every store call.
// context.WithoutCancel is the right tool precisely because it keeps them;
// context.Background() would have been the wrong one.
func TestTheShieldKeepsContextValues(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "carried"))
	cancel()

	shielded := context.WithoutCancel(ctx)
	if shielded.Err() != nil {
		t.Fatal("WithoutCancel did not drop the cancellation")
	}
	if got, _ := shielded.Value(key{}).(string); got != "carried" {
		t.Fatalf("WithoutCancel dropped the values too: %q", got)
	}
}
