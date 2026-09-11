package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fold used to be a step of its own, `merge`, and the runner remembered for
// it whether it had completed. Inside `ingest` that memory is fold-state.json, and
// these tests pin the one decision it feeds: fold, or decline.

// foldFixture writes the fold's implementation files under c.Root, so
// foldIdentity has something real to hash.
func foldFixture(t *testing.T, c *Ctx) {
	t.Helper()
	for path, body := range map[string]string{
		"rust/store/src/bin/merge.rs":         "fn main() {}\n",
		"rust/store/src/lib.rs":               "pub fn f() {}\n",
		"cmd/migrate-paragraphs/main.go":      "package main\n",
		"cmd/migrate-paragraphs/main_test.go": "package main\n",
	} {
		write(t, filepath.Join(c.Root, filepath.FromSlash(path)), body)
	}
}

func TestFoldReasonNamesEveryCaseThatFolds(t *testing.T) {
	cur := map[string]string{"rust/store/src/bin/merge.rs": "a", "rust/store/src/lib.rs": "b"}
	done := &foldState{Impl: cur}

	for _, tc := range []struct {
		name   string
		gained bool
		st     *foldState
		cur    map[string]string
		full   bool
		want   string // substring; "" means DECLINE
	}{
		{"nothing new, fold current", false, done, cur, false, ""},
		{"the parse gained clauses", true, done, cur, false, "added clauses"},
		{"a full rebuild", false, done, cur, true, "full rebuild"},
		{"no fold on record", false, nil, cur, false, "no completed fold"},
		{"a fold that died", false, &foldState{Pending: true, Impl: cur}, cur, false, "did not complete"},
		{"the fold's code moved", false, done,
			map[string]string{"rust/store/src/bin/merge.rs": "a", "rust/store/src/lib.rs": "CHANGED"}, false,
			"rust/store/src/lib.rs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := foldReason(tc.gained, tc.st, tc.cur, tc.full)
			if tc.want == "" && got != "" {
				t.Fatalf("folds (%q) where nothing is left to fold", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("reason %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// THE TRAP THE MARKER EXISTS FOR. `ingest --resume` skips whatever the shards'
// ingest_log already holds, so the retry of a fold that died parses NOTHING — and
// a decline keyed on the parse tally alone would record success over a corpus that
// never received those clauses. The pending flag written before the fold is what
// makes the retry fold.
func TestAFoldThatDiedIsRetriedWhenTheParseFindsNothingNew(t *testing.T) {
	c, _ := newTestCtx(t)
	foldFixture(t, c)
	cur, err := foldIdentity(c)
	if err != nil {
		t.Fatal(err)
	}

	// What stepIngest3GPP writes immediately before foldShards.
	if err := saveFoldState(c, foldState{Pending: true, Impl: cur}); err != nil {
		t.Fatal(err)
	}
	// ... the fold dies here. The retry's parse gains nothing:
	st, err := loadFoldState(c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if why := foldReason(false, st, cur, false); why == "" {
		t.Fatal("a fold that never completed is declined on the retry: the shards' clauses never reach the corpus")
	}

	// And once it completes, the next no-op parse declines.
	if err := saveFoldState(c, foldState{Impl: cur}); err != nil {
		t.Fatal(err)
	}
	st, _ = loadFoldState(c, cur)
	if why := foldReason(false, st, cur, false); why != "" {
		t.Fatalf("a completed fold is redone for nothing: %s", why)
	}
}

// THE FIRST RUN AFTER THE REFACTOR MUST NOT RE-FOLD A CURRENT CORPUS. There is no
// fold-state.json yet; the last `merge` record is the evidence. Re-proving a fold
// that already happened costs 34 minutes, a rewritten corpus and a 22 GB layer.
func TestTheFirstRunAdoptsTheLastMergeWhenItsCodeIsCurrent(t *testing.T) {
	c, store := newTestCtx(t)
	foldFixture(t, c)
	cur, err := foldIdentity(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, counted := cur["cmd/migrate-paragraphs/main_test.go"]; counted {
		t.Fatal("the fold identity counts a test file, which no binary it runs compiles")
	}

	// The legacy record counted main_test.go; the adoption must not require it.
	legacy := map[string]string{"cmd/migrate-paragraphs/main_test.go": "whatever"}
	for k, v := range cur {
		legacy[k] = v
	}
	if err := store.Save(&Record{Step: "merge", Status: StatusSuccess, Impl: legacy}); err != nil {
		t.Fatal(err)
	}

	st, err := loadFoldState(c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if why := foldReason(false, st, cur, false); why != "" {
		t.Fatalf("the first run re-folds a corpus the last merge already folded with this code: %s", why)
	}
	if _, err := os.Stat(foldStatePath(c)); err != nil {
		t.Fatalf("the adoption was not persisted, so it would be re-derived from a retired record forever: %v", err)
	}
}

// And the adoption is only as good as the match: a merge record from other fold
// code, or one that did not succeed, proves nothing about this fold.
func TestTheFirstRunFoldsWhenTheLastMergeIsNotEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
	}{
		{"the fold's code moved since", func(r *Record) { r.Impl["rust/store/src/lib.rs"] = "older" }},
		{"the merge record is a failure", func(r *Record) { r.Status = StatusFailed }},
		{"the merge record lacks a fold file", func(r *Record) { delete(r.Impl, "rust/store/src/bin/merge.rs") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store := newTestCtx(t)
			foldFixture(t, c)
			cur, err := foldIdentity(c)
			if err != nil {
				t.Fatal(err)
			}
			rec := &Record{Step: "merge", Status: StatusSuccess, Impl: map[string]string{}}
			for k, v := range cur {
				rec.Impl[k] = v
			}
			tc.mutate(rec)
			if err := store.Save(rec); err != nil {
				t.Fatal(err)
			}
			st, err := loadFoldState(c, cur)
			if err != nil {
				t.Fatal(err)
			}
			if why := foldReason(false, st, cur, false); why == "" {
				t.Fatal("adopted a merge record that does not vouch for the current fold")
			}
		})
	}
}

// A fold-state.json nobody can read is not "nothing to do".
func TestAnUnreadableFoldStateFolds(t *testing.T) {
	c, _ := newTestCtx(t)
	foldFixture(t, c)
	cur, err := foldIdentity(c)
	if err != nil {
		t.Fatal(err)
	}
	write(t, foldStatePath(c), "{not json")
	st, err := loadFoldState(c, cur)
	if err != nil {
		t.Fatal(err)
	}
	if foldReason(false, st, cur, false) == "" {
		t.Fatal("an unreadable fold state was read as a completed fold")
	}
}

// The fold's files are declared in the step's Impl as well as hashed on their own:
// a step whose own fingerprint ignored them would never re-run to ask.
func TestIngestDeclaresTheFoldItRuns(t *testing.T) {
	s := stepIngest(corpus3GPP())
	for _, f := range foldImpl {
		found := false
		for _, p := range s.Impl {
			if p == f {
				found = true
			}
		}
		if !found {
			t.Errorf("ingest runs the fold but does not declare %s", f)
		}
	}
	if !s.ExcludeTests {
		t.Error("ingest counts test files, so a test-only commit in cmd/migrate-paragraphs replays the fold")
	}
}
