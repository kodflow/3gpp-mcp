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

// federatedClient wires BOTH halves, which is the only configuration in which the
// routing defects this file pins are observable at all.
func federatedClient(t *testing.T) (*client.Client, context.Context) {
	t.Helper()
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
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "2",
			Heading: "References", Text: "profiling ETSI TS 103 221-1 and TS 103 280"},
	})

	etsi := open()
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 102 221", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 102 221", Release: "ETSI", Version: "18.4.0"})
	_ = etsi.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 221-1", DocType: "TS"})
	_ = etsi.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: "1.23.1"})
	_ = etsi.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "ETSI TS 102 221", Release: "ETSI", Version: "18.4.0", ClausePath: "2",
			Heading: "References", Text: "ETSI TS 103 221-1 and TS 23.038 apply"},
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
	return c, ctx
}

// THE DEFECT. findCrossRefs read h.st unconditionally while get_spec and
// trace_clause went through storeFor, so asking an ETSI deliverable for its
// references hit the 3GPP store, found no such spec, and answered count 0 — on
// clause 2, which IS the normative reference list. Silently: an empty answer is
// indistinguishable from a document that cites nothing.
//
// Falsified: with src reverted to h.st, count is 0, etsi_references is empty and
// release/version come back blank.
func TestCrossRefsRoutesToTheHalfThatOwnsTheSpec(t *testing.T) {
	c, ctx := federatedClient(t)
	out := call(t, c, ctx, "find_cross_references", map[string]any{"spec_id": "ETSI TS 102 221", "clause": "2"})

	if got := out["version"]; got != "18.4.0" {
		t.Errorf("version = %v, want 18.4.0 resolved from the ETSI half", got)
	}
	var etsiRefs []string
	b, _ := json.Marshal(out["etsi_references"])
	_ = json.Unmarshal(b, &etsiRefs)
	if len(etsiRefs) != 1 || etsiRefs[0] != "103 221-1" {
		t.Errorf("etsi_references = %v, want [103 221-1]", etsiRefs)
	}
	var refs []string
	b, _ = json.Marshal(out["references"])
	_ = json.Unmarshal(b, &refs)
	if len(refs) != 1 || refs[0] != "23.038" {
		t.Errorf("references = %v, want [23.038] — the 3GPP miner still runs on ETSI text", refs)
	}
}

// An ETSI mention used to cite the deliver FOLDER because a bare "TS 103 221-1"
// in prose names no version. That was right when the attached half held 14
// deliverables; it now holds 11 822 versions, so the corpus usually knows the
// exact one — and a versioned PDF is a citation a reader can open at the text.
// A deliverable the corpus does NOT hold must still cite the folder.
func TestEtsiReferenceCitesTheVersionTheCorpusHolds(t *testing.T) {
	c, ctx := federatedClient(t)
	out := call(t, c, ctx, "find_cross_references", map[string]any{"spec_id": "33.128", "clause": "2"})

	var cites []model.Citation
	b, _ := json.Marshal(out["etsi_ref_citations"])
	_ = json.Unmarshal(b, &cites)

	byID := map[string]model.Citation{}
	for _, c := range cites {
		byID[c.SpecID] = c
	}
	held, ok := byID["ETSI TS 103 221-1"]
	if !ok {
		t.Fatalf("no citation for the held deliverable, got %v", byID)
	}
	if held.Version != "1.23.1" {
		t.Errorf("version = %q, want 1.23.1 from the attached half", held.Version)
	}
	if want := "10322101v012301p.pdf"; !contains(held.URL, want) {
		t.Errorf("url = %q, want the versioned PDF ending %s", held.URL, want)
	}
	unheld, ok := byID["ETSI TS 103 280"]
	if !ok {
		t.Fatalf("a deliverable the corpus does not hold must still be cited, got %v", byID)
	}
	if unheld.Version != "" || !contains(unheld.URL, "/103280/") {
		t.Errorf("unheld = %+v, want no version and the deliver folder — never a fabricated version", unheld)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// A ZERO THAT MEANS TWO THINGS IS NOT AN ANSWER. get_changelog on an ETSI
// deliverable returned count 0, which reads as "this deliverable never changed"
// — on a corpus that keeps every published version because it did. The corpus
// genuinely holds no ETSI change requests (see the handler for why the source
// does not allow them), so the fix is to SAY that and name the tool that answers
// the question from the text.
//
// The 3GPP half must NOT gain the note: there, count 0 means what it says.
func TestChangelogSaysWhyTheEtsiHalfHasNone(t *testing.T) {
	c, ctx := federatedClient(t)

	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 102 221"})
	note, _ := out["note"].(string)
	if note == "" {
		t.Fatal("an empty ETSI changelog must say why, not just count 0")
	}
	if !contains(note, "trace_clause") {
		t.Errorf("the note must name the tool that DOES answer it: %q", note)
	}

	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "33.128"})
	if _, ok := out["note"]; ok {
		t.Errorf("the 3GPP half must not gain the note: %v", out["note"])
	}
}
