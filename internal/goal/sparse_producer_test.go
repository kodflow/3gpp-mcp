package goal

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------ the producer's closure
//
// THE LIST THIS HOLDS THE DECLARATIONS TO IS DERIVED, NOT WRITTEN. A test that
// compares sparseProducerImpl to a second copy of the same three lines passes when
// both lose Cargo.lock together, and that is exactly how the gap this closes was
// left open: build-sparse declared the manifest and the lockfile, the sparse steps
// did not, and every test read the declaration it was meant to check. So the
// closure is read from what cargo reads: the [[bin]] entry of embed-core-sparse in
// rust/embed-core/Cargo.toml, the library it links, the module tree both roots
// declare, any path dependency, a build script, and the lockfile cargo obeys.

// cargoManifest is the part of a Cargo.toml the closure depends on.
type cargoManifest struct {
	libPath   string
	bins      map[string]string // [[bin]] name -> path
	build     string            // [package] build, "" when absent
	pathDeps  []string          // path = "…" of every dependency cargo compiles in
	hasBuildF bool              // [package] build = false
}

var (
	tomlHeaderRe = regexp.MustCompile(`^\[\[?\s*([^\]]+?)\s*\]\]?$`)
	tomlStringRe = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=\s*"([^"]*)"`)
	tomlPathRe   = regexp.MustCompile(`\bpath\s*=\s*"([^"]+)"`)
)

// readCargoManifest reads the handful of keys the closure needs. It is not a TOML
// parser and does not pretend to be: a shape it cannot read makes the caller fail,
// rather than silently narrowing the closure.
func readCargoManifest(t *testing.T, manifest string) cargoManifest {
	t.Helper()
	m := cargoManifest{bins: map[string]string{}}
	section, binName, binPath := "", "", ""
	flushBin := func() {
		if binName != "" {
			m.bins[binName] = binPath
		}
		binName, binPath = "", ""
	}
	for _, raw := range strings.Split(readLF(t, manifest), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if h := tomlHeaderRe.FindStringSubmatch(line); h != nil {
			if section == "bin" {
				flushBin()
			}
			section = h[1]
			continue
		}
		key, val := "", ""
		if kv := tomlStringRe.FindStringSubmatch(line); kv != nil {
			key, val = kv[1], kv[2]
		}
		switch {
		case section == "bin" && key == "name":
			binName = val
		case section == "bin" && key == "path":
			binPath = val
		case section == "lib" && key == "path":
			m.libPath = val
		case section == "package" && key == "build":
			m.build = val
		case section == "package" && strings.HasPrefix(line, "build") && strings.Contains(line, "false"):
			m.hasBuildF = true
		}
		// Dev-dependencies are compiled into tests only, never into the binary.
		if strings.Contains(section, "dependencies") && !strings.Contains(section, "dev-dependencies") {
			if p := tomlPathRe.FindStringSubmatch(line); p != nil {
				m.pathDeps = append(m.pathDeps, p[1])
			}
		}
	}
	if section == "bin" {
		flushBin()
	}
	if m.libPath == "" {
		m.libPath = "src/lib.rs"
	}
	return m
}

var (
	rustModDeclRe = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)\s*;`)
	rustIncludeRe = regexp.MustCompile(`include_(?:str|bytes)!\s*\(\s*"([^"]+)"`)
)

// rustModuleClosure is every file the module tree rooted at rootFile compiles:
// `mod x;` resolves to x.rs or x/mod.rs, include_str!/include_bytes! name files
// directly. Every `mod` is followed whatever its #[cfg] — a feature-gated module is
// still compiled into the binary built with that feature. A #[path] attribute is a
// shape this reader does not follow, so it fails instead of guessing.
func rustModuleClosure(t *testing.T, root, rootFile string) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	var walk func(rel string, isRoot bool)
	walk = func(rel string, isRoot bool) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, rel)
		src := readLF(t, filepath.Join(root, filepath.FromSlash(rel)))
		dir := path.Dir(rel)
		childDir := dir
		if base := path.Base(rel); !isRoot && base != "mod.rs" {
			childDir = path.Join(dir, strings.TrimSuffix(base, ".rs"))
		}
		for _, line := range strings.Split(src, "\n") {
			code := line
			if i := strings.Index(code, "//"); i >= 0 {
				code = code[:i]
			}
			if strings.Contains(code, "#[path") {
				t.Fatalf("%s uses #[path], which this closure reader does not follow: teach it, or the "+
					"sparse producer's identity will miss the file it names", rel)
			}
			if m := rustIncludeRe.FindStringSubmatch(code); m != nil {
				out = append(out, path.Join(dir, m[1]))
			}
			m := rustModDeclRe.FindStringSubmatch(code)
			if m == nil {
				continue
			}
			flat := path.Join(childDir, m[1]+".rs")
			nested := path.Join(childDir, m[1], "mod.rs")
			switch {
			case fileExists(filepath.Join(root, filepath.FromSlash(flat))):
				walk(flat, false)
			case fileExists(filepath.Join(root, filepath.FromSlash(nested))):
				walk(nested, false)
			default:
				t.Fatalf("%s declares `mod %s;` and neither %s nor %s exists", rel, m[1], flat, nested)
			}
		}
	}
	walk(rootFile, true)
	return out
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// sparseProducerClosure is the set of repository files cargo reads to build
// embed-core-sparse, derived from the manifest.
func sparseProducerClosure(t *testing.T) []string {
	t.Helper()
	root := repoRootForTest()
	crate := sparseProducerCrate
	manifest := crate + "/Cargo.toml"
	m := readCargoManifest(t, filepath.Join(root, filepath.FromSlash(manifest)))

	binRel, ok := m.bins["embed-core-sparse"]
	if !ok {
		t.Fatalf("%s has no [[bin]] named embed-core-sparse: the reader is stale, or the producer moved", manifest)
	}
	binFile := path.Join(crate, binRel)
	if binFile != sparseProducerBin {
		t.Fatalf("the manifest builds embed-core-sparse from %s, sparseProducerBin says %s: the identity would "+
			"drop the binary's own source as \"another binary\"", binFile, sparseProducerBin)
	}
	if len(m.pathDeps) > 0 {
		t.Fatalf("%s now has path dependencies %v: their sources are compiled into embed-core-sparse and "+
			"sparseProducerImpl must name them (and this test must learn to walk them)", manifest, m.pathDeps)
	}

	files := map[string]bool{manifest: true}
	lock := nearestLockfile(root, crate)
	if lock == "" {
		t.Fatalf("no Cargo.lock decides what %s resolves", manifest)
	}
	files[lock] = true
	for _, f := range rustModuleClosure(t, root, path.Join(crate, m.libPath)) {
		files[f] = true
	}
	for _, f := range rustModuleClosure(t, root, binFile) {
		files[f] = true
	}
	build := m.build
	if build == "" && !m.hasBuildF && fileExists(filepath.Join(root, filepath.FromSlash(crate), "build.rs")) {
		build = "build.rs"
	}
	if build != "" {
		files[path.Join(crate, build)] = true
	}

	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	// The floor: the manifest, the lockfile, the library and the binary. Fewer means
	// the reader found nothing, and every assertion over it would pass.
	if len(out) < 4 {
		t.Fatalf("derived a closure of %d file(s) (%v): the reader is stale", len(out), out)
	}
	return out
}

// TestTheSparseProducerIsTheClosureCargoCompiles fails when a file cargo compiles
// into embed-core-sparse — or resolves it from — is missing from the declarations
// that decide whether sparse postings are recomputed, or when the identity counts
// a file that is not in it.
//
// Three places must agree with the manifest:
//
//	stepSparse / stepSparse-etsi   a change must make the step run at all
//	build-sparse                   a change must rebuild the binary
//	sparseProducerIdentity         a change must make the run re-encode
//
// The identity is held to the closure EXACTLY, in both directions. Missing a file
// is the silent staleness this exists to end; counting one more — embed-core's
// other binary, src/bin/check.rs — re-encodes both corpora on the GPU and re-pushes
// both layers for an edit that cannot reach a posting.
func TestTheSparseProducerIsTheClosureCargoCompiles(t *testing.T) {
	closure := sparseProducerClosure(t)

	for _, s := range []*Step{stepSparse(corpus3GPP()), stepSparse(corpusETSI()), stepBuildSparse()} {
		for _, f := range closure {
			if !declaresPath(s.Impl, f) {
				t.Errorf("%s does not declare %s, which cargo compiles into embed-core-sparse (or resolves it "+
					"from): a change there leaves the step SKIPping over postings from the previous producer",
					s.Name, f)
			}
		}
	}

	c := &Ctx{Root: repoRootForTest()}
	cur, err := sparseProducerIdentity(c, "model")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(cur.Impl))
	for k := range cur.Impl {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(closure, "\n") {
		t.Errorf("the sparse producer's identity hashes\n  %v\nand cargo compiles embed-core-sparse from\n  %v\n"+
			"a file only in the second is a producer change nothing re-encodes for; a file only in the first "+
			"re-encodes both corpora for an edit that cannot reach a posting", got, closure)
	}
}

// TestEveryFileOfTheProducerMovesItsIdentity is the behavioural half: on a copy of
// the real closure, editing any one of its files changes the identity, and editing
// the crate's other binary does not.
func TestEveryFileOfTheProducerMovesItsIdentity(t *testing.T) {
	closure := sparseProducerClosure(t)
	real := repoRootForTest()
	root := t.TempDir()
	other := sparseProducerCrate + "/src/bin/check.rs"
	for _, f := range append(append([]string(nil), closure...), other) {
		b, err := os.ReadFile(filepath.Join(real, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, filepath.FromSlash(f)), string(b))
	}
	c := &Ctx{Root: root}
	base, err := sparseProducerIdentity(c, "model")
	if err != nil {
		t.Fatal(err)
	}

	edit := func(f string) string {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(f))
		orig, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		write(t, p, string(orig)+"\n// edited\n")
		defer write(t, p, string(orig))
		cur, err := sparseProducerIdentity(c, "model")
		if err != nil {
			t.Fatal(err)
		}
		return cur.digest()
	}
	for _, f := range closure {
		if edit(f) == base.digest() {
			t.Errorf("editing %s leaves the sparse producer's identity unchanged: its postings would be kept", f)
		}
	}
	if edit(other) != base.digest() {
		t.Errorf("editing %s, a binary embed-core-sparse does not compile, changes the identity: both "+
			"corpora would be re-encoded for nothing", other)
	}
	// And a different model is a different producer, with the code unchanged.
	withModel, err := sparseProducerIdentity(c, "another-model")
	if err != nil {
		t.Fatal(err)
	}
	if withModel.digest() == base.digest() {
		t.Error("a new sparse model leaves the producer's identity unchanged")
	}
	// A lockfile that disappears is an error, not a smaller identity.
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(sparseProducerCrate+"/Cargo.lock"))); err != nil {
		t.Fatal(err)
	}
	if _, err := sparseProducerIdentity(c, "model"); err == nil {
		t.Error("the identity was computed without the lockfile: a missing Cargo.lock would read as a producer change, or none")
	}
}

// -------------------------------------------------- the decision itself

func producerFixture(model string, files map[string]string) sparseProducer {
	impl := map[string]string{}
	for k, v := range files {
		impl[k] = v
	}
	return sparseProducer{Model: model, Impl: impl}
}

var producerFiles = map[string]string{
	sparseProducerCrate + "/Cargo.toml":         "toml1",
	sparseProducerCrate + "/Cargo.lock":         "lock1",
	sparseProducerCrate + "/src/lib.rs":         "lib1",
	sparseProducerBin:                           "bin1",
	sparseProducerCrate + "/src/ort_backend.rs": "ort1",
}

func TestSparseReencodeReasonNamesEveryCaseThatReencodes(t *testing.T) {
	cur := producerFixture("m1", producerFiles)
	same := producerFixture("m1", producerFiles)
	lockMoved := producerFixture("m1", producerFiles)
	lockMoved.Impl[sparseProducerCrate+"/Cargo.lock"] = "lock0"

	for _, tc := range []struct {
		name string
		st   *sparseProducerState
		want string // substring; "" means the postings are current
	}{
		{"the producer on record is the current one", &sparseProducerState{Producer: &same}, ""},
		{"no record", nil, "no record"},
		{"a re-encode that died", &sparseProducerState{Pending: true, Producer: &same}, "did not complete"},
		{"a record naming no producer", &sparseProducerState{}, "names no producer"},
		{"the lockfile moved", &sparseProducerState{Producer: &lockMoved}, "rust/embed-core/Cargo.lock"},
		{"the model moved", &sparseProducerState{Producer: func() *sparseProducer {
			p := producerFixture("m0", producerFiles)
			return &p
		}()}, "m0 -> m1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sparseReencodeReason(tc.st, cur)
			if tc.want == "" && got != "" {
				t.Fatalf("re-encodes (%q) postings the current producer wrote", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("reason %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// THE FIRST RUN AFTER THIS LANDED MUST NOT RE-ENCODE A CURRENT CORPUS: that is two
// GPU campaigns and 42 GB of layers for postings nothing changed. The last sparse
// record is the evidence — and only as far as it goes.
func TestSparseAdoptionTrustsTheLastRunOnlyAsFarAsItSaw(t *testing.T) {
	cur := producerFixture("m1", producerFiles)
	// What every sparse record carried before this change: the sources (and the
	// other binary), never the manifest or the lockfile.
	lastRun := func() *Record {
		return &Record{Step: "sparse", Status: StatusSuccess, StartedAt: time.Unix(0, 0),
			Environment: map[string]string{"sparse_identity": "m1"},
			Impl: map[string]string{
				sparseProducerCrate + "/src/lib.rs":         "lib1",
				sparseProducerBin:                           "bin1",
				sparseProducerCrate + "/src/ort_backend.rs": "ort1",
				sparseProducerCrate + "/src/bin/check.rs":   "whatever",
				"rust/store/src/vectors.rs":                 "whatever",
			}}
	}

	t.Run("it saw the same sources and model", func(t *testing.T) {
		_, unrecorded, ok := sparseAdoption(lastRun(), cur)
		if !ok {
			t.Fatal("refused to adopt a producer the last run saw file for file — two GPU campaigns for nothing")
		}
		want := []string{sparseProducerCrate + "/Cargo.lock", sparseProducerCrate + "/Cargo.toml"}
		if strings.Join(unrecorded, ",") != strings.Join(want, ",") {
			t.Errorf("unrecorded = %v, want %v: the log must say what was adopted without evidence", unrecorded, want)
		}
	})
	t.Run("no sparse run ever happened here", func(t *testing.T) {
		if _, _, ok := sparseAdoption(nil, cur); !ok {
			t.Fatal("a seeded corpus is re-encoded on arrival: the seed contract is that the expensive half declines")
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
	}{
		{"a producer source moved since", func(r *Record) { r.Impl[sparseProducerCrate+"/src/lib.rs"] = "lib0" }},
		{"the sparse model moved since", func(r *Record) { r.Environment["sparse_identity"] = "m0" }},
		{"the record never saw the producer's binary", func(r *Record) { delete(r.Impl, sparseProducerBin) }},
		{"it saw a lockfile other than this one", func(r *Record) { r.Impl[sparseProducerCrate+"/Cargo.lock"] = "lock0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := lastRun()
			tc.mutate(r)
			if _, _, ok := sparseAdoption(r, cur); ok {
				t.Fatal("adopted a producer the last run's record contradicts: its postings would be kept")
			}
		})
	}
}

// The adoption is persisted, so it is decided once and not re-derived from a record
// the next run overwrites; and a state file nobody can read proves nothing.
func TestSparseProducerStateIsPersistedAndAnUnreadableOneReencodes(t *testing.T) {
	c, _ := newTestCtx(t)
	cur := producerFixture("m1", producerFiles)
	tgt := corpus3GPP()

	st, err := loadSparseProducerState(c, tgt, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if why := sparseReencodeReason(st, cur); why != "" {
		t.Fatalf("the first run re-encodes: %s", why)
	}
	if _, err := os.Stat(sparseProducerStatePath(c, tgt)); err != nil {
		t.Fatalf("the adoption was not persisted: %v", err)
	}
	// The ETSI arm keeps its own record: one corpus's postings say nothing of the other's.
	if sparseProducerStatePath(c, corpusETSI()) == sparseProducerStatePath(c, tgt) {
		t.Fatal("both arms share one producer record")
	}

	write(t, sparseProducerStatePath(c, tgt), "{not json")
	st, err = loadSparseProducerState(c, tgt, cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sparseReencodeReason(st, cur) == "" {
		t.Fatal("an unreadable producer record was read as a current one")
	}
}
