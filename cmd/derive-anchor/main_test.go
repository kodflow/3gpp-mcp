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

func buildCorpus(t *testing.T, path string, versions []model.SpecVersion) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, v := range versions {
		if !seen[v.SpecID] {
			if err := st.UpsertSpec(model.Spec{SpecID: v.SpecID, Series: model.SeriesOf(v.SpecID), DocType: "TS"}); err != nil {
				t.Fatal(err)
			}
			seen[v.SpecID] = true
		}
		if err := st.UpsertVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// The anchor of a corpus is read off its spec_versions, highest version per
// (spec, release) in numeric order, written in the fold's exact bytes — and the
// corpus is only read.
func TestTheAnchorIsReadOffTheCorpus(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "3gpp.duckdb")
	buildCorpus(t, db, []model.SpecVersion{
		{SpecID: "22.261", Release: "Rel-19", Version: "19.9.0"},
		{SpecID: "22.261", Release: "Rel-19", Version: "19.14.0"},
		{SpecID: "23.501", Release: "Rel-18", Version: "18.5.0"},
		{SpecID: "23.501", Release: "Rel-19", Version: "19.2.0"},
	})
	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "corpus-index.json")
	n, err := run(context.Background(), db, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("%d keys, want 3", n)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n \"22.261|Rel-19\": \"19.14.0\",\n \"23.501|Rel-18\": \"18.5.0\",\n \"23.501|Rel-19\": \"19.2.0\"\n}"
	if string(got) != want {
		t.Fatalf("anchor:\n%s\nwant:\n%s", got, want)
	}
	if left, _ := filepath.Glob(out + ".*.tmp"); len(left) > 0 {
		t.Errorf("the temporary file was left behind: %v", left)
	}
	after, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("deriving the anchor wrote to the corpus")
	}
}

// An empty anchor would read as "nothing indexed" and send discover after the
// whole archive. A corpus with no versions is reported, not described.
func TestAnEmptyCorpusIsRefused(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "empty.duckdb")
	buildCorpus(t, db, nil)
	out := filepath.Join(dir, "corpus-index.json")
	if _, err := run(context.Background(), db, out); err == nil || !strings.Contains(err.Error(), "empty anchor") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an anchor was written for an empty corpus")
	}
}
