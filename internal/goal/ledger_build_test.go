package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// writeJSONL writes one JSON object per line and returns the path.
func writeJSONL(t *testing.T, dir, name string, rows []any) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

type wlItem struct {
	ChunkID uint64 `json:"chunk_id"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

type ledRow struct {
	ChunkID uint64 `json:"chunk_id"`
	Hash    string `json:"hash"`
}

// TestLedgerDescribesAnotherBuild pins the check that stands between a rebuilt
// corpus and a silently wrong one.
//
// chunk_ids are positional: ingest assigns them sequentially, so a corpus rebuilt
// from scratch reuses the same numbers for different clauses. The embedder resumes
// on chunk_id and embed-io --import joins on chunk_id, so a ledger from another
// build attaches vectors computed from unrelated text — and every gate still
// passes, because every clause does have a vector.
//
// Measured 2026-09-02 between two ETSI builds of the SAME 11 821 documents:
// chunk_id 138 named "ETSI TS 101 671 v3.15.1 §10" in one and
// "ETSI EN 300 113-1 v1.3.1 §4" in the other.
func TestLedgerDescribesAnotherBuild(t *testing.T) {
	const id = "38067f8c6efe"
	dir := t.TempDir()

	// The work list this build exported.
	var wl []any
	for i := 1; i <= 400; i++ {
		wl = append(wl, wlItem{ChunkID: uint64(i), Heading: fmt.Sprintf("h%d", i), Text: fmt.Sprintf("clause text %d", i)})
	}
	worklist := writeJSONL(t, dir, "worklist.jsonl", wl)

	// A ledger written against THIS build: same ids, same text, so same hashes.
	var same []any
	for i := 1; i <= 400; i++ {
		same = append(same, ledRow{ChunkID: uint64(i), Hash: embed.ClauseHash(fmt.Sprintf("h%d", i), fmt.Sprintf("clause text %d", i), id)})
	}
	if moved, bad, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "same.jsonl", same), worklist, denseHash(id)); moved || bad != 0 {
		t.Errorf("a ledger from THIS build must not be archived: moved=%v disagree=%d checked=%d", moved, bad, n)
	}

	// A ledger written against ANOTHER build: the same ids, other text.
	var other []any
	for i := 1; i <= 400; i++ {
		other = append(other, ledRow{ChunkID: uint64(i), Hash: embed.ClauseHash("other", fmt.Sprintf("a different clause %d", i), id)})
	}
	moved, bad, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "other.jsonl", other), worklist, denseHash(id))
	if !moved {
		t.Errorf("a ledger from another build MUST be detected: disagree=%d checked=%d", bad, n)
	}

	// A handful of revised documents is NOT a renumbering, and must not throw away a
	// ledger that took hours of GPU.
	var revised []any
	for i := 1; i <= 400; i++ {
		text := fmt.Sprintf("clause text %d", i)
		if i <= 40 { // 10%: below the threshold
			text = "revised " + text
		}
		revised = append(revised, ledRow{ChunkID: uint64(i), Hash: embed.ClauseHash(fmt.Sprintf("h%d", i), text, id)})
	}
	if moved, bad, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "revised.jsonl", revised), worklist, denseHash(id)); moved {
		t.Errorf("10%% revised clauses is a normal update, not a renumbering: disagree=%d checked=%d", bad, n)
	}

	// Too few comparisons to conclude: say nothing rather than archive on noise.
	tiny := writeJSONL(t, dir, "tiny.jsonl", []any{ledRow{ChunkID: 1, Hash: "nope"}})
	if moved, _, n := ledgerDescribesAnotherBuild(tiny, worklist, denseHash(id)); moved || n >= ledgerSampleMin {
		t.Errorf("a sample of %d must not decide anything (moved=%v)", n, moved)
	}

	// A missing ledger is not a moved one.
	if moved, _, _ := ledgerDescribesAnotherBuild(filepath.Join(dir, "nope.jsonl"), worklist, denseHash(id)); moved {
		t.Error("a missing ledger must not report a moved id space")
	}
}

// denseHash is what the embed step passes: the dense ClauseHash for one identity.
func denseHash(id string) func(string, string) string {
	return func(heading, text string) string { return embed.ClauseHash(heading, text, id) }
}

// The SPARSE postings file names its hash field "h" and folds in no model identity,
// and it carried the same hazard with nothing to detect it: a posting line held a
// chunk_id and nothing else. The one check must read both shapes.
func TestLedgerCheckReadsTheSparseShapeToo(t *testing.T) {
	dir := t.TempDir()
	var wl []any
	for i := 1; i <= 400; i++ {
		wl = append(wl, wlItem{ChunkID: uint64(i), Heading: fmt.Sprintf("h%d", i), Text: fmt.Sprintf("clause text %d", i)})
	}
	worklist := writeJSONL(t, dir, "sparse-worklist.jsonl", wl)

	type sparseRow struct {
		ChunkID uint64 `json:"chunk_id"`
		H       string `json:"h"`
	}
	var mine, theirs []any
	for i := 1; i <= 400; i++ {
		mine = append(mine, sparseRow{ChunkID: uint64(i), H: sparseResumeHash(fmt.Sprintf("h%d", i), fmt.Sprintf("clause text %d", i))})
		theirs = append(theirs, sparseRow{ChunkID: uint64(i), H: sparseResumeHash("other", fmt.Sprintf("a different clause %d", i))})
	}
	if moved, bad, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "s-same.jsonl", mine), worklist, sparseResumeHash); moved {
		t.Errorf("sparse postings from THIS build must be kept: disagree=%d checked=%d", bad, n)
	}
	if moved, bad, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "s-other.jsonl", theirs), worklist, sparseResumeHash); !moved {
		t.Errorf("sparse postings from another build MUST be detected: disagree=%d checked=%d", bad, n)
	}
	// A pre-hash postings file carries no "h" at all: nothing to compare, so nothing
	// is concluded — the embedder's own resume check is what refuses to trust it.
	type oldRow struct {
		ChunkID uint64 `json:"chunk_id"`
	}
	var old []any
	for i := 1; i <= 400; i++ {
		old = append(old, oldRow{ChunkID: uint64(i)})
	}
	if moved, _, n := ledgerDescribesAnotherBuild(writeJSONL(t, dir, "s-old.jsonl", old), worklist, sparseResumeHash); moved || n != 0 {
		t.Errorf("a hash-less postings file gives no sample: moved=%v checked=%d", moved, n)
	}
}

// CROSS-RUNTIME GOLDEN, the other half of rust/embed-core's
// resume_hash_matches_the_go_side. The sparse postings file is written by Rust and
// checked here by Go; if the two drift, this check either throws away a good file
// every run or stops noticing a bad one. Both sides are pinned to this constant.
func TestSparseResumeHashMatchesTheRustSide(t *testing.T) {
	if got, want := sparseResumeHash("6 X1", "body"), "b76cec67248a1ec9"; got != want {
		t.Errorf("sparseResumeHash = %q, want %q (rust/embed-core resume_hash)", got, want)
	}
}
