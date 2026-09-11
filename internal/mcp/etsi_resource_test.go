package mcp

import (
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// THE RESOURCE URI get_spec HANDS OUT MUST BE READABLE — ON BOTH HALVES.
//
// get_spec answers an ETSI deliverable with a snippet and a `resource` URI, and
// its resource_hint says to call resources/read on that URI for the full text.
// readResource read the 3GPP store unconditionally, so for every ETSI clause that
// call answered "resource not found" — the ETSI half silently absent again, one
// hop further than the routing fixes of #311 reached.
func TestAnEtsiResourceURIIsReadFromTheEtsiHalf(t *testing.T) {
	st, e := halves(t)
	c, ctx := clientOver(t, st, e)

	out, text, isErr := callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "ETSI TS 103 221-1", "clause": "5"})
	if isErr {
		t.Fatalf("get_spec: %s", text)
	}
	clauses, _ := out["clauses"].([]any)
	if len(clauses) != 1 {
		t.Fatalf("want one clause; got %v", out["clauses"])
	}
	uri, _ := clauses[0].(map[string]any)["resource"].(string)

	var rr mcpgo.ReadResourceRequest
	rr.Params.URI = uri
	res, err := c.ReadResource(ctx, rr)
	if err != nil {
		t.Fatalf("resources/read %q: %v", uri, err)
	}
	var body strings.Builder
	for _, rc := range res.Contents {
		if tc, ok := rc.(mcpgo.TextResourceContents); ok {
			body.WriteString(tc.Text)
		}
	}
	if !strings.Contains(body.String(), "registration of a target over X1") {
		t.Errorf("resources/read %q did not return the ETSI clause text: %q", uri, body.String())
	}

	// The 3GPP URIs keep their spelling: nothing in a 3GPP id needs encoding.
	if got := build3GPPURI(model.Citation{SpecID: "33.128", Release: "Rel-19", Clause: "6.2.2.2", Version: "19.6.0"}); got != "3gpp://33.128/Rel-19/6.2.2.2@19.6.0" {
		t.Errorf("a 3GPP resource URI changed spelling: %q", got)
	}
	ref, err := parse3GPPURI(uri)
	if err != nil || ref.specID != "ETSI TS 103 221-1" || ref.clause != "5" || ref.version != "1.23.1" {
		t.Errorf("parse3GPPURI(%q) = %+v, %v", uri, ref, err)
	}

	// An ETSI half that could not be opened says so here too, rather than "not found".
	c, ctx = clientOver(t, st, nil, WithETSIUnavailable("etsi.duckdb could not be opened at startup: IO Error"))
	if _, err := c.ReadResource(ctx, rr); err == nil || !strings.Contains(err.Error(), "could not be opened") {
		t.Errorf("resources/read on an ETSI URI with the half down: want the reason, got %v", err)
	}
}
