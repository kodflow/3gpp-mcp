package li

import (
	"context"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func TestAuditTokens(t *testing.T) {
	cases := map[string][]string{
		"RESTORE_DATA":     {"restore", "data"},
		"EUTRAN_ATTACH":    {"eutran", "attach"}, // hyphen normalised: matches "e-utran"
		"START_OF_INTCPT":  {},                   // all tokens are stop/short
		"MAP_SEND_ROUTING": {"map", "send", "routing"},
	}
	for in, want := range cases {
		got := auditTokens(in)
		if len(got) != len(want) {
			t.Errorf("auditTokens(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("auditTokens(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestFracIn(t *testing.T) {
	// "e-utran attach procedure" normalised drops the hyphen -> "eutran" matches.
	txt := auditNorm("E-UTRAN attach procedure")
	if f := fracIn([]string{"eutran", "attach"}, txt); f < 1.0 {
		t.Errorf("fracIn co-located tokens = %v, want 1.0", f)
	}
	if f := fracIn([]string{"restore", "data"}, "the create session request"); f != 0 {
		t.Errorf("fracIn absent tokens = %v, want 0", f)
	}
}

func TestKnownHomeOverride(t *testing.T) {
	for _, ev := range []string{"RESTORE_DATA", "REGISTRATION_REFRESH"} {
		if kh, ok := knownHome[ev]; !ok || kh.spec == "" || kh.clause == "" {
			t.Errorf("knownHome[%q] missing or incomplete: %+v", ev, kh)
		}
	}
}

// --- the audit against a fake corpus ---------------------------------------

// fakeStore implements only what the audit reads. The embedded nil Reader makes
// any OTHER call panic, which is the point: a test that silently exercised a
// different code path would prove nothing.
type fakeStore struct {
	store.Reader
	latest  map[string]string        // spec -> latest version
	clauses map[string][]model.Clause // spec -> clauses of that version
	hits    []model.SearchHit         // what the lexical ranker returns, in order
}

func (f *fakeStore) LatestVersion(_ context.Context, spec string) (string, string, bool, error) {
	v, ok := f.latest[spec]
	return "Rel-19", v, ok, nil
}

func (f *fakeStore) GetClauses(_ context.Context, spec, _, _ string) ([]model.Clause, error) {
	return f.clauses[spec], nil
}

func (f *fakeStore) SearchClauses(_ context.Context, q store.SearchQuery) ([]model.SearchHit, error) {
	out := make([]model.SearchHit, 0, len(f.hits))
	for _, h := range f.hits {
		if q.Filter.SpecID != "" && h.Clause.SpecID != q.Filter.SpecID {
			continue
		}
		if len(out) == q.TopK {
			break
		}
		out = append(out, h)
	}
	return out, nil
}

func hit(spec, path, heading string) model.SearchHit {
	return model.SearchHit{Clause: model.Clause{SpecID: spec, ClausePath: path, Heading: heading}}
}

// versions repeats one clause once per release, the way `clauses` really holds
// it: 17 rows for a single normative paragraph.
func versions(n int, spec, path, heading string) []model.SearchHit {
	out := make([]model.SearchHit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, hit(spec, path, heading))
	}
	return out
}

func auditOne(t *testing.T, st store.Reader, ev SentinelEvent) Finding {
	t.Helper()
	fs, err := AuditCatalog(context.Background(), st, []SentinelEvent{ev})
	if err != nil {
		t.Fatal(err)
	}
	return fs[0]
}

// The regression this whole change exists for: the ranker's window filled with
// one clause repeated across releases, so the true home never got scored.
func TestTheRelocationWindowCountsClausesNotVersions(t *testing.T) {
	st := &fakeStore{
		latest:  map[string]string{"33.108": "19.0.0"},
		clauses: map[string][]model.Clause{"33.108": {{ClausePath: "10.1", Heading: "unrelated", Text: "unrelated"}}},
		// Twelve copies of a near-miss (heading holds "imei", not "check"),
		// then the real home. A raw TopK of 12 never reaches the last row.
		hits: append(versions(12, "29.002", "25.6.6", "Macro Obtain_IMEI_VLR"),
			hit("29.273", "5.2.3.35", "IMEI-Check-In-VPLMN-Result")),
	}
	f := auditOne(t, st, SentinelEvent{NF: "HSS", Event: "CHECK_IMEI", Spec: "33.108", Clause: "Annex"})
	if f.Verdict != VWrongSpec {
		t.Fatalf("verdict = %s (%s), want %s", f.Verdict, f.Why, VWrongSpec)
	}
	if f.RealSpec != "29.273" || f.RealClause != "5.2.3.35" {
		t.Fatalf("relocated to %s §%s, want 29.273 §5.2.3.35", f.RealSpec, f.RealClause)
	}
}

// Front matter carries no clause path and all of it collapses into one map slot.
// parentClause("Annex") is "", so a citation with no parent must NOT be checked
// against whichever unpathed row happened to be stored last.
func TestFrontMatterIsNotEvidence(t *testing.T) {
	st := &fakeStore{
		latest: map[string]string{"33.108": "19.0.0"},
		clauses: map[string][]model.Clause{"33.108": {
			// A table of contents naming every annex: it "contains" the tokens
			// of almost any operation name.
			{ClausePath: "", Heading: "Contents", Text: "Annex B: any time interrogation, send authentication info, purge MS"},
		}},
	}
	f := auditOne(t, st, SentinelEvent{NF: "HLR", Event: "ANY_TIME_INTERROGATION", Spec: "33.108", Clause: "Annex"})
	if f.Verdict == VConfirmed || f.Verdict == VParentRef {
		t.Fatalf("a table of contents must not confirm an event, got %s", f.Verdict)
	}
}

// A real parent still works — the fix must not disarm the tier.
func TestARealParentStillSupportsItsChild(t *testing.T) {
	st := &fakeStore{
		latest: map[string]string{"33.128": "19.7.0"},
		clauses: map[string][]model.Clause{"33.128": {
			{ClausePath: "6.2.2", Heading: "AMF registration", Text: "the registration procedure"},
		}},
	}
	f := auditOne(t, st, SentinelEvent{NF: "AMF", Event: "AMF_REGISTRATION", Spec: "33.128", Clause: "6.2.2.9"})
	if f.Verdict != VParentRef {
		t.Fatalf("verdict = %s, want %s", f.Verdict, VParentRef)
	}
}

// IP_RELEASE reduces to one token, so every heading holding "release" ties at
// 1.0 and the winner is whoever was ranked first. That is a draw, not a finding.
func TestASingleTokenNameCannotRelocate(t *testing.T) {
	st := &fakeStore{
		latest:  map[string]string{"33.108": "19.0.0"},
		clauses: map[string][]model.Clause{"33.108": {{ClausePath: "10.1", Heading: "x", Text: "y"}}},
		hits:    []model.SearchHit{hit("38.331", "5.3.5.12a.1.1", "IP Address Release")},
	}
	f := auditOne(t, st, SentinelEvent{NF: "PGW", Event: "IP_RELEASE", Spec: "33.108", Clause: "10.5.1.5"})
	if f.Verdict != VAmbiguous {
		t.Fatalf("verdict = %s, want %s", f.Verdict, VAmbiguous)
	}
	if f.RealSpec != "" {
		t.Fatalf("an undecided audit must name no home, got %s", f.RealSpec)
	}
	if f.Why == "" {
		t.Fatal("AMBIGUOUS must say why")
	}
}

// Two specs naming the operation equally well are two candidate homes.
func TestATieBetweenSpecsIsNotARelocation(t *testing.T) {
	st := &fakeStore{
		latest:  map[string]string{"33.108": "19.0.0"},
		clauses: map[string][]model.Clause{"33.108": {{ClausePath: "10.1", Heading: "x", Text: "y"}}},
		hits: []model.SearchHit{
			hit("26.346", "5.4A.5.2", "Create Session"),
			hit("29.274", "7.2.1", "Create Session Request"),
		},
	}
	f := auditOne(t, st, SentinelEvent{NF: "PGW", Event: "CREATE_SESSION", Spec: "33.108", Clause: "10.5.1.5.1"})
	if f.Verdict != VAmbiguous {
		t.Fatalf("verdict = %s, want %s", f.Verdict, VAmbiguous)
	}
	if !strings.Contains(f.Why, "26.346") || !strings.Contains(f.Why, "29.274") {
		t.Fatalf("the reason must name the rival specs, got %q", f.Why)
	}
}

// An event whose text really is nowhere must still be reported as absent:
// AMBIGUOUS must not become the new place where everything hides.
func TestAbsentIsStillAbsent(t *testing.T) {
	st := &fakeStore{
		latest:  map[string]string{"33.108": "19.0.0"},
		clauses: map[string][]model.Clause{"33.108": {{ClausePath: "10.1", Heading: "x", Text: "y"}}},
		hits:    []model.SearchHit{hit("29.002", "1.1", "something else entirely")},
	}
	f := auditOne(t, st, SentinelEvent{NF: "PTC", Event: "PTC_E2E_KEYS", Spec: "33.128", Clause: "7.5.2"})
	if f.Verdict != VNotFound {
		t.Fatalf("verdict = %s (%s), want %s", f.Verdict, f.Why, VNotFound)
	}
}
