package goal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// glossaryWriter is the one Go file that writes rows of `acronyms`, and the one
// `enrich` names for it. See internal/store/acronyms_write.go.
const glossaryWriter = "internal/store/acronyms_write.go"

// TestEnrichDeclaresTheGlossaryWriter holds the narrow declaration that makes
// internal/store/acronyms_write.go worth being a file of its own — the Go twin of
// TestEnrichDeclaresTheChangelogWriter.
//
// THE HOLE IT CLOSES. seed-glossary's rows reach the corpus through store
// methods, and until 2026-09-11 those sat in store.go, which enrich does not
// declare: a change to the glossary writer changed what enrich writes without
// enrich replaying, and the pipeline shipped the old glossary as current. Since
// the write became a REPLACEMENT the hole is sharper — the rule that decides which
// rows are DELETED lives in that file.
//
// BOTH DIRECTIONS ARE PINNED, because each has its own failure. Too narrow and
// the corpus goes stale in silence; too wide — internal/store whole, or store.go —
// and every serve-path edit replays enrich, then paragraphs, sparse and index, and
// re-pushes the corpus, to rewrite a glossary the edit did not touch.
func TestEnrichDeclaresTheGlossaryWriter(t *testing.T) {
	step := stepEnrich(corpus3GPP())

	for _, want := range []string{
		glossaryWriter,
		"internal/glossaryseed",
		"internal/abbrev",
		"cmd/seed-glossary",
	} {
		if !containsString(step.Impl, want) {
			t.Errorf("enrich must declare %q: it runs seed-glossary, and a file it does not "+
				"declare can change the glossary without replaying the step that writes it.\nImpl = %v",
				want, step.Impl)
		}
	}
	for _, tooWide := range []string{"internal/store", "internal/store/store.go"} {
		if containsString(step.Impl, tooWide) {
			t.Errorf("enrich declares %q: every edit to the store's serve path would replay enrich "+
				"and the corpus chain behind it. Declare %s, which is the glossary write path "+
				"and nothing else.", tooWide, glossaryWriter)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRootForTest(), filepath.FromSlash(glossaryWriter))); err != nil {
		t.Fatalf("enrich declares %s and it does not exist: implHash refuses a missing path, "+
			"and a renamed writer would otherwise leave the declaration pointing at nothing: %v",
			glossaryWriter, err)
	}

	// THE DECLARATION MUST MOVE THE FINGERPRINT, not merely appear in a list:
	// ExcludeTests or a pattern rule could still drop it on the way to the hash.
	root := t.TempDir()
	implFixture(t, root, step.Impl)
	before, _, err := implHash(root, step.Impl, false)
	if err != nil {
		t.Fatal(err)
	}
	if after := implAfterWriting(t, root, step.Impl, glossaryWriter, "package store // changed\n"); after == before {
		t.Errorf("editing %s leaves enrich's fingerprint unchanged — the glossary writer can "+
			"change and the corpus keeps the old glossary", glossaryWriter)
	}
	// The other direction, from a fresh baseline: the tree just changed.
	base, _, err := implHash(root, step.Impl, false)
	if err != nil {
		t.Fatal(err)
	}
	if after := implAfterWriting(t, root, step.Impl, "internal/store/store.go", "package store // serve\n"); after != base {
		t.Errorf("editing internal/store/store.go replays enrich — the store's serve path is not " +
			"the glossary write path, and replaying enrich drags the corpus chain and a re-push behind it")
	}
}

// TestGlossaryWriterHasExactlyTheCallersEnrichAssumes is the other half of the
// trade above, the Go twin of TestEveryChangelogWriterIsDeclaredByTheStepThatRunsIt.
//
// enrich's narrow declaration is correct only while the glossary is written by
// this file alone, only seed-glossary calls it, and only enrich runs seed-glossary.
// A second writer, caller or runner would make some step's provenance silently
// wrong — it would write the glossary through code it never declares — and the
// failure would look like a corpus that did not update rather than like a missing
// declaration. So this counts them instead of trusting the comment. When it fails,
// the fix is to declare the file in the new step, not to delete this test.
func TestGlossaryWriterHasExactlyTheCallersEnrichAssumes(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}
	rel := func(p string) string {
		return filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
	}
	// prod lists the production Go files under cmd/ and internal/ holding needle.
	// A _test.go is not a caller in the sense meant here: no pipeline step runs it
	// and it writes no corpus, so it cannot make any step's provenance wrong.
	prod := func(needle string) []string {
		var out []string
		for _, dir := range []string{"cmd", "internal"} {
			for _, p := range grepTree(t, filepath.Join(root, dir), ".go", needle) {
				if !strings.HasSuffix(p, "_test.go") {
					out = append(out, rel(p))
				}
			}
		}
		sort.Strings(out)
		return out
	}

	// 1. EVERY GO STATEMENT THAT WRITES A ROW OF `acronyms` LIVES IN THE DECLARED
	// FILE. A writer anywhere else in the store would reach the corpus through a
	// file no step declares — the hole this whole arrangement closes.
	for _, stmt := range []string{"INTO acronyms", "DELETE FROM acronyms", "UPDATE acronyms"} {
		got := prod(stmt)
		if stmt == "INTO acronyms" && len(got) == 0 {
			t.Fatalf("no production file says %q: the search string is stale and this test "+
				"is no longer checking anything", stmt)
		}
		for _, p := range got {
			if p != glossaryWriter {
				t.Errorf("%s writes the acronyms table (%q) outside %s, which is the only glossary "+
					"writer enrich declares. Move it there, or declare it in the step that runs it.",
					p, stmt, glossaryWriter)
			}
		}
	}

	// 2. THE WRITE PATH'S METHODS: defined once, in the declared file, and called in
	// production by exactly the files the declaration assumes.
	for _, m := range []struct {
		name    string
		callers []string
	}{
		{"ReplaceSeededAcronyms", []string{"internal/glossaryseed/glossaryseed.go"}},
		{"PlanSeededAcronyms", []string{"internal/glossaryseed/glossaryseed.go"}},
		{"SpecsWithAbbreviations", []string{"internal/glossaryseed/glossaryseed.go"}},
		// The single-row writer ships in no binary. Tests seed fixtures with it; a
		// production caller would be a glossary writer in some step that does not
		// declare this file.
		{"UpsertAcronym", nil},
	} {
		if defs := prod("func (s *Store) " + m.name + "("); len(defs) != 1 || defs[0] != glossaryWriter {
			t.Errorf("%s must be defined exactly once, in %s; found %v", m.name, glossaryWriter, defs)
		}
		// MATCH THE CALL, NOT THE NAME — the lesson TestEveryChangelogWriterIsDeclaredByTheStepThatRunsIt
		// paid for when it failed on its own documentation. A call is `.Name(`.
		got := prod("." + m.name + "(")
		if strings.Join(got, ",") != strings.Join(m.callers, ",") {
			t.Errorf("%s is called in production from %v, want %v.\n"+
				"enrich declares %s on the assumption that seed-glossary is the only binary that "+
				"writes the glossary. Declare the file in the new caller's step.",
				m.name, got, m.callers, glossaryWriter)
		}
	}

	// 3. ONLY seed-glossary LINKS THE MINER. A LOOP OVER AN EMPTY SLICE PASSES, so
	// the positive count comes first.
	importers := prod(`"github.com/kodflow/3gpp-mcp/internal/glossaryseed"`)
	if len(importers) == 0 {
		t.Fatal("nothing imports internal/glossaryseed; the search string is stale")
	}
	if strings.Join(importers, ",") != "cmd/seed-glossary/main.go" {
		t.Errorf("internal/glossaryseed is linked by %v; only cmd/seed-glossary may be, or the "+
			"binary that also links it writes the glossary from a step that does not declare %s",
			importers, glossaryWriter)
	}

	// 4. ONLY enrich RUNS seed-glossary — asked of the SYNTAX TREE, so the answer is
	// the enclosing step function rather than a file that holds forty of them.
	runners := binCallers(t, filepath.Join(root, "internal", "goal"), "seed-glossary")
	if len(runners) == 0 {
		t.Fatal("no step runs seed-glossary; either the glossary seed was removed or the " +
			"search is stale")
	}
	if strings.Join(runners, ",") != "stepEnrich" {
		t.Errorf("seed-glossary is run from %v; only stepEnrich declares %s, so any other "+
			"runner writes the glossary through code its fingerprint does not cover",
			runners, glossaryWriter)
	}
}

// TestThePipelineNeverPassesTheMassRemovalOptOut pins that --allow-mass-removal
// stays an operator's decision.
//
// The mass-removal guard exists because the glossary write now DELETES, and a
// sweep that silently lost specs would delete their rows in an unattended run.
// A pipeline that passed the opt-out would disarm the guard exactly where it is
// needed, and every gate would stay green — the refusal it suppresses is the only
// signal there is.
func TestThePipelineNeverPassesTheMassRemovalOptOut(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}
	const flag = "allow-mass-removal"
	// A LOOP OVER AN EMPTY RESULT PASSES: prove the flag still exists under this
	// name first, or renaming it would leave this test checking nothing.
	if defs := grepTree(t, filepath.Join(root, "cmd", "seed-glossary"), ".go", `"`+flag+`"`); len(defs) == 0 {
		t.Fatalf("cmd/seed-glossary no longer defines --%s; the search string is stale", flag)
	}
	var passers []string
	for _, p := range grepTree(t, filepath.Join(root, "internal", "goal"), ".go", flag) {
		if !strings.HasSuffix(p, "_test.go") {
			passers = append(passers, filepath.ToSlash(p))
		}
	}
	if len(passers) > 0 {
		t.Errorf("the pipeline mentions --%s in %v: an unattended enrich must never disarm the "+
			"mass-removal guard. A deliberate cleanup is run by hand.", flag, passers)
	}
}

// binCallers names the functions, in the production files of dir, that spawn the
// Go binary `name` through c.bin(name).
func binCallers(t *testing.T, dir, name string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "bin" {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, err := strconv.Unquote(lit.Value); err == nil && v == name {
					out = append(out, fn.Name.Name)
				}
				return true
			})
		}
	}
	sort.Strings(out)
	return out
}
