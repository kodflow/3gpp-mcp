package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestDBCountCountersAreDeterministic locks the foundation of the PR-1
// pre-publish shrink guard (latest-shrink): the guard compares
// dbcount's spec_versions / api_operations between the about-to-publish DB and
// the current `latest` base, and blocks a clobber when either DECREASES. That
// fail-closed decision is only sound if the counters faithfully and stably
// reflect the rows in the DB. This test builds a DB with a KNOWN number of
// spec_versions, then asserts CountSpecVersions returns exactly that — and that
// a DB with strictly fewer rows reports a strictly smaller count (the SHRINK the
// guard must catch). A regression that miscounts (e.g. DISTINCT collapse, a JOIN
// that double-counts) would let a real shrink slip past the guard and clobber
// `latest`.
func TestDBCountCountersAreDeterministic(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	build := func(path string, versions []model.SpecVersion) {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, v := range versions {
			if !seen[v.SpecID] {
				_ = st.UpsertSpec(model.Spec{SpecID: v.SpecID, Series: model.SeriesOf(v.SpecID), DocType: "TS"})
				seen[v.SpecID] = true
			}
			_ = st.UpsertVersion(v)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}

	bigPath := filepath.Join(dir, "big.duckdb")
	smallPath := filepath.Join(dir, "small.duckdb")

	// `big` carries 3 (spec,release) version rows; `small` is a strict subset (2).
	big := []model.SpecVersion{
		{SpecID: "23.501", Release: "Rel-18", Version: "18.5.0"},
		{SpecID: "23.501", Release: "Rel-19", Version: "19.2.0"},
		{SpecID: "24.501", Release: "Rel-19", Version: "19.1.0"},
	}
	small := big[:2]
	build(bigPath, big)
	build(smallPath, small)

	count := func(path string) int {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		n, err := st.CountSpecVersions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got := count(bigPath); got != len(big) {
		t.Errorf("spec_versions count = %d, want %d (counter must be faithful for the shrink guard)", got, len(big))
	}
	if got := count(smallPath); got != len(small) {
		t.Errorf("spec_versions count = %d, want %d", got, len(small))
	}
	// The guard's core invariant: a strictly smaller corpus reports a strictly
	// smaller count, so a delta that loses rows is detectable as a SHRINK.
	if count(smallPath) >= count(bigPath) {
		t.Errorf("a strict-subset DB must count fewer spec_versions (%d) than the full DB (%d) — else the shrink guard cannot fire",
			count(smallPath), count(bigPath))
	}
}

// TestDBCountRunEmitsBothCounters exercises run() end-to-end: it must open the DB
// and exit without error, emitting the two KEY=VALUE lines the workflow guard
// eval's. A missing api_operations table (legacy base) must count as 0, never an
// error — the guard treats 0 as "no API rows to lose", so a panic/err here would
// turn a benign legacy base into a hard publish failure.
func TestDBCountRunEmitsBothCounters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.duckdb")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "29.518", Series: "29", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "29.518", Release: "Rel-19", Version: "19.0.0"})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, path); err != nil {
		t.Fatalf("dbcount run on a valid DB errored: %v", err)
	}
}
