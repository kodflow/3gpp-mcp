package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnrichDeclaresTheChangelogWriter holds the narrow declaration that makes
// rust/store/src/changes.rs worth being a file of its own.
//
// THE TRADE THIS PINS. The changelog writer links the store library, and `merge`
// declares rust/store/src/lib.rs because it genuinely links it too. Leaving
// replace_changes in lib.rs would have made every edit to the changelog replay
// merge (34m15), then paragraphs (20m32), compact (19m22) and index (8m06), and
// re-push a 42 GB image — to rewrite a table none of those steps read. Split into
// its own file and declared by `enrich`, the same edit costs one replay of enrich.
//
// The split is the NARROW direction of a provenance declaration, and narrow is the
// dangerous direction: a step that fails to replay ships a stale corpus and nothing
// says so, which is how `cargo update` alone could once change a binary with no
// data step noticing. It is safe here only while `enrich` is the step that runs
// ingest-crs — so that is what this asserts, rather than trusting the comment.
func TestEnrichDeclaresTheChangelogWriter(t *testing.T) {
	step := stepEnrich(corpus3GPP())

	for _, want := range []string{
		"rust/store/src/changes.rs",
		"rust/ingest/src/bin/ingest_crs.rs",
		"scripts/fetch-crdb.sh",
	} {
		if !containsString(step.Impl, want) {
			t.Errorf("enrich must declare %q: it runs ingest-crs, and a file it does not "+
				"declare can change the changelog without replaying the step that writes it.\nImpl = %v",
				want, step.Impl)
		}
	}
}

// TestEveryChangelogWriterIsDeclaredByTheStepThatRunsIt is the other half of the
// trade above.
//
// enrich's narrow declaration is correct only while the step that runs a caller of
// replace_changes is the step that declares rust/store/src/changes.rs. A caller in
// a step that does not declare it would make that step's provenance silently wrong
// — it would link a writer it never declares — and the failure would look like a
// corpus that did not update rather than like a missing declaration.
//
// THERE ARE TWO CALLERS NOW, ONE PER ARM. ingest-crs writes the 3GPP changelog from
// the CR database and is run by `enrich`; ingest-etsi-changes writes the ETSI one
// from the deliverables' change-history annexes and is run by `enrich-etsi`. This
// used to assert exactly one caller; what it protects was never the count, it was
// the pairing, so the pairing is what it now checks — for every caller.
//
// So this counts the callers instead of asserting the comment. When it fails on an
// unknown caller, the fix is to add rust/store/src/changes.rs (and the caller) to
// the Impl of the step that runs it, and a row to the table below — not to delete
// this test.
func TestEveryChangelogWriterIsDeclaredByTheStepThatRunsIt(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}

	writers := []struct {
		file, bin string
		step      *Step
	}{
		{"rust/ingest/src/bin/ingest_crs.rs", "ingest-crs", stepEnrich(corpus3GPP())},
		{"rust/ingest/src/bin/ingest_etsi_changes.rs", "ingest-etsi-changes", stepEnrich(corpusETSI())},
	}
	known := map[string]bool{}
	for _, w := range writers {
		known[w.file] = true
	}

	// MATCH THE CALL, NOT THE NAME. Grepping for the bare identifier matched the
	// prose in crdb.rs that explains why the writer replaces rather than appends,
	// and the test failed on its own documentation. A method call is `.replace_changes(`
	// and a definition is `fn replace_changes(`; only the first is a caller.
	callers := grepTree(t, filepath.Join(root, "rust"), ".rs", ".replace_changes(")
	var unexpected []string
	seen := map[string]bool{}
	for _, p := range callers {
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		// A TEST TARGET IS NOT A CALLER IN THE SENSE THIS TEST MEANS, and the
		// second half of this same function already says so about Go by skipping
		// `_test.go`. What is being protected is STEP PROVENANCE: a production
		// caller in another step would make that step link a writer it never
		// declares, and ship a stale corpus with nothing to show for it. A file
		// under rust/<crate>/tests/ is a cargo test target — no pipeline step runs
		// it and it writes no corpus, so it cannot make any step's provenance
		// wrong. `#[cfg(test)]` inside src/ is deliberately NOT excused: it is not
		// distinguishable by path, and being conservative there costs a comment
		// rather than a corpus.
		if strings.Contains(rel, "/tests/") {
			continue
		}
		if !known[rel] {
			unexpected = append(unexpected, rel)
			continue
		}
		seen[rel] = true
	}
	if len(unexpected) > 0 {
		t.Fatalf("replace_changes gained a caller no step is known to declare: %v\n"+
			"Add rust/store/src/changes.rs and the caller to the Impl of the step that runs it, "+
			"and a row to this test's table.", unexpected)
	}
	if defs := grepTree(t, filepath.Join(root, "rust"), ".rs", "fn replace_changes("); len(defs) != 1 {
		t.Fatalf("expected exactly one definition of replace_changes, found %v", defs)
	}

	// A LOOP OVER AN EMPTY SLICE PASSES. Every row of the table must be FOUND as a
	// caller and as a runner: without that, renaming replace_changes or changing
	// how the step spawns the binary would empty the search and this test would go
	// green while checking nothing — the failure mode of a guard whose whole job is
	// to notice a change.
	for _, w := range writers {
		if !seen[w.file] {
			t.Errorf("%s no longer calls replace_changes; the table or the search string is stale", w.file)
		}
		for _, want := range []string{"rust/store/src/changes.rs", w.file} {
			if !containsString(w.step.Impl, want) {
				t.Errorf("%s runs %s and does not declare %q: a change there can rewrite the "+
					"changelog without replaying the step that writes it.\nImpl = %v",
					w.step.Name, w.bin, want, w.step.Impl)
			}
		}
		runners := grepTree(t, filepath.Join(root, "internal", "goal"), ".go", `rbin("`+w.bin+`")`)
		var prod []string
		for _, p := range runners {
			if !strings.HasSuffix(p, "_test.go") {
				prod = append(prod, filepath.ToSlash(p))
			}
		}
		if len(prod) == 0 {
			t.Errorf("no step runs %s; either the writer was removed or the search string is stale", w.bin)
		}
		for _, p := range prod {
			if !strings.HasSuffix(p, "internal/goal/pipeline_embed.go") {
				t.Errorf("%s is run from %s as well as %s; the changelog writer's provenance "+
					"now has to be declared there too", w.bin, p, w.step.Name)
			}
		}
	}
}

// THE ETSI WRITER STAYS OFF THE 3GPP ARM, and the 3GPP one off the ETSI arm.
//
// Each arm declares only the writer it runs. Declaring the other would replay that
// arm's enrich — and, on the 3GPP side, paragraphs, sparse, compact, index and a
// 22 GB re-push behind it — for an edit to a binary it never launches: the shape
// ingest_li.rs cost the ETSI half on 2026-09-06 (~1 h, 18.8 GiB).
func TestEachArmDeclaresOnlyItsOwnChangelogWriter(t *testing.T) {
	if containsString(stepEnrich(corpus3GPP()).Impl, "rust/ingest/src/bin/ingest_etsi_changes.rs") {
		t.Error("enrich (3GPP) declares the ETSI changelog writer, which it never runs")
	}
	if containsString(stepEnrich(corpusETSI()).Impl, "rust/ingest/src/bin/ingest_crs.rs") {
		t.Error("enrich-etsi declares ingest_crs.rs, which it never runs")
	}
	found := false
	for _, b := range rustBins["rust/ingest/Cargo.toml"] {
		if b == "ingest-etsi-changes" {
			found = true
		}
	}
	if !found {
		t.Error("ingest-etsi-changes is not in rustBins: build-rust would never compile it, and " +
			"enrich-etsi would fail on a fresh clone with \"the binary is missing\" — the defect " +
			"ingest-glossary had")
	}
}

// containsString is a local helper: the goal package has no generics dependency
// and this reads better at the call site than a slices import for one predicate.
func containsString(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// grepTree returns every file under dir with the given extension whose contents
// hold needle.
func grepTree(t *testing.T, dir, ext, needle string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || filepath.Ext(p) != ext {
			return nil
		}
		// target/ is build output: it carries copies of the sources and would make
		// every count wrong.
		if strings.Contains(filepath.ToSlash(p), "/target/") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), needle) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}
