package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestSplitETSIFederation proves the two SPLIT indexes (3gpp.duckdb + etsi.duckdb)
// are federated by ONE server: get_spec routes an "ETSI …" id to the attached ETSI
// store, a 3GPP id to the 3GPP store, and list_specs unions both — without merging.
func TestSplitETSIFederation(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2", Heading: "xIRI over LI_X2", Text: "delivered to MDF2"},
	})

	etsi, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = etsi.Close() })
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 280", Series: "ET", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 280", Release: "ETSI", Version: "2.18.1"})
	_ = etsi.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TS 103 280", Release: "ETSI", Version: "2.18.1", ClausePath: "1", Heading: "Scope", Text: "Dictionary for common LI parameters"},
	})

	srv, _ := New(st, "test", "", nil, etsi) // <- ETSI store attached
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
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

	// get_spec on an ETSI id is served from the ETSI store (routing).
	if got := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "ETSI TS 103 280"}); got["count"].(float64) < 1 {
		t.Errorf("get_spec(ETSI TS 103 280) found nothing — ETSI routing broken")
	}
	// get_spec on a 3GPP id still resolves from the 3GPP store.
	if got := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "33.128", "clause": "6.2.2.2"}); got["count"].(float64) < 1 {
		t.Errorf("get_spec(33.128) regressed")
	}
	// list_releases on the ETSI id routes too.
	if got := call(t, c, ctx, "list_releases", map[string]any{"spec_id": "ETSI TS 103 280"}); got["count"].(float64) < 1 {
		t.Errorf("list_releases(ETSI TS 103 280) found nothing")
	}
	// search_spec federates: an ETSI-only term surfaces the ETSI clause via the union.
	if got := call(t, c, ctx, "search_spec", map[string]any{"query": "dictionary common LI parameters"}); got["count"].(float64) < 1 {
		t.Errorf("search_spec union found no ETSI hit")
	}
	// list_specs unions both corpora.
	got := call(t, c, ctx, "list_specs", map[string]any{})
	specs, _ := got["specs"].([]any)
	have3gpp, haveEtsi := false, false
	for _, s := range specs {
		if m, ok := s.(map[string]any); ok {
			switch m["spec_id"] {
			case "33.128":
				have3gpp = true
			case "ETSI TS 103 280":
				haveEtsi = true
			}
		}
	}
	if !have3gpp || !haveEtsi {
		t.Errorf("list_specs union: 3gpp=%v etsi=%v (want both)", have3gpp, haveEtsi)
	}
}

// TestETSISearchFindsATRByID is the regression test for a filter that answered a
// question the caller had already answered.
//
// The ETSI half's unset spec_type means "the normative types" (TS + EN), because
// defaulting to the literal "TS" hid 56% of that catalogue. But a spec_id PINS the
// document: asking for "ETSI TR 103 101" with no spec_type used to run the
// normative default, match neither type, and return NOTHING for a deliverable the
// caller had named.
func TestETSISearchFindsATRByID(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "x", Text: "transient voltages"},
	})

	etsi, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = etsi.Close() })
	// One of each ETSI type, so the default's effect is visible.
	for _, s := range []struct{ id, dt string }{
		{"ETSI TR 103 101", "TR"},
		{"ETSI EN 301 893", "EN"},
		{"ETSI TS 103 280", "TS"},
	} {
		_ = etsi.UpsertSpec(model.Spec{SpecID: s.id, Series: "ET", DocType: s.dt})
		_ = etsi.UpsertVersion(model.SpecVersion{SpecID: s.id, Release: "ETSI", Version: "1.1.1"})
	}
	_ = etsi.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TR 103 101", Release: "ETSI", Version: "1.1.1", ClausePath: "1", Heading: "Scope", Text: "transient voltages at interface A"},
		{ChunkID: 2, SpecID: "ETSI EN 301 893", Release: "ETSI", Version: "1.1.1", ClausePath: "1", Heading: "Scope", Text: "transient voltages in the 5 GHz band"},
		{ChunkID: 3, SpecID: "ETSI TS 103 280", Release: "ETSI", Version: "1.1.1", ClausePath: "1", Heading: "Scope", Text: "transient voltages dictionary"},
	})

	srv, _ := New(st, "test", "", nil, etsi)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
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

	// A named TR must answer, with no spec_type given.
	got := call(t, c, ctx, "search_spec", map[string]any{
		"query": "transient voltages", "spec_id": "ETSI TR 103 101",
	})
	if n, _ := got["count"].(float64); n < 1 {
		t.Errorf("search_spec(spec_id=ETSI TR 103 101) found nothing — the type default overrode an explicit id")
	}

	// And an unqualified search must reach the EN, which the old literal-"TS"
	// default filtered away.
	got = call(t, c, ctx, "search_spec", map[string]any{"query": "transient voltages", "top_k": 20})
	found := false
	if hits, ok := got["hits"].([]any); ok {
		for _, h := range hits {
			if m, ok := h.(map[string]any); ok && m["spec_id"] == "ETSI EN 301 893" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("an unqualified search did not reach the ETSI EN; hits=%v", got["hits"])
	}
}
