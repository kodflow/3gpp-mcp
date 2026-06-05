package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadMergedIndex: comma-separated ledgers merge with the highest version
// winning, and a missing file is skipped (not fatal).
func TestLoadMergedIndex(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	if err := os.WriteFile(a, []byte(`{"21.100|Rel-19":"19.5.0","30.531|Rel-18":"18.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`{"21.100|Rel-19":"19.6.0","21.802|Rel-20":"1.1.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := loadMergedIndex(a + "," + b + "," + filepath.Join(dir, "missing.json"))
	if m["21.100|Rel-19"] != "19.6.0" { // higher of 19.5.0 / 19.6.0
		t.Errorf("merge must keep highest version: %q", m["21.100|Rel-19"])
	}
	if m["30.531|Rel-18"] != "18.0.0" || m["21.802|Rel-20"] != "1.1.1" {
		t.Errorf("merge dropped a key: %v", m)
	}
}

// TestEmitDraftLedgerSelectsDraftsOnly: the emitted ledger is valid JSON holding
// exactly the in-scope DRAFT keys (version major < 3) — not the frozen ones — so
// merging it into --absent-index silences only the draft re-flag noise.
func TestEmitDraftLedgerSelectsDraftsOnly(t *testing.T) {
	site := map[string]string{
		"21.802|Rel-20": "1.1.1",  // draft (major 1) → ledgered
		"21.919|Rel-19": "2.0.0",  // draft (major 2) → ledgered
		"23.501|Rel-19": "19.6.0", // frozen (major 19) → NOT ledgered
		"24.008|Rel-4":  "4.8.0",  // in-scope, not a draft → NOT ledgered
	}
	out := filepath.Join(t.TempDir(), "drafts.json")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	emitDraftLedger(site, major("Rel-99"), "")
	os.Stdout = old
	_ = f.Close()

	m := loadIndex(out) // must be valid JSON
	if len(m) != 2 {
		t.Fatalf("draft ledger should hold exactly the 2 draft keys, got %v", m)
	}
	if m["21.802|Rel-20"] != "1.1.1" || m["21.919|Rel-19"] != "2.0.0" {
		t.Errorf("wrong draft keys: %v", m)
	}
	if _, ok := m["23.501|Rel-19"]; ok {
		t.Error("frozen key must not be in the draft ledger")
	}
}

// TestDraftLedgerStopsReflag proves the end-to-end effect: a draft key merged into
// `have` at the site version stops the series re-flagging; a newer site draft
// re-flags (it may have advanced and become fetchable).
func TestDraftLedgerStopsReflag(t *testing.T) {
	floor := major("Rel-99")
	site := map[string]string{"21.802|Rel-20": "1.1.1"}

	have := map[string]string{"21.802|Rel-20": "1.1.1"} // draft-ledgered at same version
	if got := deltaSeries(site, have, floor, false); len(got) != 0 {
		t.Fatalf("draft-ledgered-at-same-version must not flag; got %v", got)
	}
	site["21.802|Rel-20"] = "1.2.0" // 3GPP advanced the draft
	if got := deltaSeries(site, have, floor, false); !got["21"] {
		t.Fatalf("a newer draft version must re-flag; got %v", got)
	}
}
