package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestHelpCountsTheCorpusItServes pins the one property that makes `help` worth
// having: the numbers are COUNTED from the served DB, not restated from what the
// build intended. A recap that hardcodes "2.7M clauses" keeps saying so after a
// migration silently truncates the corpus — the exact failure this project has
// already hit once, where every fingerprint-based gate stayed green over a
// corpus that had lost most of its clause occurrences.
//
// It also pins that the ETSI half is named when attached and reported absent when
// not: a caller must never have to infer which halves it is talking to.
func TestHelpCountsTheCorpusItServes(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.4.0"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.4.0", ClausePath: "5.4.4a", Heading: "AMF", Text: "amf selection"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.4.0", ClausePath: "5.4.4b", Heading: "SMF", Text: "smf selection"},
	}); err != nil {
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

	got := call(t, newClient(t, st, etsi), ctx, "help", map[string]any{})

	corpus, ok := got["corpus"].(map[string]any)
	if !ok {
		t.Fatal("help does not report a corpus inventory at all")
	}
	three, ok := corpus["3gpp"].(map[string]any)
	if !ok {
		t.Fatal("help omits the 3GPP half")
	}
	// Counted, not asserted: two clauses went in, two must come back. A hardcoded
	// production figure would fail here, which is the point.
	if n, _ := three["clauses"].(float64); n != 2 {
		t.Errorf("help clauses = %v, want 2 (counted from the served DB)", three["clauses"])
	}
	if n, _ := three["specs"].(float64); n != 1 {
		t.Errorf("help specs = %v, want 1", three["specs"])
	}

	half, ok := corpus["etsi"].(map[string]any)
	if !ok {
		t.Fatal("help omits the ETSI half")
	}
	if half["attached"] != true {
		t.Errorf("help etsi.attached = %v with an ETSI store passed, want true", half["attached"])
	}
	if n, _ := half["clauses"].(float64); n != 1 {
		t.Errorf("help etsi clauses = %v, want 1", half["clauses"])
	}

	// The map from question to tool, and the knobs — a recap without them sends
	// the caller back to the source to find out which tool answers what.
	if tools, _ := got["tools"].([]any); len(tools) == 0 {
		t.Error("help lists no tools")
	}
	if cfg, _ := got["configuration"].([]any); len(cfg) == 0 {
		t.Error("help lists no configuration knobs")
	}

	// With no ETSI store the half must be reported ABSENT, never silently omitted.
	got = call(t, newClient(t, st, nil), ctx, "help", map[string]any{})
	corpus, _ = got["corpus"].(map[string]any)
	half, ok = corpus["etsi"].(map[string]any)
	if !ok || half["attached"] != false {
		t.Errorf("help etsi = %v with no ETSI store, want attached:false", corpus["etsi"])
	}
}

// TestHelpCountersAllRun is the test whose absence let two broken counters ship.
//
// The inventory reports a COUNT per key, and a query that cannot run is reported
// in place of the number as "unavailable: <error>". The original test asserted
// `clauses` and `specs` and nothing else, so `vectors` (querying a clause_vectors
// table that does not exist) and `axis_values` (querying specs.release, a column
// that does not exist) shipped returning errors on the real corpus while every
// test here stayed green.
//
// So assert the PROPERTY rather than individual numbers: nothing in the inventory
// may be an "unavailable" string, on either half. A counter added later is covered
// without anyone remembering to extend this.
func TestHelpCountersAllRun(t *testing.T) {
	c, ctx := federatedClient(t)
	got := call(t, c, ctx, "help", map[string]any{})

	corpus, _ := got["corpus"].(map[string]any)
	if corpus == nil {
		t.Fatal("help reports no corpus inventory")
	}
	checked := 0
	for name, raw := range corpus {
		inv, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range inv {
			str, isStr := v.(string)
			if isStr && strings.HasPrefix(str, "unavailable:") {
				t.Errorf("%s half: counter %q could not run: %s", name, k, str)
			}
			if _, isNum := v.(float64); isNum {
				checked++
			}
		}
	}
	if checked < 4 {
		t.Errorf("only %d counter(s) came back as numbers — the inventory is not being counted", checked)
	}
}
