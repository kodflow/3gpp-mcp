package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// ONE VERSION FILED UNDER SEVERAL RELEASES IS SERVED ONCE (filings.go).
//
// The fixture has the three shapes measured on the published corpus: a draft
// filed under two releases with identical copies (22.890 0.7.0, Rel-17 and
// Rel-18 — get_spec served each of its clauses twice), a stable version whose
// major names one of its filings (26.510 18.4.0, Rel-18 and Rel-20), and — not
// found in the corpus, but the case the fold must never swallow — two filings of
// one version whose texts differ.
func filingsFixture(t *testing.T) *store.Store {
	t.Helper()
	st := memStore(t)
	id := uint64(0)
	put := func(spec, rel, ver string, clauses ...[2]string) {
		_ = st.UpsertSpec(model.Spec{SpecID: spec, Series: spec[:2], DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: spec, Release: rel, Version: ver})
		for _, c := range clauses {
			id++
			if err := st.InsertClauses([]model.Clause{{ChunkID: id, SpecID: spec, Release: rel, Version: ver,
				ClausePath: c[0], Heading: "h" + c[0], Text: c[1]}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Two rows share the path "4" in each filing: GetClauses does not fix their
	// order, and identical copies must still be recognised as identical.
	draft := [][2]string{{"4", "first of two under 4"}, {"4", "second of two under 4"}, {"5", "the draft text"}, {"5.1", "a sub-clause"}}
	put("22.890", "Rel-17", "0.7.0", draft...)
	put("22.890", "Rel-18", "0.7.0", draft[2], draft[3], draft[1], draft[0])
	put("22.890", "Rel-19", "19.0.0", [2]string{"5", "the published text"})
	put("26.510", "Rel-20", "18.4.0", [2]string{"1", "scope of 18.4.0"})
	put("26.510", "Rel-18", "18.4.0", [2]string{"1", "scope of 18.4.0"})
	put("99.999", "Rel-5", "1.0.0", [2]string{"1", "the Rel-5 copy"})
	put("99.999", "Rel-6", "1.0.0", [2]string{"1", "the Rel-6 copy, which differs"})
	// A catalogue row with no clauses behind it, as 26.510 18.4.0 has under Rel-18
	// on the published corpus while its text is filed under Rel-20.
	put("27.777", "Rel-20", "18.4.0", [2]string{"1", "scope, filed under Rel-20"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "27.777", Release: "Rel-18", Version: "18.4.0"})
	// Same under clause 5, different under clause 6: a clause-filtered call folds
	// what it was given, and must say only that (Qodo, #347).
	put("88.888", "Rel-5", "1.0.0", [2]string{"5", "same under 5"}, [2]string{"6", "the Rel-5 text of 6"})
	put("88.888", "Rel-6", "1.0.0", [2]string{"5", "same under 5"}, [2]string{"6", "the Rel-6 text of 6, which differs"})
	return st
}

func citedReleases(out map[string]any) map[string]int {
	got := map[string]int{}
	list, _ := out["citations"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		r, _ := m["release"].(string)
		got[r]++
	}
	return got
}

func TestAVersionFiledUnderSeveralReleasesIsServedOnce(t *testing.T) {
	c, ctx := clientOver(t, filingsFixture(t), nil)

	for _, rel := range []string{"Rel-17", "Rel-18"} {
		out, text, isErr := callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "22.890", "release": rel})
		if isErr {
			t.Fatalf("get_spec(22.890, %s): %s", rel, text)
		}
		if n, _ := out["count"].(float64); n != 4 {
			t.Errorf("get_spec(22.890, %s): count = %v, want the 4 clauses once, not once per filing", rel, out["count"])
		}
		if got := citedReleases(out); len(got) != 1 || got[rel] != 4 {
			t.Errorf("get_spec(22.890, %s): every citation must name the release asked for; got %v", rel, got)
		}
		if fu := fmt.Sprint(out["filed_under"]); fu != "[Rel-17 Rel-18]" {
			t.Errorf("get_spec(22.890, %s): filed_under = %s, want [Rel-17 Rel-18]", rel, fu)
		}
		if n := noteOf(out); !strings.Contains(n, "served once, cited as "+rel) {
			t.Errorf("get_spec(22.890, %s): the note must say it is served once; note = %q", rel, n)
		}
	}

	// Not filed where asked (no release): the release the version's major names.
	out, _, _ := callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "26.510", "version": "18.4.0"})
	if got := citedReleases(out); got["Rel-18"] != 1 || len(got) != 1 {
		t.Errorf("get_spec(26.510, 18.4.0): want one clause cited as Rel-18, the release 18.x names; got %v", got)
	}

	// Copies that differ are two documents: both served, and the note says so.
	out, _, _ = callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "99.999", "version": "1.0.0"})
	if n, _ := out["count"].(float64); n != 2 {
		t.Errorf("get_spec(99.999, 1.0.0): count = %v, want both differing copies", out["count"])
	}
	if n := noteOf(out); !strings.Contains(n, "DIFFER") {
		t.Errorf("differing copies must be named as such; note = %q", n)
	}

	// A version filed once is untouched.
	out, _, _ = callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "22.890", "release": "Rel-19"})
	if _, ok := out["filed_under"]; ok || out["count"] != float64(1) {
		t.Errorf("a version filed once must be served as before; got count %v, filed_under %v", out["count"], out["filed_under"])
	}

	// A clause filter is applied before the fold, so the note claims only what it
	// compared — and the unfiltered call on the same spec says the text differs.
	out, _, _ = callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "88.888", "version": "1.0.0", "clause": "5"})
	if n := noteOf(out); !strings.Contains(n, "the text under clause 5 is identical in each") {
		t.Errorf("a clause-filtered fold must name its scope; note = %q", n)
	}
	out, _, _ = callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "88.888", "version": "1.0.0"})
	if n, _ := out["count"].(float64); n != 4 {
		t.Errorf("88.888 1.0.0 differs between its filings: count = %v, want both copies", out["count"])
	}
	if n := noteOf(out); !strings.Contains(n, "the text DIFFERS") {
		t.Errorf("the unfiltered answer must say the text differs; note = %q", n)
	}

	// A release and a version that name two different documents is refused, as
	// resources/read refuses it (#345).
	_, text, isErr := callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "22.890", "release": "Rel-19", "version": "0.7.0"})
	if !isErr || !strings.Contains(text, "published in Rel-17, not Rel-19") {
		t.Errorf("an explicit release/version mismatch must be refused; isError=%v %q", isErr, text)
	}

	// The catalogue files 27.777 18.4.0 under Rel-18, but the text this corpus
	// holds is filed under Rel-20: the answer is scoped to what it served.
	out, _, _ = callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "27.777", "release": "Rel-18", "version": "18.4.0"})
	if out["release"] != "Rel-20" {
		t.Errorf("release = %v, want the release the clauses are filed under", out["release"])
	}
	if n := noteOf(out); !strings.Contains(n, "are filed under Rel-20, not Rel-18") {
		t.Errorf("the answer must say which filing it served; note = %q", n)
	}

	// resources/read of the filing: each clause once.
	var rr mcpgo.ReadResourceRequest
	rr.Params.URI = "3gpp://22.890/Rel-18@0.7.0"
	res, err := c.ReadResource(ctx, rr)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Contents)
	if n := strings.Count(string(b), "the draft text"); n != 1 {
		t.Errorf("resources/read %s served clause 5 %d times, want once", rr.Params.URI, n)
	}
}

// The baseline is not a release the CALLER named, so a version from another
// release is answered rather than refused — and the answer names the release it
// served, not the baseline (Qodo, #347).
func TestTheAnswerNamesTheFilingItServedNotTheBaseline(t *testing.T) {
	ctx := context.Background()
	srv, _ := New(filingsFixture(t), "test", "Rel-19", nil, nil)
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
	out, text, isErr := callAny(t, c, ctx, "get_spec", map[string]any{"spec_id": "22.890", "version": "0.7.0"})
	if isErr {
		t.Fatalf("the baseline must not refuse a version of another release: %s", text)
	}
	if out["release"] != "Rel-17" {
		t.Errorf("release = %v, want the filing actually served (Rel-17)", out["release"])
	}
	if got := citedReleases(out); got["Rel-17"] != 4 || len(got) != 1 {
		t.Errorf("citations = %v, want the served filing's release", got)
	}
}

// is_normative is part of the answer, so filings that disagree on it are not
// interchangeable (Qodo, #347).
func TestFilingsThatDisagreeOnNormativeStatusAreNotFolded(t *testing.T) {
	a := []model.Clause{{Release: "Rel-5", Version: "1.0.0", ClausePath: "1", Text: "same", IsNormative: true}}
	b := []model.Clause{{Release: "Rel-6", Version: "1.0.0", ClausePath: "1", Text: "same", IsNormative: false}}
	kept, filed := oneFiling(append(a, b...), "")
	if len(kept) != 2 || len(filed) != 2 {
		t.Errorf("want both copies served and both releases named; got %d clauses, filed_under %v", len(kept), filed)
	}
}

func TestOneFilingKeepsSeveralVersionsAndSingleFilingsAsTheyAre(t *testing.T) {
	mixed := []model.Clause{
		{Release: "Rel-17", Version: "0.7.0", ClausePath: "5", Text: "a"},
		{Release: "Rel-18", Version: "0.8.0", ClausePath: "5", Text: "a"},
	}
	if got, fu := oneFiling(mixed, ""); len(got) != 2 || fu != nil {
		t.Errorf("several versions are not one document: got %d clauses, filed_under %v", len(got), fu)
	}
	rs := []string{"Rel-9", "Rel-12", "Rel-99", "GSM", "Rel-4"}
	sortReleases(rs)
	if fmt.Sprint(rs) != "[GSM Rel-99 Rel-4 Rel-9 Rel-12]" {
		t.Errorf("release order = %v", rs)
	}
}

// A CHANGES_SOURCE STAMP THAT COULD NOT BE READ IS NOT AN ABSENT STAMP.
//
// Through GetMeta a failed read was "", and "" chose the ETSI note that says the
// half holds NO change record — served beside the records the same call returned.
func TestChangelogNotesSayWhenTheStampCouldNotBeRead(t *testing.T) {
	closed := memStore(t)
	_ = closed.Close()
	withRecords := func(stamp bool) *store.Store {
		e := memStore(t)
		etsiWithX1(e)
		if err := e.InsertChanges([]model.Change{{CRNumber: "CR001", SpecID: "ETSI TS 103 221-1",
			FromVersion: "1.21.1", ToVersion: "1.22.1", Category: "F", Summary: "a record"}}); err != nil {
			t.Fatal(err)
		}
		if stamp {
			_ = e.SetMeta("changes_source", "change-history annexes")
		}
		return e
	}
	st := memStore(t)
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.5.0"})

	// Control: stamp readable, records served, no "no records" claim.
	c, ctx := clientOver(t, st, withRecords(true))
	out, _, _ := callAny(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); out["count"] != float64(1) || strings.Contains(n, "holds no change-request record") {
		t.Fatalf("control: count %v, note %q", out["count"], n)
	}

	// The stamp cannot be read: the records are still served, and the note says
	// the stamp is unreadable instead of denying the records.
	c, ctx = clientOver(t, st, &countlessHalf{Reader: withRecords(true), closed: closed})
	out, _, _ = callAny(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	n := noteOf(out)
	if out["count"] != float64(1) {
		t.Fatalf("the record was lost: count = %v", out["count"])
	}
	if strings.Contains(n, "holds no change-request record") || !strings.Contains(n, "could not be read") {
		t.Errorf("an unreadable stamp must be named, and never deny the records beside it; note = %q", n)
	}

	// No stamp at all, but records: still no denial.
	c, ctx = clientOver(t, st, withRecords(false))
	out, _, _ = callAny(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); strings.Contains(n, "holds no change-request record") {
		t.Errorf("records without a stamp are still records; note = %q", n)
	}

	// No record and an unreadable stamp: the note cannot say whether the half has any.
	e := memStore(t)
	etsiWithX1(e)
	c, ctx = clientOver(t, st, &countlessHalf{Reader: e, closed: closed})
	out, _, _ = callAny(t, c, ctx, "get_changelog", map[string]any{"spec_id": "ETSI TS 103 221-1"})
	if n := noteOf(out); !strings.Contains(n, "cannot say whether") {
		t.Errorf("want the note to say it cannot tell; note = %q", n)
	}

	// The 3GPP note names an unreadable export stamp too.
	c, ctx = clientOver(t, &countlessHalf{Reader: st, closed: closed}, nil)
	out, _, _ = callAny(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501"})
	if n := noteOf(out); !strings.Contains(n, "export stamp could not be read") {
		t.Errorf("the 3GPP note must name an unreadable stamp; note = %q", n)
	}
}
