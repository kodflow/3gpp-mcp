// Package e2e is the end-to-end proof for the target question:
//
//	"How many LI events does each NE/NF report over X2 to the MDF2?"
//
// It runs the whole stack: ingest the converted HTML of TS 33.128 into DuckDB,
// stand up the MCP server in-process, drive it with a real MCP client, and
// compose the answer purely from tool output (get_spec) — proving the service
// can list the NE/NF, reach the LI spec, and yield the per-NF X2->MDF2 events
// with exact citations.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/ingest"
	mcpsrv "github.com/kodflow/3gpp-mcp/internal/mcp"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	lisubj "github.com/kodflow/3gpp-mcp/internal/subject/li"
)

func convertDir(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("CORPUS_DIR"); env != "" {
		return env
	}
	// Walk up from CWD to the module root (go.mod), independent of how the test
	// binary is invoked, then resolve data/sources/convert.
	wd, _ := os.Getwd()
	root := ""
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			root = d
			break
		}
		p := filepath.Dir(d)
		if p == d {
			break
		}
		d = p
	}
	if root == "" {
		t.Skip("module root (go.mod) not found")
	}
	dir := filepath.Join(root, "data", "sources", "convert")
	if _, err := os.Stat(filepath.Join(dir, "Rel-19", "33128-j60.html")); err != nil {
		t.Skipf("corpus not present (%v); run scripts/corpus.sh", err)
	}
	return dir
}

// clauseJSON mirrors the get_spec tool's clause shape.
type clauseJSON struct {
	ClausePath string         `json:"clause_path"`
	Heading    string         `json:"heading"`
	Text       string         `json:"text"`
	Citation   model.Citation `json:"citation"`
}

func TestLIEventsPerNF_E2E(t *testing.T) {
	ctx := context.Background()
	dir := convertDir(t)

	// --- ingest TS 33.128 into a throwaway DuckDB ---
	db := filepath.Join(t.TempDir(), "li.duckdb")
	st, err := ingest.Run(ctx, db, ingest.Options{
		ConvertDir: dir,
		SpecIDs:    []string{"33.128"},
		EnableFTS:  false, // offline-safe; LIKE fallback is enough for this POC
		Logf:       func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if st.Clauses == 0 {
		t.Fatalf("ingest produced no clauses")
	}
	t.Logf("ingested TS 33.128: %d versions, %d clauses, %d changes", st.Versions, st.Clauses, st.Changes)

	// --- stand up the MCP server + in-process client ---
	store0, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store0.Close() }()
	srv, _ := mcpsrv.New(store0, "e2e", "", nil)

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "e2e", Version: "1"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatal(err)
	}

	// the service exposes the 8 core tools (CLAUDE.md §5) + li_events
	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 10 {
		t.Errorf("expected 10 MCP tools, got %d", len(tools.Tools))
	}

	// --- find the LI spec's latest version via list_releases ---
	relText := callTool(t, ctx, c, "list_releases", map[string]any{"spec_id": "33.128"})
	var rel struct {
		Versions []model.SpecVersion `json:"versions"`
	}
	mustJSON(t, relText, &rel)
	if len(rel.Versions) == 0 {
		t.Fatal("list_releases returned no versions for 33.128")
	}
	latest := rel.Versions[0]
	t.Logf("TS 33.128 latest = %s %s", latest.Release, latest.Version)

	// --- fetch clause 6 verbatim via get_spec, then compose the answer ---
	specText := callTool(t, ctx, c, "get_spec", map[string]any{"spec_id": "33.128", "clause": "6"})
	var spec struct {
		Version string       `json:"version"`
		Clauses []clauseJSON `json:"clauses"`
	}
	mustJSON(t, specText, &spec)
	if len(spec.Clauses) == 0 {
		t.Fatal("get_spec returned no clause-6 content")
	}

	clauses := make([]model.Clause, len(spec.Clauses))
	for i, c := range spec.Clauses {
		clauses[i] = model.Clause{
			SpecID:     c.Citation.SpecID,
			Release:    c.Citation.Release,
			Version:    c.Citation.Version,
			ClausePath: c.ClausePath,
			Heading:    c.Heading,
			Text:       c.Text,
		}
	}

	// THE ANSWER: events each NE/NF reports over LI_X2 to the MDF2.
	nfs := lisubj.ExtractLIX2Events(clauses)
	if len(nfs) < 4 {
		t.Fatalf("expected >=4 NE/NF with an X2 section, got %d", len(nfs))
	}

	byNF := map[string]lisubj.X2NFEvents{}
	t.Logf("=== TS 33.128 %s — LI events over X2 -> MDF2, per NE/NF ===", spec.Version)
	total := 0
	for _, n := range nfs {
		byNF[n.NF] = n
		total += n.Count
		t.Logf("  %-18s %2d events   (X2 clause %s, cite %s %s)",
			n.NF, n.Count, n.X2Section, n.Citation.SpecID, n.Citation.Version)
		// every result must be citeable (CLAUDE.md §1)
		if n.Citation.SpecID != "33.128" || n.Citation.Clause == "" || n.Citation.URL == "" {
			t.Errorf("NF %s has an incomplete citation: %+v", n.NF, n.Citation)
		}
	}
	t.Logf("  TOTAL across NE/NF: %d events", total)

	// --- assertions on the headline answer (deterministic from the corpus) ---
	amf, ok := byNF["AMF"]
	if !ok || amf.Count < 8 {
		t.Errorf("AMF: want >=8 X2 events, got %d (ok=%v)", amf.Count, ok)
	}
	if !hasEvent(amf, "Registration") || !hasEvent(amf, "Handovers") {
		t.Errorf("AMF events missing Registration/Handovers: %v", eventHeadings(amf))
	}
	if mme, ok := byNF["MME"]; !ok || mme.Count < 5 {
		t.Errorf("MME: want >=5 X2 events, got %d (ok=%v)", mme.Count, ok)
	}
	if smf, ok := byNF["SMF"]; !ok || smf.Count < 5 {
		t.Errorf("SMF: want >=5 X2 events, got %d (ok=%v)", smf.Count, ok)
	}

	// --- the search tool also reaches the X2 sections, with citations ---
	hitText := callTool(t, ctx, c, "search_spec", map[string]any{
		"query": "Generation of xIRI over LI_X2", "spec_id": "33.128", "top_k": 5,
	})
	var sr struct {
		Count     int              `json:"count"`
		Citations []model.Citation `json:"citations"`
	}
	mustJSON(t, hitText, &sr)
	if sr.Count == 0 || len(sr.Citations) == 0 {
		t.Errorf("search_spec returned no cited hits for the X2 query")
	}
}

func callTool(t *testing.T, ctx context.Context, c *client.Client, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tool %s returned error: %s", name, textOf(res))
	}
	return textOf(res)
}

func textOf(res *mcp.CallToolResult) string {
	out := ""
	for _, ct := range res.Content {
		if tc, ok := mcp.AsTextContent(ct); ok {
			out += tc.Text
		}
	}
	return out
}

func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("decode tool JSON: %v\n%s", err, s)
	}
}

func hasEvent(n lisubj.X2NFEvents, substr string) bool {
	for _, e := range n.Events {
		if containsFold(e.Heading, substr) {
			return true
		}
	}
	return false
}

func eventHeadings(n lisubj.X2NFEvents) []string {
	var out []string
	for _, e := range n.Events {
		out = append(out, e.Heading)
	}
	return out
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	low := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if low(s[i+j]) != low(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
