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

// TestChangelogWriterHasExactlyOneCaller is the other half of the trade above.
//
// enrich's narrow declaration is correct only while `ingest-crs` is the sole user
// of replace_changes and `enrich` is the sole runner of ingest-crs. A second
// caller in another step would make that step's provenance silently wrong — it
// would link a writer it never declares — and the failure would look like a corpus
// that did not update rather than like a missing declaration.
//
// So this counts the callers instead of asserting the comment. When it fails, the
// fix is to add rust/store/src/changes.rs to the new caller's step, not to delete
// this test.
func TestChangelogWriterHasExactlyOneCaller(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}

	// MATCH THE CALL, NOT THE NAME. Grepping for the bare identifier matched the
	// prose in crdb.rs that explains why the writer replaces rather than appends,
	// and the test failed on its own documentation. A method call is `.replace_changes(`
	// and a definition is `fn replace_changes(`; only the first is a caller.
	callers := grepTree(t, filepath.Join(root, "rust"), ".rs", ".replace_changes(")
	var unexpected []string
	for _, p := range callers {
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if rel == "rust/ingest/src/bin/ingest_crs.rs" {
			continue
		}
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
		unexpected = append(unexpected, rel)
	}
	if len(unexpected) > 0 {
		t.Fatalf("replace_changes gained a caller outside ingest-crs: %v\n"+
			"enrich declares rust/store/src/changes.rs on the assumption that it is the only "+
			"step whose binaries link this writer. Add the file to the new caller's step Impl.",
			unexpected)
	}
	if defs := grepTree(t, filepath.Join(root, "rust"), ".rs", "fn replace_changes("); len(defs) != 1 {
		t.Fatalf("expected exactly one definition of replace_changes, found %v", defs)
	}

	// A LOOP OVER AN EMPTY SLICE PASSES. Both halves below assert a POSITIVE count
	// first: without that, renaming replace_changes or changing how the step spawns
	// the binary would empty the search and this test would go green while checking
	// nothing — the failure mode of a guard whose whole job is to notice a change.
	if len(callers) == 0 {
		t.Fatal("no file mentions replace_changes; the search string is stale and this " +
			"test is no longer checking anything")
	}

	runners := grepTree(t, filepath.Join(root, "internal", "goal"), ".go", `rbin("ingest-crs")`)
	var prod []string
	for _, p := range runners {
		if !strings.HasSuffix(p, "_test.go") {
			prod = append(prod, filepath.ToSlash(p))
		}
	}
	if len(prod) == 0 {
		t.Fatal("no step runs ingest-crs; either the changelog overlay was removed or " +
			"the search string is stale")
	}
	for _, p := range prod {
		if !strings.HasSuffix(p, "internal/goal/pipeline_embed.go") {
			t.Fatalf("ingest-crs is run from %s as well as enrich; the changelog writer's "+
				"provenance now has to be declared there too", p)
		}
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
