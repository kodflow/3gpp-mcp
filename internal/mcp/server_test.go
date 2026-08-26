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

// seeded in-memory store + in-process MCP client — exercises the tool layer
// with no corpus dependency (runs in CI).
func newClient(t *testing.T) (*client.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2", Heading: "Generation of xIRI over LI_X2", Text: "delivered to MDF2"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2.2", Heading: "Registration", Text: "registration event over X2"},
	})
	_ = st.InsertEvolutions([]model.Evolution{{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT", JustificationSpec: "23.501", Confidence: 0.9}})
	_ = st.UpsertAcronym(model.Acronym{Term: "AMF", Expansion: "Access and Mobility Management Function", Domain: "5GC"})

	srv, _ := New(st, "test", "", nil, nil)
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
	return c, ctx
}

func call(t *testing.T, c *client.Client, ctx context.Context, name string, args map[string]any) map[string]any {
	t.Helper()
	var r mcpgo.CallToolRequest
	r.Params.Name = name
	r.Params.Arguments = args
	res, err := c.CallTool(ctx, r)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned error", name)
	}
	var out map[string]any
	for _, ct := range res.Content {
		if tc, ok := mcpgo.AsTextContent(ct); ok {
			_ = json.Unmarshal([]byte(tc.Text), &out)
		}
	}
	return out
}

func TestMCPTools(t *testing.T) {
	c, ctx := newClient(t)

	tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// A count alone reports that the surface changed but not how, and every time
	// it fails somebody has to go and look. Name them instead: the 8 core tools
	// (CLAUDE.md §5) + search_api + server_info + trace_clause, plus li_events
	// contributed by the `li` subject.
	wantTools := map[string]bool{
		"search_spec": true, "get_spec": true, "get_changelog": true, "list_releases": true,
		"resolve_term": true, "trace_evolution": true, "find_cross_references": true,
		"list_specs": true, "search_api": true, "server_info": true, "trace_clause": true,
		"li_events": true,
	}
	gotTools := map[string]bool{}
	for _, tl := range tools.Tools {
		gotTools[tl.Name] = true
	}
	for name := range wantTools {
		if !gotTools[name] {
			t.Errorf("tool %q is not exposed", name)
		}
	}
	for name := range gotTools {
		if !wantTools[name] {
			t.Errorf("tool %q is exposed but undeclared here — the surface grew without this test noticing", name)
		}
	}
	if got := call(t, c, ctx, "server_info", map[string]any{}); got["lexical"] != true {
		t.Errorf("server_info lexical = %v, want true", got["lexical"])
	}

	if got := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "33.128", "clause": "6.2.2.2"}); got["count"].(float64) < 2 {
		t.Errorf("get_spec count = %v, want >=2", got["count"])
	}
	if got := call(t, c, ctx, "list_releases", map[string]any{"spec_id": "33.128"}); got["count"].(float64) != 1 {
		t.Errorf("list_releases count = %v, want 1", got["count"])
	}
	if got := call(t, c, ctx, "resolve_term", map[string]any{"term": "AMF"}); got["count"].(float64) != 1 {
		t.Errorf("resolve_term count = %v, want 1", got["count"])
	}
	if got := call(t, c, ctx, "trace_evolution", map[string]any{"entity": "MME"}); got["count"].(float64) != 1 {
		t.Errorf("trace_evolution count = %v, want 1", got["count"])
	}
	// search_spec must return cited hits (LIKE fallback on :memory:)
	got := call(t, c, ctx, "search_spec", map[string]any{"query": "registration X2", "spec_id": "33.128"})
	if got["count"].(float64) < 1 {
		t.Errorf("search_spec found nothing")
	}
	// every tool answer carries citations where it returns content
	if _, ok := got["citations"]; !ok {
		t.Error("search_spec missing citations block")
	}
}
