package li

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject/li/asn1"
)

// openStore opens a fresh on-disk DuckDB store with the schema bootstrapped.
func openStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "shard.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	return st, ctx
}

func countLI(t *testing.T, st *store.Store, ctx context.Context, table, release string) int {
	t.Helper()
	var n int
	q := `SELECT count(*) FROM ` + table + ` WHERE spec_id='33.128' AND release=?`
	if err := st.DB().QueryRowContext(ctx, q, release).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPurgeClearsSubjectTables locks the purge contract (PR-2 part b): Purge must
// clear EVERY LI-owned table for the (spec, release) scope, and leave a sibling
// release untouched. PurgeSpecScope at the core knows none of these tables.
func TestPurgeClearsSubjectTables(t *testing.T) {
	st, ctx := openStore(t)

	for _, rel := range []string{"Rel-18", "Rel-19"} {
		if err := InsertEvents(st, rel, "r"+rel, []asn1.Event{
			{Interface: "X2", Name: "registration", Type: "AMFRegistration", Tag: 1, NF: "AMF", Domain: "5GC", Clause: "6.1", FieldCount: 2},
		}); err != nil {
			t.Fatal(err)
		}
		if err := InsertFields(st, rel, []asn1.Field{
			{Interface: "X2", EventName: "registration", FieldName: "imsi", Type: "IMSI", Tag: 0, Optional: false, Ordinal: 0},
		}); err != nil {
			t.Fatal(err)
		}
		if err := InsertNFClauses(st, rel, []asn1.NFClause{
			{NF: "AMF", Interface: "X2", Clause: "6.1"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := InsertASN1Types(st, specID, rel, []asn1.TypeDef{
			{Name: "AMFRegistration", Kind: "SEQUENCE"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := Purge(ctx, st, "Rel-18"); err != nil {
		t.Fatal(err)
	}

	tables := []string{"li_events", "li_event_fields", "li_nf_clauses", "asn1_types"}
	for _, tbl := range tables {
		if got := countLI(t, st, ctx, tbl, "Rel-18"); got != 0 {
			t.Errorf("%s Rel-18 not purged: count=%d", tbl, got)
		}
		if got := countLI(t, st, ctx, tbl, "Rel-19"); got != 1 {
			t.Errorf("%s Rel-19 collaterally purged: count=%d (want 1)", tbl, got)
		}
	}
}

// TestResumeRedoCorrectsAndDropsRows locks PR-2 parts (b)+(c) together on the
// LI tables: after a content change, the resume redo (Purge + re-insert) must
// (a) UPDATE a changed event's mutable attributes — INSERT OR REPLACE, not the
// old DO NOTHING which silently kept the stale tag/clause — and (b) DROP an
// event the corrected parse no longer emits.
func TestResumeRedoCorrectsAndDropsRows(t *testing.T) {
	st, ctx := openStore(t)
	const rel = "Rel-18"

	// First (stale) parse: FOO with tag=10/clause=7.1, plus BAR that the
	// corrected parse will drop.
	if err := InsertEvents(st, rel, "r18", []asn1.Event{
		{Interface: "X2", Name: "FOO", Type: "FooT", Tag: 10, NF: "AMF", Domain: "5GC", Clause: "7.1", FieldCount: 1},
		{Interface: "X2", Name: "BAR", Type: "BarT", Tag: 11, NF: "AMF", Domain: "5GC", Clause: "7.9", FieldCount: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if err := InsertASN1Types(st, specID, rel, []asn1.TypeDef{
		{Name: "FooT", Kind: "SEQUENCE"},
		{Name: "GhostType", Kind: "SEQUENCE"}, // dropped by the corrected parse
	}); err != nil {
		t.Fatal(err)
	}

	// Resume redo: Purge the scope, then re-ingest the corrected parse — FOO with
	// tag=20/clause=7.2, no BAR, no GhostType.
	if err := Purge(ctx, st, rel); err != nil {
		t.Fatal(err)
	}
	if err := InsertEvents(st, rel, "r18", []asn1.Event{
		{Interface: "X2", Name: "FOO", Type: "FooT", Tag: 20, NF: "AMF", Domain: "5GC", Clause: "7.2", FieldCount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := InsertASN1Types(st, specID, rel, []asn1.TypeDef{
		{Name: "FooT", Kind: "SEQUENCE"},
	}); err != nil {
		t.Fatal(err)
	}

	// FOO corrected (tag=20, clause=7.2), exactly once.
	var tag int
	var clause string
	var nFoo int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM li_events WHERE spec_id='33.128' AND release=? AND event_name='FOO'`, rel).Scan(&nFoo); err != nil {
		t.Fatal(err)
	}
	if nFoo != 1 {
		t.Fatalf("FOO count=%d, want exactly 1", nFoo)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT asn1_tag, spec_clause FROM li_events WHERE spec_id='33.128' AND release=? AND event_name='FOO'`, rel).Scan(&tag, &clause); err != nil {
		t.Fatal(err)
	}
	if tag != 20 || clause != "7.2" {
		t.Errorf("FOO not corrected on redo: tag=%d clause=%q (want 20, 7.2 — DO NOTHING would keep 10/7.1)", tag, clause)
	}
	// BAR dropped.
	if got := func() int {
		var n int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT count(*) FROM li_events WHERE spec_id='33.128' AND release=? AND event_name='BAR'`, rel).Scan(&n)
		return n
	}(); got != 0 {
		t.Errorf("BAR should be gone after the corrected redo, count=%d", got)
	}
	// GhostType dropped from asn1_types (INSERT OR REPLACE alone can't remove it).
	if got := func() int {
		var n int
		_ = st.DB().QueryRowContext(ctx,
			`SELECT count(*) FROM asn1_types WHERE spec_id='33.128' AND release=? AND type_name='GhostType'`, rel).Scan(&n)
		return n
	}(); got != 0 {
		t.Errorf("GhostType should be gone after Purge+redo, count=%d", got)
	}
}
