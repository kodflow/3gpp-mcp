package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// A HALF THAT FAILS IS NAMED, AND THE OTHER IS STILL SERVED (half_failures.go).
//
// The failure is injected at the store boundary, on the real store, and the
// answers are read through the real server: search_spec with an ETSI half that
// raises — under DUCKDB_MEMORY_LIMIT=4GB the ETSI half measurably does, and the
// answer used to be the 3GPP half alone with nothing saying so.
//
// Every "fails" case is paired with its healthy control, which proves the fixture
// has something on that half to lose: a test whose broken half had nothing to
// return would pass on the old code too.

// oom is what DuckDB raises when a half runs out of its memory limit.
var oom = errors.New("Out of Memory Error: failed to allocate data of size 256.0 MiB (3.7 GiB/4.0 GiB used)")

// brokenHalf is a real store whose reads fail. docType narrows the failure to
// the calls filtered on one document type ("" = every read fails), which is how
// one query of a half can fail while the next one succeeds.
type brokenHalf struct {
	store.Reader
	docType string
}

func (b *brokenHalf) fails(dt string) bool { return b.docType == "" || b.docType == dt }

func (b *brokenHalf) SearchClauses(ctx context.Context, q store.SearchQuery) ([]model.SearchHit, error) {
	if b.fails(q.Filter.DocType) {
		return nil, oom
	}
	return b.Reader.SearchClauses(ctx, q)
}

func (b *brokenHalf) ListSpecs(ctx context.Context, f store.SpecFilter) ([]model.Spec, error) {
	if b.fails(f.DocType) {
		return nil, oom
	}
	return b.Reader.ListSpecs(ctx, f)
}

func (b *brokenHalf) ResolveTerm(ctx context.Context, term string) ([]model.Acronym, error) {
	if b.docType == "" {
		return nil, oom
	}
	return b.Reader.ResolveTerm(ctx, term)
}

func (b *brokenHalf) LatestVersion(ctx context.Context, specID string) (string, string, bool, error) {
	if b.docType == "" {
		return "", "", false, oom
	}
	return b.Reader.LatestVersion(ctx, specID)
}

func (b *brokenHalf) GetEvolutions(ctx context.Context, entity string) ([]model.Evolution, error) {
	if b.docType == "" {
		return nil, oom
	}
	return b.Reader.GetEvolutions(ctx, entity)
}

func memStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// halves builds both halves with something on each for every tool under test:
// a "registration" clause, a spec, a definition of AMF.
func halves(t *testing.T) (threeGPP, etsi *store.Store) {
	t.Helper()
	st := memStore(t)
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "2",
			Heading: "References", Text: "registration events profile ETSI TS 103 221-1"},
	})
	_ = st.UpsertAcronym(model.Acronym{Term: "AMF", Expansion: "Access and Mobility Management Function", Domain: "5GC"})
	_ = st.InsertEvolutions([]model.Evolution{{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT",
		JustificationSpec: "23.501", JustificationClause: "6.2.1", Confidence: 0.9}})

	e := memStore(t)
	for _, sp := range []struct{ id, dt, v string }{
		{"ETSI TS 103 221-1", "TS", "1.23.1"},
		{"ETSI EN 300 392-2", "EN", "3.9.1"},
	} {
		_ = e.UpsertSpec(model.Spec{SpecID: sp.id, DocType: sp.dt})
		_ = e.UpsertVersion(model.SpecVersion{SpecID: sp.id, Release: "ETSI", Version: sp.v})
	}
	_ = e.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1", ClausePath: "5",
			Heading: "X1 registration", Text: "registration of a target over X1"},
		{ChunkID: 2, SpecID: "ETSI EN 300 392-2", Release: "ETSI", Version: "3.9.1", ClausePath: "14",
			Heading: "Registration", Text: "registration procedure of the TETRA air interface"},
	})
	_ = e.UpsertAcronym(model.Acronym{Term: "AMF", Expansion: "Application Management Function", Domain: "etsi"})
	return st, e
}

func clientOver(t *testing.T, st, etsi store.Reader, opts ...Option) (*client.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	srv, _ := New(st, "test", "", nil, etsi, opts...)
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

// callAny returns the decoded answer and whether it was a tool error.
func callAny(t *testing.T, c *client.Client, ctx context.Context, name string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	var r mcpgo.CallToolRequest
	r.Params.Name = name
	r.Params.Arguments = args
	res, err := c.CallTool(ctx, r)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := resultText(*res)
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out, text, res.IsError
}

// unreadOfAnswer reads unread_halves back as "half" or "half/part" keys, mapped
// to their error text.
func unreadOfAnswer(out map[string]any) map[string]string {
	got := map[string]string{}
	list, _ := out["unread_halves"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		key, _ := m["half"].(string)
		if p, _ := m["part"].(string); p != "" {
			key += "/" + p
		}
		got[key], _ = m["error"].(string)
	}
	return got
}

// specIDsOf collects the spec_id of every hit/spec/match row under key.
func specIDsOf(out map[string]any, key string) []string {
	var ids []string
	list, _ := out[key].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		if id, _ := m["spec_id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func hasETSI(ids []string) bool {
	for _, id := range ids {
		if isETSISpecID(id) {
			return true
		}
	}
	return false
}

func has3GPP(ids []string) bool {
	for _, id := range ids {
		if !isETSISpecID(id) {
			return true
		}
	}
	return false
}

func wantNamed(t *testing.T, tool string, out map[string]any, key, noteWords string) {
	t.Helper()
	u := unreadOfAnswer(out)
	if !strings.Contains(u[key], "Out of Memory") && !strings.Contains(u[key], "could not be opened") {
		t.Errorf("%s: unread_halves must name %s with its error; got %v", tool, key, u)
	}
	if n := noteOf(out); !strings.Contains(n, noteWords) {
		t.Errorf("%s: the note must say %q; note = %q", tool, noteWords, n)
	}
}

func wantNothingUnread(t *testing.T, tool string, out map[string]any) {
	t.Helper()
	if _, ok := out["unread_halves"]; ok {
		t.Errorf("%s: a healthy federation reported unread halves: %v", tool, out["unread_halves"])
	}
}

func TestSearchSpecNamesTheHalfThatFailed(t *testing.T) {
	st, e := halves(t)
	q := map[string]any{"query": "registration"}

	// Control: both halves answer, and each contributes hits.
	c, ctx := clientOver(t, st, e)
	out, _, _ := callAny(t, c, ctx, "search_spec", q)
	if ids := specIDsOf(out, "hits"); !hasETSI(ids) || !has3GPP(ids) {
		t.Fatalf("control: the fixture must put hits on both halves; got %v", ids)
	}
	wantNothingUnread(t, "search_spec", out)

	// The ETSI half fails: the 3GPP hits are served, and the ETSI half is named.
	c, ctx = clientOver(t, st, &brokenHalf{Reader: e})
	out, text, isErr := callAny(t, c, ctx, "search_spec", q)
	if isErr {
		t.Fatalf("an ETSI failure failed the whole call: %s", text)
	}
	if ids := specIDsOf(out, "hits"); !has3GPP(ids) || hasETSI(ids) {
		t.Errorf("want the 3GPP hits alone; got %v", ids)
	}
	wantNamed(t, "search_spec", out, "etsi", "the ETSI half could not be read")

	// The 3GPP half fails: the ETSI hits are served, not lost to it.
	c, ctx = clientOver(t, &brokenHalf{Reader: st}, e)
	out, text, isErr = callAny(t, c, ctx, "search_spec", q)
	if isErr {
		t.Fatalf("a 3GPP failure cost the ETSI hits: %s", text)
	}
	if ids := specIDsOf(out, "hits"); !hasETSI(ids) || has3GPP(ids) {
		t.Errorf("want the ETSI hits alone; got %v", ids)
	}
	wantNamed(t, "search_spec", out, "3gpp", "the 3GPP half could not be read")

	// One document type of the ETSI half fails: the others are served, and the
	// answer says which type it could not search.
	c, ctx = clientOver(t, st, &brokenHalf{Reader: e, docType: "TS"})
	out, _, _ = callAny(t, c, ctx, "search_spec", q)
	ids := specIDsOf(out, "hits")
	if !contains(strings.Join(ids, ","), "ETSI EN 300 392-2") || contains(strings.Join(ids, ","), "ETSI TS 103 221-1") {
		t.Errorf("want the EN hit and not the TS one; got %v", ids)
	}
	wantNamed(t, "search_spec", out, "etsi/TS documents", "the TS documents of the ETSI half could not be read")

	// Both fail: nothing to serve, and the error names both.
	c, ctx = clientOver(t, &brokenHalf{Reader: st}, &brokenHalf{Reader: e})
	_, text, isErr = callAny(t, c, ctx, "search_spec", q)
	if !isErr || !strings.Contains(text, "3GPP") || !strings.Contains(text, "ETSI") {
		t.Errorf("both halves failing must be an error naming both; got isError=%v %q", isErr, text)
	}
}

func TestListSpecsNamesTheHalfThatFailed(t *testing.T) {
	st, e := halves(t)

	c, ctx := clientOver(t, st, e)
	out, _, _ := callAny(t, c, ctx, "list_specs", map[string]any{})
	if ids := specIDsOf(out, "specs"); !hasETSI(ids) || !has3GPP(ids) {
		t.Fatalf("control: want specs from both halves; got %v", ids)
	}
	wantNothingUnread(t, "list_specs", out)

	c, ctx = clientOver(t, st, &brokenHalf{Reader: e})
	out, _, _ = callAny(t, c, ctx, "list_specs", map[string]any{})
	if ids := specIDsOf(out, "specs"); !has3GPP(ids) || hasETSI(ids) {
		t.Errorf("want the 3GPP catalogue alone; got %v", ids)
	}
	wantNamed(t, "list_specs", out, "etsi", "the ETSI half could not be read")

	c, ctx = clientOver(t, st, &brokenHalf{Reader: e, docType: "EN"})
	out, _, _ = callAny(t, c, ctx, "list_specs", map[string]any{})
	if ids := strings.Join(specIDsOf(out, "specs"), ","); !contains(ids, "ETSI TS 103 221-1") || contains(ids, "ETSI EN") {
		t.Errorf("want the ETSI TS and not the EN; got %v", ids)
	}
	wantNamed(t, "list_specs", out, "etsi/EN documents", "the EN documents of the ETSI half")

	c, ctx = clientOver(t, &brokenHalf{Reader: st}, e)
	out, text, isErr := callAny(t, c, ctx, "list_specs", map[string]any{})
	if isErr {
		t.Fatalf("a 3GPP failure cost the ETSI catalogue: %s", text)
	}
	if ids := specIDsOf(out, "specs"); !hasETSI(ids) || has3GPP(ids) {
		t.Errorf("want the ETSI catalogue alone; got %v", ids)
	}
	wantNamed(t, "list_specs", out, "3gpp", "the 3GPP half could not be read")
}

func TestResolveTermNamesTheHalfThatFailed(t *testing.T) {
	st, e := halves(t)
	expansions := func(out map[string]any) string {
		var xs []string
		list, _ := out["matches"].([]any)
		for _, x := range list {
			m, _ := x.(map[string]any)
			s, _ := m["expansion"].(string)
			xs = append(xs, s)
		}
		return strings.Join(xs, "|")
	}

	c, ctx := clientOver(t, st, e)
	out, _, _ := callAny(t, c, ctx, "resolve_term", map[string]any{"term": "AMF"})
	if x := expansions(out); !contains(x, "Access and Mobility") || !contains(x, "Application Management") {
		t.Fatalf("control: want a definition from each half; got %q", x)
	}
	wantNothingUnread(t, "resolve_term", out)

	// An ETSI failure used to fail the whole call, losing the canonical 3GPP definition.
	c, ctx = clientOver(t, st, &brokenHalf{Reader: e})
	out, text, isErr := callAny(t, c, ctx, "resolve_term", map[string]any{"term": "AMF"})
	if isErr {
		t.Fatalf("an ETSI failure cost the 3GPP definition: %s", text)
	}
	if x := expansions(out); x != "Access and Mobility Management Function" {
		t.Errorf("want the 3GPP definition alone; got %q", x)
	}
	wantNamed(t, "resolve_term", out, "etsi", "the ETSI half could not be read")

	c, ctx = clientOver(t, &brokenHalf{Reader: st}, e)
	out, text, isErr = callAny(t, c, ctx, "resolve_term", map[string]any{"term": "AMF"})
	if isErr {
		t.Fatalf("a 3GPP failure cost the ETSI definition: %s", text)
	}
	if x := expansions(out); x != "Application Management Function" {
		t.Errorf("want the ETSI definition alone; got %q", x)
	}
	wantNamed(t, "resolve_term", out, "3gpp", "the 3GPP half could not be read")
}

func TestCrossReferencesSayWhyAVersionIsMissing(t *testing.T) {
	st, e := halves(t)
	cite := func(out map[string]any) map[string]any {
		list, _ := out["etsi_ref_citations"].([]any)
		if len(list) != 1 {
			t.Fatalf("want one ETSI reference; got %v", out["etsi_ref_citations"])
		}
		m, _ := list[0].(map[string]any)
		return m
	}

	c, ctx := clientOver(t, st, e)
	out, _, _ := callAny(t, c, ctx, "find_cross_references", map[string]any{"spec_id": "33.128"})
	if v := cite(out)["version"]; v != "1.23.1" {
		t.Fatalf("control: the ETSI mention must resolve to the held version; got %v", v)
	}
	wantNothingUnread(t, "find_cross_references", out)

	// Unresolved because the half could not be asked — which, without a word,
	// looks exactly like a deliverable the corpus does not hold.
	c, ctx = clientOver(t, st, &brokenHalf{Reader: e})
	out, _, _ = callAny(t, c, ctx, "find_cross_references", map[string]any{"spec_id": "33.128"})
	if v, _ := cite(out)["version"].(string); v != "" {
		t.Fatalf("the broken half resolved a version: %v", v)
	}
	wantNamed(t, "find_cross_references", out, "etsi", "cite its document folder")
}

func TestTraceEvolutionReportsItsUnreadHalvesToo(t *testing.T) {
	st, e := halves(t)
	c, ctx := clientOver(t, st, &brokenHalf{Reader: e})
	out, _, _ := callAny(t, c, ctx, "trace_evolution", map[string]any{"entity": "MME"})
	if n, _ := out["count"].(float64); n != 1 {
		t.Fatalf("the 3GPP edge was lost: count = %v", out["count"])
	}
	wantNamed(t, "trace_evolution", out, "etsi", "the ETSI half could not be read")
}

// AN ETSI HALF ASKED FOR AND NEVER OPENED. cmd/server logged it and served the
// 3GPP half alone; nothing in any answer told a client it was not the whole corpus.
func TestAnEtsiHalfThatCouldNotBeOpenedIsNamedEverywhere(t *testing.T) {
	st, _ := halves(t)
	why := "data/etsi.duckdb (--etsi-db) could not be opened at startup: IO Error: Cannot open file"
	c, ctx := clientOver(t, st, nil, WithETSIUnavailable(why))

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"search_spec", map[string]any{"query": "registration"}},
		{"search_spec", map[string]any{"query": "registration", "spec_id": "ETSI TS 103 221-1"}},
		{"list_specs", map[string]any{}},
		{"resolve_term", map[string]any{"term": "AMF"}},
		{"trace_evolution", map[string]any{"entity": "MME"}},
		{"find_cross_references", map[string]any{"spec_id": "33.128"}},
	} {
		out, text, isErr := callAny(t, c, ctx, tc.tool, tc.args)
		if isErr {
			t.Errorf("%s %v: the 3GPP half must still answer: %s", tc.tool, tc.args, text)
			continue
		}
		wantNamed(t, tc.tool, out, "etsi", "ETSI half")
	}

	// A question about one ETSI document is refused with the reason, rather than
	// routed to the 3GPP store and answered "no such spec".
	for _, tool := range []string{"get_spec", "list_releases", "get_changelog", "trace_clause", "find_cross_references"} {
		_, text, isErr := callAny(t, c, ctx, tool, map[string]any{"spec_id": "ETSI TS 103 221-1", "clause": "5"})
		if !isErr || !strings.Contains(text, "could not be opened") {
			t.Errorf("%s on an ETSI id must say the ETSI half is unavailable; isError=%v %q", tool, isErr, text)
		}
	}

	for _, tool := range []string{"server_info", "help"} {
		out, _, _ := callAny(t, c, ctx, tool, map[string]any{})
		etsi, _ := out["etsi"].(map[string]any)
		if tool == "help" {
			corpus, _ := out["corpus"].(map[string]any)
			etsi, _ = corpus["etsi"].(map[string]any)
		}
		if u, _ := etsi["unavailable"].(string); !strings.Contains(u, "could not be opened") {
			t.Errorf("%s must say why the ETSI half is not attached; etsi = %v", tool, etsi)
		}
	}

	// And a server never given an ETSI half says nothing of the kind.
	c, ctx = clientOver(t, st, nil)
	out, _, _ := callAny(t, c, ctx, "search_spec", map[string]any{"query": "registration"})
	wantNothingUnread(t, "search_spec", out)
}
