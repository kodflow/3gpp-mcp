package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// get_spec told a caller that ETSI TS 103 221-1 V1.23.1 — a published TS — was a
// DRAFT, with stable:false on every citation, because the 3GPP rule (major < 3)
// was applied to an ETSI edition number. Measured on the served corpus: 4 354 of
// 5 142 ETSI deliverables drew that warning. A 3GPP draft must still draw it.
//
// Falsified: with get_spec asking model.IsStableVersion(version) again, the ETSI
// call carries draft_warning and stable:false; with IsStableSpecVersion calling
// every version stable, the 3GPP draft loses its warning.
func TestGetSpecDoesNotCallAPublishedEtsiDeliverableADraft(t *testing.T) {
	ctx := context.Background()
	open := func() *store.Store {
		s, err := store.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	st := open()
	// A 3GPP spec that holds ONLY a draft: the case draft_warning exists for.
	_ = st.UpsertSpec(model.Spec{SpecID: "23.999", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.999", Release: "Rel-20", Version: "2.0.0"})
	_ = st.InsertClauses([]model.Clause{{ChunkID: 1, SpecID: "23.999", Release: "Rel-20", Version: "2.0.0",
		ClausePath: "1", Heading: "Scope", Text: "draft text"}})

	etsi := open()
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 221-1", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1"})
	_ = etsi.InsertClauses([]model.Clause{{ChunkID: 1, SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1",
		ClausePath: "1", Heading: "Scope", Text: "The present document specifies X1."}})

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

	out := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if w, ok := out["draft_warning"]; ok {
		t.Errorf("a published ETSI deliverable was called a draft: %v", w)
	}
	if out["stable"] != true {
		t.Errorf("stable = %v, want true for ETSI TS 103 221-1 V1.23.1", out["stable"])
	}
	var cites []model.Citation
	b, _ := json.Marshal(out["citations"])
	_ = json.Unmarshal(b, &cites)
	if len(cites) == 0 {
		t.Fatal("no citation to check")
	}
	for _, ct := range cites {
		if !ct.Stable {
			t.Errorf("ETSI citation stable:false: %+v", ct)
		}
	}

	out = call(t, c, ctx, "get_spec", map[string]any{"spec_id": "23.999"})
	if _, ok := out["draft_warning"]; !ok || out["stable"] != false {
		t.Errorf("a 3GPP draft must keep its warning: draft_warning=%v stable=%v", out["draft_warning"], out["stable"])
	}
}
