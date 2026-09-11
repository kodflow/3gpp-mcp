package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// coverageClient wires a 3GPP store carrying two curated edges and a CR-database
// shaped changelog (no clause lists, like every record ingest-crs writes), and —
// when etsiSetup is non-nil — an ETSI store the caller fills.
func coverageClient(t *testing.T, etsiSetup func(*store.Store)) (*client.Client, context.Context) {
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
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	for _, v := range []string{"18.3.0", "18.4.0", "18.5.0", "19.0.0"} {
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-" + v[:2], Version: v})
	}
	// Four records, one per version transition, and NO clause list on any of them:
	// that is the shape of the whole published 3GPP changelog since #322.
	if err := st.InsertChanges([]model.Change{
		{CRNumber: "0001", SpecID: "23.501", FromVersion: "18.2.0", ToVersion: "18.3.0", Summary: "a"},
		{CRNumber: "0002", SpecID: "23.501", FromVersion: "18.3.0", ToVersion: "18.4.0", Summary: "b"},
		{CRNumber: "0003", SpecID: "23.501", FromVersion: "18.4.0", ToVersion: "18.5.0", Summary: "c"},
		{CRNumber: "0004", SpecID: "23.501", FromVersion: "18.5.0", ToVersion: "19.0.0", Summary: "d"},
	}); err != nil {
		t.Fatal(err)
	}
	// NULL, the way ingest-crs writes it. InsertChanges cannot: it routes an empty
	// list through string_split('', sep), which stores [''] — a producer that is
	// structurally unable to write the row production holds (api_empty_list_test.go
	// tells the same story for search_api).
	if _, err := st.DB().ExecContext(ctx, `UPDATE changes SET clauses = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertEvolutions([]model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT", JustificationSpec: "23.501", JustificationClause: "6.2.1", Confidence: 0.9},
		{FromTerm: "PCRF", ToTerm: "PCF", EvolutionType: "RENAME", JustificationSpec: "23.501", JustificationClause: "6.2.4", Confidence: 0.9},
	}); err != nil {
		t.Fatal(err)
	}

	var etsi store.Reader
	if etsiSetup != nil {
		e := open()
		etsiSetup(e)
		etsi = e
	}
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

// etsiWithX1 is an ETSI half holding TS 103 221-1 at three versions and nothing
// else — no evolution edge, no change record, no changes_source. The state the
// published etsi.duckdb is in (2026-09-11: changes = 0, evolutions = 0).
func etsiWithX1(e *store.Store) {
	_ = e.UpsertSpec(model.Spec{SpecID: "ETSI TS 103 221-1", DocType: "TS"})
	for _, v := range []string{"1.21.1", "1.22.1", "1.23.1"} {
		_ = e.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 103 221-1", Release: "ETSI", Version: v,
			DocxURL: model.SpecURL("ETSI TS 103 221-1", v)})
	}
}

func noteOf(out map[string]any) string { s, _ := out["note"].(string); return s }

func changeNumbers(t *testing.T, out map[string]any) []string {
	t.Helper()
	var cs []model.Change
	b, _ := json.Marshal(out["changes"])
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatalf("changes is not a list of records: %s", b)
	}
	var n []string
	for _, c := range cs {
		n = append(n, c.CRNumber)
	}
	return n
}

// A VERSION BOUND WAS ACCEPTED AND IGNORED, on the published corpus, today:
// get_changelog(23.501, 18.4.0 .. 18.5.0) returned all 3 064 records — the same
// count as no bound at all — and so did from_release="banana". store.GetChangelog
// reads a bound through releaseMajor, and "not a release" meant "no bound".
//
// Falsified: with applyVersionBounds returning its input, the version-bounded call
// returns all four records; with boundsNote returning "", the banana call carries
// no word about the dropped bound.
func TestChangelogAppliesVersionBoundsAndNamesTheOnesItCannot(t *testing.T) {
	c, ctx := coverageClient(t, nil)

	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "18.4.0", "to_release": "18.5.0"})
	if got := strings.Join(changeNumbers(t, out), ","); got != "0002,0003" {
		t.Errorf("18.4.0..18.5.0 returned [%s], want [0002,0003] — the records whose to_version lies in the range", got)
	}
	// ETSI prints its versions "V1.23.1"; the cover-page spelling must work too.
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "V18.5.0"})
	if got := strings.Join(changeNumbers(t, out), ","); got != "0003,0004" {
		t.Errorf("from V18.5.0 returned [%s], want [0003,0004]", got)
	}
	// The release path is the store's and must be untouched.
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "Rel-18", "to_release": "Rel-18"})
	if got := len(changeNumbers(t, out)); got != 3 {
		t.Errorf("Rel-18..Rel-18 returned %d records, want 3", got)
	}

	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "banana"})
	if got := len(changeNumbers(t, out)); got != 4 {
		t.Errorf("an unreadable bound must not filter anything; got %d records", got)
	}
	if n := noteOf(out); !strings.Contains(n, `"banana"`) || !strings.Contains(n, "NOT") {
		t.Errorf("an unreadable bound must be NAMED as not applied; note = %q", n)
	}
}

// AN EMPTY RANGE IS NOT AN EMPTY HISTORY. The note used to be computed from the
// range-filtered records, so Rel-5..Rel-5 on a spec whose history starts at Rel-18
// told the caller "this corpus holds no citable change-request records for
// 23.501" — about a spec it holds four records for.
//
// Falsified: feeding the note the bounded set brings "no citable" back.
func TestAnEmptyRangeDoesNotDenyTheSpecHasRecords(t *testing.T) {
	c, ctx := coverageClient(t, nil)
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "Rel-5", "to_release": "Rel-5"})
	if n, _ := out["count"].(float64); n != 0 {
		t.Fatalf("Rel-5..Rel-5 matched %v records", out["count"])
	}
	n := noteOf(out)
	if strings.Contains(n, "no citable") {
		t.Errorf("the range was empty, the SPEC is not; note = %q", n)
	}
	if !strings.Contains(n, "none of the 4 records") {
		t.Errorf("the note must say the spec's records fall outside the range; got %q", n)
	}
}

// A FILTER THAT CANNOT MATCH IS NOT A NEGATIVE ANSWER. Every record ingest-crs
// writes leaves `clauses` NULL — the CR database records a version transition,
// not the clauses a change touched — so get_changelog(spec, clause=X) answered 0
// for every clause of every 3GPP spec, which reads as "nothing ever changed X".
//
// Falsified: without the `narrowed` clause branch the note says nothing about the
// filter.
func TestAClauseFilterOverClauselessRecordsSaysItCannotMatch(t *testing.T) {
	c, ctx := coverageClient(t, nil)
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "clause": "5.4"})
	if n, _ := out["count"].(float64); n != 0 {
		t.Fatalf("count = %v, want 0: no record names a clause", out["count"])
	}
	if n := noteOf(out); !strings.Contains(n, "cannot match") || !strings.Contains(n, "trace_clause") {
		t.Errorf("the note must say the filter cannot match and name the tool that answers; got %q", n)
	}
	// And `changes` is a list, never JSON null, whatever the count.
	if _, ok := out["changes"].([]any); !ok {
		t.Errorf(`"changes" = %#v, want an empty list`, out["changes"])
	}
}

// A PARTLY BLIND FILTER IS NOT A COMPLETE ANSWER (Qodo, #332). When some records
// name their clauses and others do not, the filter drops the others untested; a
// count built from the rest — zero or not — must say how many it could not test.
//
// Falsified: with the partial branch removed (only the all-clauseless wording
// kept), both calls below come back with no note.
func TestAPartlyBlindClauseFilterSaysWhatItCouldNotTest(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.3.0"})
	if err := st.InsertChanges([]model.Change{
		{CRNumber: "0100", SpecID: "24.501", FromVersion: "18.1.0", ToVersion: "18.2.0", Clauses: []string{"5.4.1"}, Summary: "named"},
		{CRNumber: "0101", SpecID: "24.501", FromVersion: "18.2.0", ToVersion: "18.3.0", Summary: "unnamed"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE changes SET clauses = NULL WHERE cr_number = '0101'`); err != nil {
		t.Fatal(err)
	}
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

	for _, tc := range []struct {
		clause string
		count  float64
	}{{"5.4", 1}, {"9.9", 0}} {
		out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "24.501", "clause": tc.clause})
		if n, _ := out["count"].(float64); n != tc.count {
			t.Errorf("clause %s: count = %v, want %v", tc.clause, out["count"], tc.count)
		}
		if n := noteOf(out); !strings.Contains(n, "1 of the 2 records name no clause") {
			t.Errorf("clause %s: the note must say one record could not be tested; got %q", tc.clause, n)
		}
	}
}

// THE ETSI NOTE FOLLOWS THE CORPUS IT IS SERVED FROM. Three states, three answers:
// no ETSI half attached (the old code said nothing at all — both branches
// declined), an ETSI half with no change records (the published state), and an
// ETSI half whose changelog was written from the deliverables' own annexes — where
// the old blanket "this corpus holds no change-request records for the ETSI half"
// would be false, and where every record must cite the PDF it was read from.
//
// Falsified: with etsiChangelogNote's `etsi == nil` branch removed the first call
// panics or answers silently; with the source branches removed the third call
// still claims the half holds nothing; with the citation block removed the records
// cite nothing.
func TestEtsiChangelogSaysWhichOfItsThreeStatesItIsIn(t *testing.T) {
	// 1. No ETSI half.
	c, ctx := coverageClient(t, nil)
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); !strings.Contains(n, "not attached") {
		t.Errorf("an ETSI spec on a 3GPP-only server must say the half is absent; note = %q", n)
	}

	// 2. ETSI half attached, no changelog written (today's corpus).
	c, ctx = coverageClient(t, etsiWithX1)
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); !strings.Contains(n, "holds no change-request records for the ETSI half") || !strings.Contains(n, "never changed") {
		t.Errorf("the unwritten ETSI changelog must say so and what 0 means; note = %q", n)
	}

	// 3. ETSI changelog written from the annexes, for this deliverable only.
	c, ctx = coverageClient(t, func(e *store.Store) {
		etsiWithX1(e)
		_ = e.UpsertSpec(model.Spec{SpecID: "ETSI TS 102 221", DocType: "TS"})
		_ = e.UpsertVersion(model.SpecVersion{SpecID: "ETSI TS 102 221", Release: "ETSI", Version: "18.4.0"})
		if err := e.InsertChanges([]model.Change{
			{CRNumber: "CR072", CRRevision: 3, SpecID: "ETSI TS 103 221-1", FromVersion: "1.21.1", ToVersion: "1.22.1", Category: "B", Summary: "Update to add TCPPortList"},
			{CRNumber: "CR077", CRRevision: 2, SpecID: "ETSI TS 103 221-1", FromVersion: "1.22.1", ToVersion: "1.23.1", Category: "F", Summary: "Improvement of References"},
			{CRNumber: "CR078", CRRevision: 1, SpecID: "ETSI TS 103 221-1", FromVersion: "1.22.1", ToVersion: "1.23.1", Category: "B", Summary: "Selective IRI Delivery Provisioning"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := e.SetMeta("changes_source", "change-history annexes"); err != nil {
			t.Fatal(err)
		}
	})
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1", "from_release": "1.23.1"})
	if got := strings.Join(changeNumbers(t, out), ","); got != "CR077,CR078" {
		t.Errorf("from 1.23.1 returned [%s], want [CR077,CR078] — ETSI bounds are versions", got)
	}
	n := noteOf(out)
	if strings.Contains(n, "holds no change-request records for the ETSI half") {
		t.Errorf("the half HAS records now; the blanket denial is false: %q", n)
	}
	if !strings.Contains(n, "first published version whose annex lists") {
		t.Errorf("the note must say how to_version was derived; got %q", n)
	}
	var cites []model.Citation
	b, _ := json.Marshal(out["citations"])
	_ = json.Unmarshal(b, &cites)
	if len(cites) != 1 || cites[0].Version != "1.23.1" || !strings.HasSuffix(cites[0].URL, "ts_10322101v012301p.pdf") {
		t.Fatalf("citations = %+v, want ONE: the published PDF of 1.23.1, whose annex lists both records", cites)
	}
	if !cites[0].Stable {
		t.Errorf("a /deliver publication is not a draft: %+v", cites[0])
	}

	// The sibling with no record must not borrow the half-wide denial either.
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 102 221"})
	n = noteOf(out)
	if !strings.Contains(n, "no change-request record for ETSI TS 102 221") || strings.Contains(n, "for the ETSI half:") {
		t.Errorf("a deliverable without records, on a half with some, must speak for itself; note = %q", n)
	}
}

// TRACE_EVOLUTION READ ONE HALF AND CALLED ITS SILENCE AN ANSWER. Measured on
// the published corpus: UICC, X1, ADMF and "ETSI TS 103 221-1" all answered
// count 0, evolutions null, note "Curated NE↔NF seed" — from a table the ETSI half
// was never consulted for.
//
// Falsified: reading h.st alone loses the ETSI edge below (count 0); with the
// zero branch of evolutionNote removed the UICC answer no longer says what its 0
// means; with reDocumentID removed the deliverable id gets no redirect.
func TestTraceEvolutionFederatesAndSaysWhatItsZeroMeans(t *testing.T) {
	c, ctx := coverageClient(t, func(e *store.Store) {
		etsiWithX1(e)
		// An edge ONLY the ETSI half holds, justified by an ETSI clause: the
		// fixture a future curated ETSI seed would produce. It is invented for
		// the test and never written to any corpus.
		if err := e.InsertEvolutions([]model.Evolution{
			{FromTerm: "LEGACY-X", ToTerm: "X1", EvolutionType: "REPLACED_BY", JustificationSpec: "ETSI TS 103 221-1", JustificationClause: "5", Confidence: 0.5},
		}); err != nil {
			t.Fatal(err)
		}
	})

	out := call(t, c, ctx, "trace_evolution", map[string]any{"entity": "X1"})
	if n, _ := out["count"].(float64); n != 1 {
		t.Fatalf("the ETSI half's edge was not served: count = %v", out["count"])
	}
	var cites []model.Citation
	b, _ := json.Marshal(out["citations"])
	_ = json.Unmarshal(b, &cites)
	if len(cites) != 1 || cites[0].Version != "1.23.1" || !strings.HasSuffix(cites[0].URL, "ts_10322101v012301p.pdf") {
		t.Errorf("an ETSI justification must cite the ETSI PDF of the version held; got %+v", cites)
	}

	out = call(t, c, ctx, "trace_evolution", map[string]any{"entity": "UICC"})
	if _, ok := out["evolutions"].([]any); !ok {
		t.Errorf(`"evolutions" = %#v, want an empty list, never null`, out["evolutions"])
	}
	n := noteOf(out)
	for _, want := range []string{"CURATED", "no curated edge", "the 3GPP half holds 2", "the ETSI half holds 1"} {
		if !strings.Contains(n, want) {
			t.Errorf("zero-edge note lacks %q: %q", want, n)
		}
	}
	held, _ := out["edges_held"].(map[string]any)
	if held["3gpp"] != float64(2) || held["etsi"] != float64(1) {
		t.Errorf("edges_held = %v, want 3gpp=2 etsi=1, read from the corpora", held)
	}

	out = call(t, c, ctx, "trace_evolution", map[string]any{"entity": "ETSI TS 103 221-1"})
	if n := noteOf(out); !strings.Contains(n, "names a DOCUMENT") || !strings.Contains(n, "get_changelog") {
		t.Errorf("a deliverable id must be redirected to the tools that trace documents; note = %q", n)
	}

	out = call(t, c, ctx, "trace_evolution", map[string]any{"entity": "MME"})
	if n := noteOf(out); strings.Contains(n, "no evolution edge") || strings.Contains(n, "DOCUMENT") {
		t.Errorf("a served edge must not carry the zero note: %q", n)
	}
}

// Without the ETSI half the note must say so rather than count a half it never
// read.
func TestTraceEvolutionWithoutTheEtsiHalfSaysSo(t *testing.T) {
	c, ctx := coverageClient(t, nil)
	out := call(t, c, ctx, "trace_evolution", map[string]any{"entity": "ADMF"})
	if n := noteOf(out); !strings.Contains(n, "the ETSI half is not attached") {
		t.Errorf("note = %q", n)
	}
	if held, _ := out["edges_held"].(map[string]any); len(held) != 1 {
		t.Errorf("edges_held = %v, want only the 3GPP half", held)
	}
}

func TestParseChangelogBound(t *testing.T) {
	for _, c := range []struct {
		in               string
		release, version string
		invalid          bool
	}{
		{"", "", "", false},
		{"Rel-18", "Rel-18", "", false},
		{"18", "18", "", false},
		{"18.4.0", "", "18.4.0", false},
		{"V1.23.1", "", "1.23.1", false},
		{" 1.10 ", "", "1.10", false},
		{"banana", "", "", true},
		{"Rel-18.4", "", "", true},
	} {
		b := parseChangelogBound(c.in)
		if b.release != c.release || b.version != c.version || b.invalid != c.invalid {
			t.Errorf("parseChangelogBound(%q) = %+v", c.in, b)
		}
	}
}

// Review round (CodeRabbit, #332): three more places where an answer could say
// less than it knew.
//
// Falsified: with the uncited note removed, the record with no held version is
// served beside an empty citations block in silence; with `scope` derived from
// the raw arguments again, the banana note says "in range" after saying the bound
// was NOT applied; with trace_evolution aborting on the first failed half, the
// 3GPP edges are lost to an ETSI read error.
func TestReviewRoundTheAnswerSaysWhatItCouldNotDo(t *testing.T) {
	c, ctx := coverageClient(t, func(e *store.Store) {
		etsiWithX1(e)
		if err := e.InsertChanges([]model.Change{
			{CRNumber: "CR090", SpecID: "ETSI TS 103 221-1", FromVersion: "1.23.1", ToVersion: "1.99.1", Category: "F", Summary: "a version the corpus does not hold"},
		}); err != nil {
			t.Fatal(err)
		}
		_ = e.SetMeta("changes_source", "change-history annexes")
	})
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); !strings.Contains(n, "1 of these records name a version") || !strings.Contains(n, "no citation") {
		t.Errorf("a record that cannot be cited must be named as such; note = %q", n)
	}

	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "banana", "clause": "5.4"})
	if n := noteOf(out); !strings.Contains(n, "NOT") || strings.Contains(n, " in range") {
		t.Errorf("an unapplied bound must not be called a range in the same note; note = %q", n)
	}

	// The ETSI half is attached and then made unreadable.
	c, ctx = coverageClient(t, func(e *store.Store) {
		etsiWithX1(e)
		_ = e.Close()
	})
	out = call(t, c, ctx, "trace_evolution", map[string]any{"entity": "MME"})
	if n, _ := out["count"].(float64); n != 1 {
		t.Fatalf("an ETSI read error cost the 3GPP edge: count = %v", out["count"])
	}
	if n := noteOf(out); !strings.Contains(n, "the ETSI half could not be read") {
		t.Errorf("the unread half must be named; note = %q", n)
	}
	held, _ := out["edges_held"].(map[string]any)
	if _, ok := held["etsi"]; ok || held["3gpp"] != float64(2) {
		t.Errorf("edges_held = %v, want only the 3GPP half's count", held)
	}
}
