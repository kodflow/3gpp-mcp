package glossaryseed

import (
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// THE READER ANCHORS WHERE THE WRITER DOES. extract_acronyms reads the region of
// any heading that CONTAINS "abbreviation"; generalRegion used to require the
// exact heading "Abbreviations". A TS 21.905 issue that moved its list under
// "Definitions and abbreviations" would then yield keys the writer stores and
// the reader never sees — and since the retirement, the reader's set decides
// which "21" rows are deleted. (Found by review on #337; identical on all 16
// stored versions, which each carry the one heading "4 Abbreviations".)
func TestTheReaderAnchorsOnTheWritersHeadingTest(t *testing.T) {
	c := func(chunk uint64, path, heading string) model.Clause {
		return model.Clause{ChunkID: chunk, SpecID: ts21905, Version: "20.0.0", Release: "Rel-20",
			ClausePath: path, Heading: heading}
	}
	got := generalRegion([]model.Clause{
		c(1, "3", "Definitions and abbreviations"),
		c(2, "", "A"),
		c(3, "3.1", "Symbols"),
		c(4, "4", "Architecture"),
	})
	var headings []string
	for _, cl := range got.clauses {
		headings = append(headings, cl.Heading)
	}
	if strings.Join(headings, ",") != "Definitions and abbreviations,A,Symbols" {
		t.Errorf("region = %v, want the heading that contains \"abbreviation\", its unnumbered and "+
			"numbered clauses, and not clause 4 — what the Rust writer reads", headings)
	}
}

// A RETIREMENT IS A DELETION, AND THE GUARD COUNTS IT. Retiring a TS 21.905 row
// rests on one read of one document; a read that cleared the floor while losing a
// letter clause would retire every row of that letter at once. The bound is what
// stops that from happening in bulk — so a retirement must count against it
// exactly as a removal does, and be named in the refusal.
func TestARetirementIsADeletionTheGuardCounts(t *testing.T) {
	rows := func(n int) []model.Acronym { return make([]model.Acronym, n) }
	bound := removalBound(100) // 53: the floor binds below 5 300 seeded rows

	if why := massRemoval(store.GlossaryDiff{Owned: 100, Retired: rows(bound)}); why != "" {
		t.Errorf("retiring exactly the bound (%d) was refused: %s", bound, why)
	}
	why := massRemoval(store.GlossaryDiff{Owned: 100, Removed: rows(1), Retired: rows(bound)})
	if !strings.Contains(why, "retire 53 of TS 21.905's") || !strings.Contains(why, "above the bound of 53") {
		t.Errorf("one removal plus %d retirements (%d deletions) against a bound of %d was not refused "+
			"with its numbers: %q", bound, bound+1, bound, why)
	}
}
