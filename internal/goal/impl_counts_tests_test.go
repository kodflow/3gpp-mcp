package goal

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// countsTestFiles records the steps that deliberately let test files into their
// fingerprint, each with the reason. It is the ONLY place such a step can be
// recorded, so adding one is a decision someone typed and a reviewer can see —
// the same shape as armShared.
//
// WHY THE MAP EXISTS RATHER THAN A BLANKET FIX. Setting ExcludeTests on a step
// CHANGES its fingerprint, so it replays once. That is 4 minutes for enrich and
// 34m15 for merge, and merge rewrites the corpus, which then costs a 42 GB image
// re-push on top. Paying all of it at once, for steps whose replay is otherwise
// cheap, is not obviously right — so the ones left are left ON PURPOSE, with
// their price written down, and this test stops the list from growing by
// accident.
var countsTestFiles = map[string]string{
	"test": "runs the suites; test files are its INPUT, not noise — the reason ExcludeTests exists",

	// Cheap to replay, so the one-time fingerprint churn buys little.
	"smoke":         "internal/mcp + internal/search; replaying it is 23.9 s (build E)",
	"validate":      "cmd/validate; replaying it is 2m32 (build E)",
	"validate-etsi": "cmd/validate, shared with validate; replaying it is 17.6 s (build E)",
	"discover-etsi": "internal/etsicat; replaying it is 39.6 s and it re-enumerates anyway",

	// Expensive, and deliberately deferred: fixing it costs one replay of the
	// thing it protects.
	"merge":           "cmd/migrate-paragraphs; replaying merge is 34m15 and rewrites the corpus, which then costs a 42 GB image re-push",
	"paragraphs":      "cmd/migrate-paragraphs, same binary as merge; deferred with it so the two move together",
	"paragraphs-etsi": "cmd/migrate-paragraphs, the ETSI twin; replaying it is 8m07 and rewrites etsi.duckdb, which drags compact-etsi, index-etsi and a 19 GB layer re-push",
}

// TestNoStepCountsTestFilesByAccident walks every step's Impl and fails when one
// names a directory holding test artefacts without either excluding them or
// saying why it does not.
//
// THE DEFECT THIS PINS, measured on build E (2026-09-09). enrich named four Go
// packages as DIRECTORIES. Adding one _test.go to internal/abbrev — in a commit
// whose PR said "tests only, no production code changes" — replayed enrich,
// paragraphs, sparse and index, rewrote data/3gpp.duckdb, and turned a publish
// that would have pushed one 70 MB layer into one that pushed 42 GB.
//
// The ETSI arm already learned this and wrote the price down: stepEnrichETSI names
// individual .rs FILES because "naming rust/ingest/src/bin made a fix to
// ingest_li.rs invalidate the whole ETSI half (~1 h of rework, 18.8 GiB
// re-pushed)". The 3GPP arm never applied it to its Go half.
func TestNoStepCountsTestFilesByAccident(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	var offenders []string
	for _, s := range Pipeline() {
		if s.ExcludeTests {
			continue
		}
		var dirs []string
		for _, pattern := range s.Impl {
			if holdsTestArtefacts(filepath.Join(root, pattern)) {
				dirs = append(dirs, pattern)
			}
		}
		if len(dirs) == 0 {
			continue
		}
		if _, recorded := countsTestFiles[s.Name]; recorded {
			continue
		}
		offenders = append(offenders, s.Name+" (via "+strings.Join(dirs, ", ")+")")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("these steps count test files in their fingerprint and neither exclude them nor "+
			"say why:\n  %s\n\nA _test.go cannot change what a step that RUNS a binary does. Either set "+
			"ExcludeTests: true, or name the individual source files instead of the directory (as "+
			"stepEnrichETSI does), or record the step in countsTestFiles with the measured cost of "+
			"replaying it.", strings.Join(offenders, "\n  "))
	}
}

// TestEveryRecordedExceptionStillNeedsToBe keeps the map honest in the other
// direction: an entry that no longer describes a real step, or describes one that
// has since started excluding tests, is worse than no entry — countsTestFiles is
// consulted to decide whether a violation is deliberate, so a stale line silently
// excuses whatever later takes that name.
func TestEveryRecordedExceptionStillNeedsToBe(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	for name, why := range countsTestFiles {
		// AN ENTRY WITH NO REASON IS WORSE THAN NO ENTRY. The map is consulted only
		// for the NAME, so `"merge": ""` would suppress the guard while recording
		// nothing — the exact silence the map exists to break.
		if strings.TrimSpace(why) == "" {
			t.Errorf("countsTestFiles excuses %q with an empty reason; the reason IS the entry", name)
			continue
		}
		s, ok := byName[name]
		if !ok {
			t.Errorf("countsTestFiles excuses %q (%s), and no such step is in the pipeline", name, why)
			continue
		}
		if s.ExcludeTests {
			t.Errorf("countsTestFiles excuses %q, but it now sets ExcludeTests — delete the entry", name)
			continue
		}
		var any bool
		for _, pattern := range s.Impl {
			if holdsTestArtefacts(filepath.Join(root, pattern)) {
				any = true
				break
			}
		}
		if !any {
			t.Errorf("countsTestFiles excuses %q, but nothing in its Impl holds test artefacts any "+
				"more — delete the entry rather than leave it excusing the next change", name)
		}
	}
}

// holdsTestArtefacts reports whether an Impl entry brings test material into a
// fingerprint — either because it IS a test file, or because it is a directory
// containing one.
//
// The first case is not hypothetical padding: the original version answered only
// the directory question, so a step that named `cmd/foo/main_test.go` outright
// passed both guards. Naming a file is the RECOMMENDED escape from this defect
// (stepEnrichETSI names .rs files precisely to avoid a directory), which makes an
// unchecked file path the likeliest way to reintroduce it.
func holdsTestArtefacts(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return isTestArtefact(path)
	}
	found := false
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if !d.IsDir() && isTestArtefact(p) {
			found = true
		}
		return nil
	})
	return found
}
