package mcp

import (
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

func TestURIRoundTrip(t *testing.T) {
	cases := []model.Citation{
		{SpecID: "33.128", Release: "Rel-19", Clause: "6.2.2.2"},
		{SpecID: "23.501", Release: "Rel-18"},
		{SpecID: "33.128", Release: "Rel-19", Clause: "6.2.2.2", Version: "19.6.0"},
		{SpecID: "33.128", Release: "Rel-19", Clause: "A.1", Version: "19.6.0"},
	}
	for _, c := range cases {
		uri := build3GPPURI(c)
		ref, err := parse3GPPURI(uri)
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		if ref.specID != c.SpecID || ref.release != c.Release ||
			ref.clause != c.Clause || ref.version != c.Version {
			t.Errorf("round-trip %q -> %+v != %+v", uri, ref, c)
		}
	}
}

func TestReadClauseResource(t *testing.T) {
	c, ctx := newClient(t)
	var rr mcpgo.ReadResourceRequest
	rr.Params.URI = "3gpp://33.128/Rel-19/6.2.2.2"
	res, err := c.ReadResource(ctx, rr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) == 0 {
		t.Fatal("no contents")
	}
	tc, ok := mcpgo.AsTextResourceContents(res.Contents[0])
	if !ok {
		t.Fatal("not text contents")
	}
	if tc.URI != rr.Params.URI || tc.MIMEType != "text/markdown" {
		t.Errorf("contents meta = %+v", tc)
	}
	if !strings.Contains(tc.Text, "MDF2") && !strings.Contains(tc.Text, "registration") {
		t.Errorf("clause body missing expected text: %q", tc.Text)
	}

	// Unknown clause -> not found (clean miss, not empty 200).
	var miss mcpgo.ReadResourceRequest
	miss.Params.URI = "3gpp://33.128/Rel-19/99.99.99"
	if _, err := c.ReadResource(ctx, miss); err == nil {
		t.Error("expected error for unknown clause URI")
	}
}

func TestSearchPagination(t *testing.T) {
	c, ctx := newClient(t)
	args := map[string]any{"query": "registration X2", "spec_id": "33.128", "top_k": 1}

	p1 := call(t, c, ctx, "search_spec", args)
	if p1["count"].(float64) != 1 {
		t.Fatalf("page1 count = %v, want 1", p1["count"])
	}
	cur, ok := p1["next_cursor"].(string)
	if !ok || cur == "" {
		t.Fatalf("page1 missing next_cursor (need >1 hit to paginate)")
	}

	// Page 2 with the cursor returns the next hit and (being last) no cursor.
	args2 := map[string]any{"query": "registration X2", "spec_id": "33.128", "top_k": 1, "cursor": cur}
	p2 := call(t, c, ctx, "search_spec", args2)
	if p2["count"].(float64) != 1 {
		t.Errorf("page2 count = %v, want 1", p2["count"])
	}
	if _, more := p2["next_cursor"]; more {
		t.Errorf("page2 should be last (no next_cursor)")
	}

	// A cursor replayed against a different query must be rejected.
	bad := map[string]any{"query": "totally different", "cursor": cur}
	var rr mcpgo.CallToolRequest
	rr.Params.Name = "search_spec"
	rr.Params.Arguments = bad
	res, err := c.CallTool(ctx, rr)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected error for cursor replayed against a different query")
	}
}

func TestGetSpecSnippetAndFull(t *testing.T) {
	c, ctx := newClient(t)

	// Default: snippet + resource URI, no full text.
	got := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "33.128", "clause": "6.2.2.2"})
	clauses, _ := got["clauses"].([]any)
	if len(clauses) == 0 {
		t.Fatal("no clauses")
	}
	c0, _ := clauses[0].(map[string]any)
	if _, hasText := c0["text"]; hasText {
		t.Error("default get_spec should omit full text")
	}
	if res, _ := c0["resource"].(string); !strings.HasPrefix(res, "3gpp://33.128/") {
		t.Errorf("clause resource URI = %q", c0["resource"])
	}

	// full=true inlines the text.
	gotFull := call(t, c, ctx, "get_spec", map[string]any{"spec_id": "33.128", "clause": "6.2.2.2", "full": true})
	fc, _ := gotFull["clauses"].([]any)
	f0, _ := fc[0].(map[string]any)
	if _, hasText := f0["text"]; !hasText {
		t.Error("full=true must inline text")
	}
}
