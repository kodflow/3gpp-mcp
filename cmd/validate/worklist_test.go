package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestRequireWorklistReconcilesThreeSets pins the ETSI arm's answer to the
// question anchorcheck answers on the 3GPP arm: does the corpus hold what the
// pipeline decided it held?
//
// The three states are NOT two. A hole with a register present is a real failure;
// the same hole with no register is UNVERIFIED, because a corpus built before the
// register existed cannot produce one retroactively and failing there would block
// the supported path for something the operator cannot fix — the call
// reportAnchorHoles already makes about the 56 known 3GPP holes.
func TestRequireWorklistReconcilesThreeSets(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "etsi.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	// The corpus holds two of the three work-list versions.
	for _, v := range []model.SpecVersion{
		{SpecID: "ETSI TS 102 221", Release: "ETSI", Version: "11.0.0"},
		{SpecID: "ETSI TR 103 101", Release: "ETSI", Version: "1.1.1"},
	} {
		_ = st.UpsertSpec(model.Spec{SpecID: v.SpecID, DocType: "TS"})
		if err := st.UpsertVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	wl := filepath.Join(dir, "worklist.tsv")
	// The TS/TR pair sharing a number is deliberate: 103 101 is a TR and the TS
	// tree 404s on it, so a key that dropped the type would let one excuse the other.
	if err := os.WriteFile(wl, []byte(
		"102 221\thttps://x/a.pdf\t11.0.0\tTS\n"+
			"103 101\thttps://x/b.pdf\t1.1.1\tTR\n"+
			"102 894-2\thttps://x/c.pdf\t2.5.1\tTS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(absences string) check {
		res := result{OK: true}
		db, err := store.OpenReadOnly(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		checkWorklist(ctx, db, &res, checkCfg{db: dbPath, worklist: wl, absences: absences})
		if len(res.Checks) != 1 {
			t.Fatalf("want exactly one check, got %d", len(res.Checks))
		}
		return res.Checks[0]
	}

	// 1. No register: the hole is reported, named, and does NOT fail the build.
	got := run(filepath.Join(dir, "does-not-exist.tsv"))
	if !got.Pass {
		t.Errorf("a hole with no register must not fail the gate: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "UNVERIFIED") || !strings.Contains(got.Detail, "ETSI TS 102 894-2 v2.5.1") {
		t.Errorf("the unverified report must say so and name the hole: %s", got.Detail)
	}

	// 2. The register explains it: green, and it says how many it excused.
	abs := filepath.Join(dir, "absences.tsv")
	if err := os.WriteFile(abs, []byte("102 894-2\t2.5.1\tTS\tno-text-layer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = run(abs)
	if !got.Pass || !strings.Contains(got.Detail, "1 excused") {
		t.Errorf("an explained absence must pass and be counted: pass=%v %s", got.Pass, got.Detail)
	}

	// 3. A register that does NOT cover the hole is a real failure. This is the
	//    whole point: without it, a deliverable whose rows never reached the corpus
	//    is invisible, because every later step trusts the same decision.
	if err := os.WriteFile(abs, []byte("199 999\t9.9.9\tTS\tno-text-layer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = run(abs)
	if got.Pass {
		t.Errorf("an unexplained hole must fail the gate: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "ETSI TS 102 894-2 v2.5.1") {
		t.Errorf("the failure must name the hole: %s", got.Detail)
	}

	// 4. The document type is part of the key: a TR must not excuse a missing TS.
	if err := os.WriteFile(abs, []byte("102 894-2\t2.5.1\tTR\tno-text-layer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got = run(abs); got.Pass {
		t.Errorf("a TR entry excused a missing TS — the type is not in the key: %s", got.Detail)
	}
}
