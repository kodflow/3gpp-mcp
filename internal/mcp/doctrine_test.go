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

// clientWithTR seeds one TS and one TR spec so the TS-first default is testable.
func clientWithTR(t *testing.T) (*client.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// A normative TS and an informative TR in the same series/release.
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertSpec(model.Spec{SpecID: "33.926", Series: "33", DocType: "TR", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.926", Release: "Rel-19", Version: "19.0.0"})
	// 23.501 is the justification spec for the MME→AMF evolution; index it so the
	// citation can resolve release+version (issue #7).
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-19", Version: "19.5.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "Scope", Text: "see TS 23.501 and TR 33.926"},
	})
	_ = st.InsertEvolutions([]model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT", JustificationSpec: "23.501", JustificationClause: "4.2", Confidence: 0.9},
	})

	c, err := client.NewInProcessClient(New(st, "test", "", nil))
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
	return c, ctx
}

// TestListSpecsDefaultsToTS pins issue #6: list_specs returns only TS by default,
// TR when explicitly requested, and both with spec_type=any.
func TestListSpecsDefaultsToTS(t *testing.T) {
	c, ctx := clientWithTR(t)

	specIDs := func(out map[string]any) []string {
		b, _ := json.Marshal(out["specs"])
		var rows []struct {
			SpecID  string `json:"spec_id"`
			DocType string `json:"doc_type"`
		}
		_ = json.Unmarshal(b, &rows)
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.SpecID)
		}
		return ids
	}

	// Default (no spec_type) → TS only.
	def := specIDs(call(t, c, ctx, "list_specs", map[string]any{"series": "33"}))
	if len(def) != 1 || def[0] != "33.128" {
		t.Errorf("default list_specs = %v, want [33.128] (TS-first; TR 33.926 excluded)", def)
	}
	// Explicit TR → TR only.
	tr := specIDs(call(t, c, ctx, "list_specs", map[string]any{"series": "33", "spec_type": "TR"}))
	if len(tr) != 1 || tr[0] != "33.926" {
		t.Errorf("spec_type=TR list_specs = %v, want [33.926]", tr)
	}
	// any → both.
	all := specIDs(call(t, c, ctx, "list_specs", map[string]any{"series": "33", "spec_type": "any"}))
	if len(all) != 2 {
		t.Errorf("spec_type=any list_specs = %v, want both TS+TR", all)
	}
}

// TestTraceEvolutionCitationComplete pins issue #7: the evolution citation carries
// release + version (resolved from the justification spec) + clause + url.
func TestTraceEvolutionCitationComplete(t *testing.T) {
	c, ctx := clientWithTR(t)
	out := call(t, c, ctx, "trace_evolution", map[string]any{"entity": "MME"})

	b, _ := json.Marshal(out["citations"])
	var cites []model.Citation
	_ = json.Unmarshal(b, &cites)
	if len(cites) == 0 {
		t.Fatal("trace_evolution returned no citations")
	}
	cit := cites[0]
	if cit.SpecID != "23.501" {
		t.Errorf("citation spec_id = %q, want 23.501", cit.SpecID)
	}
	if cit.Release != "Rel-19" || cit.Version != "19.5.0" {
		t.Errorf("citation release/version = %q/%q, want Rel-19/19.5.0 (resolved from corpus)", cit.Release, cit.Version)
	}
	if cit.Clause != "4.2" {
		t.Errorf("citation clause = %q, want 4.2 (justification anchor)", cit.Clause)
	}
	if cit.URL == "" {
		t.Error("citation url empty")
	}
}

// TestCrossRefsCitationsResolved pins issue #7 for find_cross_references: each
// resolved reference gets a citation, and the indexed TS 23.501 ref resolves its
// release/version.
func TestCrossRefsCitationsResolved(t *testing.T) {
	c, ctx := clientWithTR(t)
	out := call(t, c, ctx, "find_cross_references", map[string]any{"spec_id": "33.128"})

	b, _ := json.Marshal(out["ref_citations"])
	var cites []model.Citation
	_ = json.Unmarshal(b, &cites)
	if len(cites) == 0 {
		t.Fatal("find_cross_references returned no ref_citations")
	}
	var found23 bool
	for _, cit := range cites {
		if cit.SpecID == "23.501" {
			found23 = true
			if cit.Release != "Rel-19" || cit.Version != "19.5.0" {
				t.Errorf("23.501 ref citation release/version = %q/%q, want Rel-19/19.5.0", cit.Release, cit.Version)
			}
			if cit.URL == "" {
				t.Error("23.501 ref citation url empty")
			}
		}
	}
	if !found23 {
		t.Errorf("expected a ref citation for the indexed 23.501 reference, got %+v", cites)
	}
}
