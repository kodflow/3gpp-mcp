package glossaryseed

import (
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

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
