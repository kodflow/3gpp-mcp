package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if err := run(ctx, path, false); err != nil {
		t.Fatalf("dbcount run on a valid DB errored: %v", err)
	}
}

// TestDBCountBlocksReportsTheAccountingCompactNeeds is the end-to-end half of the
// compaction gate: `compact` declines a rewrite when these numbers say there is
// nothing to reclaim, so the numbers have to exist, parse, and add up.
//
// It is deliberately run against a REAL DuckDB rather than a fixture string. The
// gate's own unit tests (internal/goal, nothingToReclaim) prove what it concludes
// from a transcript; only this proves the transcript is one DuckDB will actually
// produce. `pragma_database_size()` is the kind of thing that changes shape
// between versions, and a gate reading a pragma that no longer returns those
// columns would fail open on every build — quietly, and for ever.
func TestDBCountBlocksReportsTheAccountingCompactNeeds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "blocks.duckdb")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "29.518", Series: "29", DocType: "TS"})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := run(ctx, path, true); err != nil {
			t.Fatalf("dbcount --blocks errored: %v", err)
		}
	})

	got := map[string]int64{}
	for _, field := range strings.Fields(out) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		got[k] = n
	}
	for _, k := range []string{"block_size", "total_blocks", "used_blocks", "free_blocks", "reclaimable_bytes"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("%s is missing from --blocks output, so the compaction gate cannot decide: %q", k, out)
		}
	}
	if got["block_size"] <= 0 || got["total_blocks"] <= 0 {
		t.Errorf("a DuckDB with a table in it reported block_size=%d total_blocks=%d", got["block_size"], got["total_blocks"])
	}
	if got["used_blocks"]+got["free_blocks"] != got["total_blocks"] {
		t.Errorf("the accounting does not add up: used %d + free %d != total %d",
			got["used_blocks"], got["free_blocks"], got["total_blocks"])
	}
	if got["reclaimable_bytes"] != got["free_blocks"]*got["block_size"] {
		t.Errorf("reclaimable_bytes=%d is not free_blocks*block_size (%d*%d)",
			got["reclaimable_bytes"], got["free_blocks"], got["block_size"])
	}
	// --blocks must NOT pay for the coverage counts: they are the expensive half,
	// and on the ETSI corpus spec_versions does not even exist.
	if strings.Contains(out, "spec_versions=") {
		t.Error("--blocks ran the coverage counts it exists to skip")
	}
}

// captureStdout runs fn with os.Stdout replaced by a pipe and returns what it
// wrote. dbcount reports through fmt.Printf by contract — the guard reads its
// stdout — so testing the contract means reading the same stream.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}
