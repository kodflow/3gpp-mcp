package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// When the ASN.1 registry was ingested, li_events must answer from it
// (source="asn1"), scope to the baseline release, and surface events added in
// later releases under added_in_later_releases.
func TestLIEventsAuthoritative(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	for _, v := range []string{"18.15.0", "19.6.0"} {
		rel := "Rel-18"
		if v[:2] == "19" {
			rel = "Rel-19"
		}
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: rel, Version: v})
		// clause-availability needs at least one clause-6 row per release
		_ = st.InsertClauses([]model.Clause{{
			ChunkID: chunkFor(rel), SpecID: "33.128", Release: rel, Version: v,
			ClausePath: "6.2.2.2", Heading: "AMF", Text: "x",
		}})
	}

	// Rel-18 baseline: two authoritative AMF X2 events.
	seedLIEvent(t, st, "Rel-18", "r18 version14", "X2", "registration", "AMFRegistration", 1, "AMF", "6.2.2.2", 32)
	seedLIEvent(t, st, "Rel-18", "r18 version14", "X2", "deregistration", "AMFDeregistration", 2, "AMF", "6.2.2.2", 13)
	// Rel-19 keeps the two and adds one new AMF X2 event.
	seedLIEvent(t, st, "Rel-19", "r19 version6", "X2", "registration", "AMFRegistration", 1, "AMF", "6.2.2.2", 32)
	seedLIEvent(t, st, "Rel-19", "r19 version6", "X2", "deregistration", "AMFDeregistration", 2, "AMF", "6.2.2.2", 13)
	seedLIEvent(t, st, "Rel-19", "r19 version6", "X2", "locationUpdate", "AMFLocationUpdate", 3, "AMF", "6.2.2.2", 10)

	c := mustClient(t, st)

	// Baseline Rel-18: authoritative, 2 events, one later addition.
	got := call(t, c, ctx, "li_events", map[string]any{"nf": "AMF", "release": "Rel-18"})
	if got["source"] != "asn1" {
		t.Errorf("source = %v, want asn1", got["source"])
	}
	if got["count"].(float64) != 2 {
		t.Errorf("Rel-18 count = %v, want 2", got["count"])
	}
	added, _ := got["added_in_later_releases"].(map[string]any)
	if rel19, ok := added["Rel-19"].([]any); !ok || len(rel19) != 1 {
		t.Errorf("added_in_later_releases[Rel-19] = %v, want 1 entry", added["Rel-19"])
	}
	if _, ok := got["citations"]; !ok {
		t.Error("authoritative li_events missing citations")
	}

	// Baseline Rel-19: all three present, nothing added later.
	got = call(t, c, ctx, "li_events", map[string]any{"nf": "AMF", "release": "Rel-19"})
	if got["count"].(float64) != 3 {
		t.Errorf("Rel-19 count = %v, want 3", got["count"])
	}
}

// resolve_term attaches an ASN.1 definition + citation when the term is a type.
func TestResolveTermASN1(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-18", Version: "18.15.0"})
	seedASN1Type(t, st, "Rel-18", "AMFRegistration", "SEQUENCE", `[{"name":"sUPI","type":"SUPI","tag":1}]`)

	c := mustClient(t, st)
	got := call(t, c, ctx, "resolve_term", map[string]any{"term": "AMFRegistration"})
	a, ok := got["asn1"].(map[string]any)
	if !ok {
		t.Fatalf("resolve_term missing asn1 block: %v", got)
	}
	if a["kind"] != "SEQUENCE" {
		t.Errorf("asn1.kind = %v, want SEQUENCE", a["kind"])
	}
	if _, ok := got["asn1_citation"]; !ok {
		t.Error("resolve_term missing asn1_citation")
	}
}

func chunkFor(rel string) uint64 {
	if rel == "Rel-19" {
		return 2
	}
	return 1
}

// seedLIEvent inserts one authoritative li_events row directly. The Go LI write
// helpers (li.InsertEvents/…) were removed in Phase 11b — Rust owns ingest — so
// the read-path tests seed their fixtures with plain SQL over the test DB.
func seedLIEvent(t *testing.T, st *store.Store, release, moduleVersion, iface, name, typ string, tag int, nf, clause string, fieldCount int) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT OR REPLACE INTO li_events
		 (spec_id, release, module_version, interface, event_name, asn1_type, asn1_tag, originating_nf, domain, spec_clause, field_count)
		 VALUES ('33.128', ?, ?, ?, ?, ?, ?, ?, '5GC', ?, ?)`,
		release, moduleVersion, iface, name, typ, tag, nf, clause, fieldCount); err != nil {
		t.Fatal(err)
	}
}

// seedASN1Type inserts one asn1_types row directly (see seedLIEvent rationale).
func seedASN1Type(t *testing.T, st *store.Store, release, name, kind, membersJSON string) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT OR REPLACE INTO asn1_types (spec_id, release, type_name, kind, members) VALUES ('33.128', ?, ?, ?, ?)`,
		release, name, kind, membersJSON); err != nil {
		t.Fatal(err)
	}
}

// search_api must answer from the api_* tables with a SHA-pinned forge citation.
func TestSearchAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sha := "867f0e81ced93aa72376ef97c5e8f9df63fbbf3b"
	op := model.APIOperation{
		OpID: 1, SpecID: "29.518", Release: "Rel-18", Version: "18.13.0",
		Service: "Namf_Communication", ServiceFamily: "Namf", APIRoot: "namf-comm/v1",
		Path: "/ue-contexts/{ueContextId}", Method: "PUT", OperationID: "CreateUEContext",
		Summary:  "Namf_Communication CreateUEContext service Operation",
		YAMLFile: "TS29518_Namf_Communication.yaml", ForgeSHA: sha,
		ForgeURL: model.ForgeRawURL("TS29518_Namf_Communication.yaml", sha, ""),
	}
	if err := st.InsertAPIOperations([]model.APIOperation{op}); err != nil {
		t.Fatal(err)
	}
	sc := model.APISchema{
		SchemaID: 1, SpecID: "29.571", Release: "Rel-18", Version: "18.11.0",
		Service: "CommonData", SchemaName: "Guami", Kind: "object",
		YAMLFile: "TS29571_CommonData.yaml", ForgeSHA: sha,
		ForgeURL: model.ForgeRawURL("TS29571_CommonData.yaml", sha, "Guami"),
	}
	if err := st.InsertAPISchemas([]model.APISchema{sc}); err != nil {
		t.Fatal(err)
	}

	c := mustClient(t, st)

	got := call(t, c, ctx, "search_api", map[string]any{"query": "create UE context", "kind": "operation"})
	if got["count"].(float64) < 1 {
		t.Fatalf("search_api(operation) found nothing")
	}
	cites, _ := got["citations"].([]any)
	if len(cites) == 0 {
		t.Fatal("search_api missing citations")
	}
	c0, _ := cites[0].(map[string]any)
	if url, _ := c0["url"].(string); url == "" || url[:5] != "https" {
		t.Errorf("citation url = %q, want forge https URL", c0["url"])
	}

	gotS := call(t, c, ctx, "search_api", map[string]any{"query": "Guami", "kind": "schema"})
	if gotS["count"].(float64) < 1 {
		t.Errorf("search_api(schema) found nothing")
	}
}

func mustClient(t *testing.T, st *store.Store) *client.Client {
	t.Helper()
	srv, _ := New(st, "test", "", nil, nil)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ir mcpgo.InitializeRequest
	ir.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	ir.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(context.Background(), ir); err != nil {
		t.Fatal(err)
	}
	return c
}
