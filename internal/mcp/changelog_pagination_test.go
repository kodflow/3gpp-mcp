package mcp

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// pagedFixture is a 3GPP spec with 250 records over only FIVE to_versions, so
// that 50 records tie on the store's sort key — the case where an order that is
// not total makes pages overlap and skip. Rows are inserted in a shuffled order
// chosen by seed, so two fixtures hold the same records in different physical
// orders.
func pagedFixture(t *testing.T, seed int64) (*client.Client, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	var rows []model.Change
	for i := 0; i < 250; i++ {
		v := i % 5 // 18.0.0 .. 18.4.0, fifty records each
		rows = append(rows, model.Change{
			CRNumber: fmt.Sprintf("%04d", i), SpecID: "23.501",
			FromVersion: fmt.Sprintf("17.%d.0", v), ToVersion: fmt.Sprintf("18.%d.0", v),
			Summary: "change " + fmt.Sprint(i),
		})
	}
	for v := 0; v < 5; v++ {
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: fmt.Sprintf("18.%d.0", v)})
	}
	rand.New(rand.NewSource(seed)).Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	if err := st.InsertChanges(rows); err != nil {
		t.Fatal(err)
	}
	// A second spec small enough to fit in one page.
	_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.1.0"})
	_ = st.InsertChanges([]model.Change{{CRNumber: "0001", SpecID: "24.501", FromVersion: "18.0.0", ToVersion: "18.1.0", Summary: "x"}})

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
	return c, ctx
}

// walkPages follows next_cursor from the first page to the last and returns every
// record's CR number in the order served.
func walkPages(t *testing.T, c *client.Client, ctx context.Context, args map[string]any) (crs []string, pages int, total float64) {
	t.Helper()
	cursor := ""
	for pages = 1; pages < 100; pages++ {
		a := map[string]any{}
		for k, v := range args {
			a[k] = v
		}
		if cursor != "" {
			a["cursor"] = cursor
		}
		out := call(t, c, ctx, "get_changelog", a)
		total, _ = out["total"].(float64)
		crs = append(crs, changeNumbers(t, out)...)
		next, _ := out["next_cursor"].(string)
		if next == "" {
			return crs, pages, total
		}
		cursor = next
	}
	t.Fatal("pagination never ended")
	return nil, 0, 0
}

// A CHANGELOG IS SERVED IN PAGES, AND THE PAGES ADD UP TO IT. TS 23.501 was one
// 1 013 076-byte answer on the published corpus. The first page must say it is
// one, and following next_cursor must yield every record exactly once, in order.
//
// Falsified: without the slice, the first call returns all 250; without the page
// note, it returns 100 and does not say so; with the cursor ignored, the walk
// serves page one forever and never ends.
func TestTheChangelogIsPagedAndThePagesAddUp(t *testing.T) {
	c, ctx := pagedFixture(t, 1)

	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501"})
	if n, _ := out["count"].(float64); n != 100 {
		t.Errorf("first page count = %v, want the default 100", out["count"])
	}
	if n, _ := out["total"].(float64); n != 250 {
		t.Errorf("total = %v, want 250", out["total"])
	}
	if n := noteOf(out); !strings.Contains(n, "250 records match; this page returns records 1-100") || !strings.Contains(n, "cursor=") {
		t.Errorf("a truncated answer must say so and say how to continue; note = %q", n)
	}

	crs, pages, total := walkPages(t, c, ctx, map[string]any{"spec_id": "23.501"})
	if pages != 3 || total != 250 || len(crs) != 250 {
		t.Fatalf("walk: %d pages, total %v, %d records — want 3, 250, 250", pages, total, len(crs))
	}
	seen := map[string]bool{}
	for _, cr := range crs {
		if seen[cr] {
			t.Fatalf("record %s served twice: the pages overlap", cr)
		}
		seen[cr] = true
	}
	// In order: to_version, then CR number.
	for i := 1; i < len(crs); i++ {
		a, b := crs[i-1], crs[i]
		// Atoi, not Sscan: Sscan reads "0015" as OCTAL.
		ai, _ := strconv.Atoi(a)
		bi, _ := strconv.Atoi(b)
		if ai%5 > bi%5 || (ai%5 == bi%5 && a > b) {
			t.Fatalf("order broken at %d: %s then %s", i, a, b)
		}
	}
}

// THE ORDER DOES NOT DEPEND ON HOW THE ROWS SIT ON DISK. Fifty records tie on
// to_version; stored in two different physical orders, the same page must hold
// the same records, or offset 100 means different things on two builds.
//
// Falsified: without sortChanges, the two fixtures serve different second pages.
func TestAPageIsTheSameWhateverThePhysicalOrder(t *testing.T) {
	c1, ctx1 := pagedFixture(t, 1)
	c2, ctx2 := pagedFixture(t, 2)
	a, _, _ := walkPages(t, c1, ctx1, map[string]any{"spec_id": "23.501", "limit": 70})
	b, _, _ := walkPages(t, c2, ctx2, map[string]any{"spec_id": "23.501", "limit": 70})
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Error("the same records in a different physical order were paged differently")
	}
}

// BOUNDS FIRST, THEN PAGES, AND A CURSOR BELONGS TO ITS QUESTION.
//
// Falsified: with the query hash ignoring the bounds, the cursor minted for the
// unbounded history is accepted against the 18.1.0 range.
func TestBoundsApplyBeforePagingAndCursorsDoNotCrossQueries(t *testing.T) {
	c, ctx := pagedFixture(t, 1)
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "from_release": "18.1.0", "to_release": "18.1.0"})
	if n, _ := out["total"].(float64); n != 50 {
		t.Errorf("18.1.0..18.1.0 total = %v, want the 50 records of that version", out["total"])
	}
	if _, more := out["next_cursor"]; more {
		t.Error("50 records fit in the default page; there is no next page")
	}

	first := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501"})
	cur, _ := first["next_cursor"].(string)
	if cur == "" {
		t.Fatal("no cursor to replay")
	}
	var r mcpgo.CallToolRequest
	r.Params.Name = "get_changelog"
	r.Params.Arguments = map[string]any{"spec_id": "23.501", "from_release": "18.1.0", "to_release": "18.1.0", "cursor": cur}
	res, err := c.CallTool(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("a cursor from the unbounded history was accepted against a bounded query")
	}
}

// The page size is the caller's, within a ceiling that is SAID; a spec that fits
// in one page answers exactly as it did before paging existed.
//
// Falsified: without the clamp note, the cap is silent (the numeric clamp itself
// is pinned by TestChangelogLimit: 501 -> 500).
func TestTheLimitIsCappedAloudAndSmallSpecsAreUnchanged(t *testing.T) {
	c, ctx := pagedFixture(t, 1)
	out := call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "limit": 1000})
	if n, _ := out["count"].(float64); n != 250 {
		t.Errorf("limit 1000 over 250 records: count = %v, want 250 (capped at %d, which is more)", out["count"], changelogMaxLimit)
	}
	if n := noteOf(out); !strings.Contains(n, "limit capped at 500") {
		t.Errorf("a clamped limit must be said; note = %q", n)
	}
	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "23.501", "limit": 10})
	if n, _ := out["count"].(float64); n != 10 {
		t.Errorf("limit 10: count = %v", out["count"])
	}

	out = call(t, c, ctx, "get_changelog", map[string]any{"spec_id": "24.501"})
	if n, _ := out["count"].(float64); n != 1 || out["total"] != float64(1) {
		t.Errorf("a one-record spec: count=%v total=%v", out["count"], out["total"])
	}
	if _, more := out["next_cursor"]; more {
		t.Error("a spec that fits in one page must carry no next_cursor")
	}
	if strings.Contains(noteOf(out), "this page returns") {
		t.Errorf("a whole answer must not be described as a page: %q", noteOf(out))
	}
}

func TestChangelogLimit(t *testing.T) {
	for _, c := range []struct {
		in, want int
		clamped  bool
	}{{0, 100, false}, {-3, 100, false}, {1, 1, false}, {500, 500, false}, {501, 500, true}} {
		got, cl := changelogLimit(c.in)
		if got != c.want || cl != c.clamped {
			t.Errorf("changelogLimit(%d) = %d,%v want %d,%v", c.in, got, cl, c.want, c.clamped)
		}
	}
}

// THE ORDER THE NOTE PROMISES IS THE ORDER SERVED (Qodo, #341). The note, the tool
// description and CLAUDE.md say "to_version, then CR number"; the comparator used
// to put from_version second, so two CRs landing in the same version from
// different starting versions came out against the promised order.
//
// Falsified: with from_version compared before the CR number, CR 0010 (from
// 17.9.0) sorts ahead of CR 0009 (from 18.0.0).
func TestSortChangesKeepsThePromisedOrder(t *testing.T) {
	cs := []model.Change{
		{CRNumber: "0010", FromVersion: "17.9.0", ToVersion: "18.1.0"},
		{CRNumber: "0009", FromVersion: "18.0.0", ToVersion: "18.1.0"},
		{CRNumber: "0001", FromVersion: "9.0.0", ToVersion: "10.0.0"},
		{CRNumber: "0100", FromVersion: "18.0.0", ToVersion: "9.0.0"},
	}
	sortChanges(cs)
	var got []string
	for _, c := range cs {
		got = append(got, c.CRNumber)
	}
	if strings.Join(got, ",") != "0100,0001,0009,0010" {
		t.Errorf("order = %v, want to_version numerically (9.0.0 < 10.0.0 < 18.1.0), then CR number", got)
	}
}

// The shared page cutter: a window past the end is an empty last page, never a
// crash, and a cursor bound to another query is refused.
func TestPaginateCutsWithinTheListAndRefusesAForeignCursor(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}
	page, start, next, err := paginate(items, "", "q", 2)
	if err != nil || start != 0 || len(page) != 2 || next == "" {
		t.Fatalf("first page: %v %d %q %v", page, start, next, err)
	}
	page, start, next, err = paginate(items, encodeCursor(pageCursor{Offset: 4, QHash: "q"}), "q", 2)
	if err != nil || start != 4 || len(page) != 1 || next != "" {
		t.Fatalf("last page: %v %d %q %v", page, start, next, err)
	}
	page, _, next, err = paginate(items, encodeCursor(pageCursor{Offset: 9, QHash: "q"}), "q", 2)
	if err != nil || len(page) != 0 || next != "" {
		t.Fatalf("past the end: %v %q %v", page, next, err)
	}
	if _, _, _, err = paginate(items, encodeCursor(pageCursor{Offset: 2, QHash: "other"}), "q", 2); err == nil {
		t.Fatal("a cursor minted for another query was accepted")
	}
}
