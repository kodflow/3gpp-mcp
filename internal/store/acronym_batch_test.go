package store

import (
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// THE SWAP MUST REPLACE WHAT THE BATCH NAMES AND TOUCH NOTHING IT DOES NOT OWN.
//
// The glossary write used to run one `INSERT … ON CONFLICT` per row inside the
// transaction, which DuckDB checks against the transaction's own uncommitted
// rows — quadratic in the batch. At the 679 rows six specs produced it was
// invisible; at the 30 000 a corpus-wide sweep produces it did not finish (661 s
// of CPU, ZERO bytes written). It now stages into a constraint-free TEMP table
// and swaps set-based, and this test pins that the rewrite kept the semantics:
// a named key is REPLACED — a TS 21.905 row included, because a spec's own
// declaration outranks it — and a row the seed does not own SURVIVES.
func TestTheGlossaryBatchReplacesOnlyWhatItNames(t *testing.T) {
	db := filepath.Join(t.TempDir(), "c.duckdb")
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// A row nothing later mentions, and one the batch will replace — both
	// TS 21.905's, written the way the Rust ingest writes them: one at a time,
	// stamped with the series.
	for _, a := range []model.Acronym{
		{Term: "UNTOUCHED", Expansion: "Left Exactly As It Was", SourceSeries: "21"},
		{Term: "AMF", Expansion: "ATM Mapping Function", SourceSeries: "21"},
	} {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}

	// The batch names the AMF row's key and adds a new one.
	batch := []model.Acronym{
		{Term: "AMF", Expansion: "ATM Mapping Function", SourceSeries: "23.501", DeclaredBy: 74},
		{Term: "SMF", Expansion: "Session Management Function", SourceSeries: "23.501", DeclaredBy: 84},
	}
	diff, err := s.ReplaceSeededAcronyms(batch, cleared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Changed() {
		t.Error("a batch that rewrites provenance reported no change")
	}

	got := map[string]model.Acronym{}
	rows, err := s.acronymIndex()
	if err != nil {
		t.Fatal(err)
	}
	for k, a := range rows {
		got[k.term] = a
	}

	if a, ok := got["UNTOUCHED"]; !ok || a.SourceSeries != "21" {
		t.Errorf("a row the batch never named was lost or rewritten: %+v (present=%v)", a, ok)
	}
	if a := got["AMF"]; a.SourceSeries != "23.501" || a.DeclaredBy != 74 {
		t.Errorf("the named key was not replaced: source=%q declared_by=%d", a.SourceSeries, a.DeclaredBy)
	}
	if a := got["SMF"]; a.SourceSeries != "23.501" {
		t.Errorf("a new key was not written: %+v", a)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 rows, got %d — the swap dropped or duplicated", len(got))
	}

	// AND IT IS IDEMPOTENT: writing the same batch again must report no change,
	// because one changed byte in a 23 GB corpus is a new layer digest and a push.
	if diff, err := s.ReplaceSeededAcronyms(batch, cleared, nil); err != nil {
		t.Fatal(err)
	} else if diff.Changed() {
		t.Errorf("re-writing an identical batch reported a change (%+v) — the corpus would be re-pushed", diff)
	}
}
