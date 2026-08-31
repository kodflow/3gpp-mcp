package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestServerInfoReportsEveryArm pins what server_info is FOR: a client asking what
// this server can do must not have to guess.
//
// It reported lexical, semantic, hnsw and the reranker, and said nothing about the
// sparse arm or the ETSI half — both of which search.Engine drops in silence when
// they are unavailable. A capability the tool cannot report is one the caller
// infers from the shape of the results, which is exactly the guessing this tool
// exists to end.
func TestServerInfoReportsEveryArm(t *testing.T) {
	t.Setenv("EMBEDDER", "local") // embed.Local implements SparseEmbedder
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "AMF registration", Text: "amf registration procedure"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}

	etsi, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = etsi.Close() })
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 280", Series: "ET", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 280", Release: "ETSI", Version: "2.18.1"})
	_ = etsi.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TS 103 280", Release: "ETSI", Version: "2.18.1", ClausePath: "1", Heading: "Scope", Text: "common LI parameters"},
	})

	newClient := func(t *testing.T, main, second store.Reader) *client.Client {
		t.Helper()
		srv, _ := New(main, "test", "", nil, second)
		c, cerr := client.NewInProcessClient(srv)
		if cerr != nil {
			t.Fatal(cerr)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Start(ctx); err != nil {
			t.Fatal(err)
		}
		var ir mcpgo.InitializeRequest
		ir.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
		ir.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1"}
		if _, err := c.Initialize(ctx, ir); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// (1) No sparse postings: the arm is off, and it SAYS SO with a reason a
	//     reader can act on rather than leaving the field absent.
	got := call(t, newClient(t, st, etsi), ctx, "server_info", map[string]any{})
	if _, present := got["sparse"]; !present {
		t.Fatal("server_info does not report the sparse arm at all")
	}
	if got["sparse"] != false {
		t.Errorf("sparse = %v with no postings, want false", got["sparse"])
	}
	if got["sparse_reason"] != "no_sparse_postings_in_db" {
		t.Errorf("sparse_reason = %q, want no_sparse_postings_in_db", got["sparse_reason"])
	}

	etsiInfo, ok := got["etsi"].(map[string]any)
	if !ok {
		t.Fatal("server_info does not report the ETSI half")
	}
	if etsiInfo["attached"] != true {
		t.Errorf("etsi.attached = %v with an ETSI store attached, want true", etsiInfo["attached"])
	}

	// (2) With postings the arm comes on. Same server surface, different corpus
	//     state — which is the distinction the field has to carry.
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
	got = call(t, newClient(t, st, etsi), ctx, "server_info", map[string]any{})
	if got["sparse"] != true {
		t.Errorf("sparse = %v with populated clause_sparse, want true (reason=%q)", got["sparse"], got["sparse_reason"])
	}

	// (3) No ETSI store: attached is false rather than the key vanishing, so a
	//     client can tell "no ETSI half" from "an older server that never said".
	got = call(t, newClient(t, st, nil), ctx, "server_info", map[string]any{})
	etsiInfo, ok = got["etsi"].(map[string]any)
	if !ok {
		t.Fatal("server_info omits the etsi block when no ETSI store is attached")
	}
	if etsiInfo["attached"] != false {
		t.Errorf("etsi.attached = %v with no ETSI store, want false", etsiInfo["attached"])
	}
}
