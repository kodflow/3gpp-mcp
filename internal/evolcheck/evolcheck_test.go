package evolcheck

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestNamesTermIsTokenNotSubstring locks the property the whole citation check
// rests on: an edge is credited only when the cited clause names its target as a
// WORD. A substring test would credit "PCF" to a clause that only ever says
// "PCFICH" — the check would then pass on precisely the mis-anchored citations it
// exists to catch, which is how the previous seed shipped three of them.
func TestNamesTermIsTokenNotSubstring(t *testing.T) {
	cases := []struct {
		name string
		body string
		term string
		want bool
	}{
		{"plain mention", "The AMF handles registration.", "AMF", true},
		{"start of text", "AMF is the access and mobility function.", "AMF", true},
		{"end of text", "…registration is handled by the AMF", "AMF", true},
		{"parenthesised", "the Policy Control Function (PCF) decides", "PCF", true},
		{"comma separated", "AMF, SMF and UPF", "SMF", true},
		{"hyphenated compound counts", "the AMF-set holds several AMFs", "AMF", true},

		// The failures that matter.
		{"substring of a longer token", "the PCFICH carries the indicator", "PCF", false},
		{"substring inside a word", "SAMFLOW is not a network function", "AMF", false},
		{"absent entirely", "Service-based interfaces between NFs.", "gNB", false},

		// Multi-word terms survive a line break in the extracted text.
		{"two-word term, single space", "the TSN AF exposes the bridge", "TSN AF", true},
		{"two-word term, newline between", "the TSN\nAF exposes the bridge", "TSN AF", true},
		{"two-word term absent", "the AF exposes the bridge", "TSN AF", false},

		// Case-insensitive: the extracted heading may be title-cased.
		{"case insensitive", "The Unified Data Management (udm) stores…", "UDM", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NamesTerm(tc.body, tc.term); got != tc.want {
				t.Errorf("NamesTerm(%q, %q) = %v, want %v", tc.body, tc.term, got, tc.want)
			}
		})
	}
}

// TestNamesTermEmptyTermIsVacuouslyTrue — the "new in 5G" edges carry no
// from-term, and it is the TO-term that gets checked. An empty term must not be
// reported as a failure, or every such edge would warn forever.
func TestNamesTermEmptyTermIsVacuouslyTrue(t *testing.T) {
	if !NamesTerm("anything at all", "") {
		t.Error("an empty term must not count as unnamed")
	}
	if !NamesTerm("anything at all", "   ") {
		t.Error("a whitespace-only term must not count as unnamed")
	}
}

// TestDescribeRendersTheNewIn5GEdges keeps the log line readable: an edge with no
// predecessor prints as "(new in 5G) -> DCCF", not as " -> DCCF".
func TestDescribeRendersTheNewIn5GEdges(t *testing.T) {
	if got, want := Describe(model.Evolution{FromTerm: "", ToTerm: "DCCF"}), "(new in 5G) -> DCCF"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
	if got, want := Describe(model.Evolution{FromTerm: "MME", ToTerm: "AMF"}), "MME -> AMF"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

// fakeCorpus is a ClauseSource over a literal clause table, so Verify can be
// tested without a DuckDB.
type fakeCorpus struct {
	version string
	// clauses maps a clause path to its body. GetClauses is a PREFIX lookup, as
	// the real store's is — which is the behaviour under test.
	clauses map[string]string
	missing bool // LatestVersion reports the spec as absent
}

func (f fakeCorpus) LatestVersion(_ context.Context, _ string) (string, string, bool, error) {
	if f.missing {
		return "", "", false, nil
	}
	return "Rel-20", f.version, true, nil
}

func (f fakeCorpus) GetClauses(_ context.Context, specID, _, prefix string) ([]model.Clause, error) {
	var out []model.Clause
	for path, text := range f.clauses {
		if strings.HasPrefix(path, prefix) {
			out = append(out, model.Clause{SpecID: specID, ClausePath: path, Heading: "h " + path, Text: text})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClausePath < out[j].ClausePath })
	return out, nil
}

// TestVerifyUsesTheEXACTClauseNotThePrefix is the behaviour this package exists
// for, and the one nothing covered.
//
// GetClauses takes a PREFIX, so asking for "6.2.3" also returns "6.2.30",
// "6.2.31" and so on. If Verify concatenated everything it got back, an edge
// citing §6.2.3 would be credited by text that lives in §6.2.30 — a citation that
// looks checkable, lands on the wrong clause, and passes the check meant to catch
// exactly that. Dropping the exact-path filter must fail here.
func TestVerifyUsesTheEXACTClauseNotThePrefix(t *testing.T) {
	src := fakeCorpus{
		version: "20.2.0",
		clauses: map[string]string{
			"6.2.3":  "The User Plane Function is described here.", // names UPF? no
			"6.2.30": "The AMF is described at length in this neighbouring clause.",
		},
	}
	seed := []model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", JustificationSpec: "23.501", JustificationClause: "6.2.3"},
	}
	res, err := Verify(context.Background(), src, seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("clause 6.2.3 exists; Missing = %v", res.Missing)
	}
	if len(res.Unnamed) != 1 {
		t.Fatalf("6.2.3 never names AMF (only its prefix-sibling 6.2.30 does); Unnamed = %v, want 1 edge", res.Unnamed)
	}
}

// TestVerifyReportsAnAbsentSpecAsMissing — an edge citing a spec the corpus does
// not carry has nothing backing it, and that is the FATAL half of the grading.
func TestVerifyReportsAnAbsentSpecAsMissing(t *testing.T) {
	seed := []model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", JustificationSpec: "23.501", JustificationClause: "6.2.1"},
	}
	res, err := Verify(context.Background(), fakeCorpus{missing: true}, seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) != 1 || res.OK() {
		t.Errorf("an absent spec must be Missing and not OK; got Missing=%d OK=%v", len(res.Missing), res.OK())
	}
}

// TestVerifyResolvesEachClauseOnce — the seed cites the same clause from several
// edges, and each resolution is a store round trip. Caching them is not an
// optimisation detail: on the real corpus this is 45 edges over far fewer
// distinct clauses.
func TestVerifyResolvesEachClauseOnce(t *testing.T) {
	src := &countingCorpus{fakeCorpus: fakeCorpus{
		version: "20.2.0",
		clauses: map[string]string{"6.2.1": "AMF and SMF are both named here."},
	}}
	seed := []model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", JustificationSpec: "23.501", JustificationClause: "6.2.1"},
		{FromTerm: "SGSN", ToTerm: "SMF", JustificationSpec: "23.501", JustificationClause: "6.2.1"},
	}
	if _, err := Verify(context.Background(), src, seed); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 {
		t.Errorf("GetClauses called %d time(s) for one distinct clause, want 1", src.calls)
	}
}

type countingCorpus struct {
	fakeCorpus
	calls int
}

func (c *countingCorpus) GetClauses(ctx context.Context, specID, version, prefix string) ([]model.Clause, error) {
	c.calls++
	return c.fakeCorpus.GetClauses(ctx, specID, version, prefix)
}
