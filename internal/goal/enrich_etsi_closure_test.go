package goal

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ENRICH-ETSI DECLARES WHAT ITS BINARY IS BUILT FROM, AND THE LIST IS DERIVED.
//
// The step runs one binary, ingest-glossary, and until 2026-09-11 it named its
// sources by hand: the binary, glossary.rs, etsi.rs and the store library. That
// list missed rust/parse/src/lib.rs, where parse_html_clauses — the walker that
// turns every ETSI deliverable into the clauses the glossary is mined from —
// lives, and html_bytes.rs, which decodes every file before it is parsed. A fix
// to either changed the ETSI glossary and left this step SKIPPED, the stale
// vocabulary reported as current. The narrow direction is the dangerous one.
//
// A hand-written list is what drifted, so this test does not hold one. It reads
// the Rust sources the way the compiler resolves paths — from the binary, every
// file a closure file names (`parse3gpp::x`, `store_rs::X`, `crate::x`,
// `super::`, a module declared in the crate root, `include_str!`), the crates
// they come from through the manifests' path dependencies — and requires the
// step's Impl to cover every file it reaches, plus the manifests and lockfile
// that decide how those files are compiled.
//
// WHAT IT DOES NOT FOLLOW IS A BARE `mod x;`. Every module of a crate is compiled
// into it, but a module the binary's code never names cannot change what the
// binary does: `impl Store` blocks in vectors.rs add methods, they cannot alter
// replace_mined_acronyms. Following declarations would make the closure "every
// file of every linked crate", which is the declaration this repository keeps
// removing because it replays steps for edits they cannot see.
//
// Since ingest-glossary writes nothing when the glossary is unchanged, an extra
// replay here costs about two minutes and zero bytes; a missing file costs a
// wrong glossary. The derivation errs toward the first.
func TestEnrichETSIDeclaresEverythingItsBinaryIsBuiltFrom(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	closure := rustSourceClosure(t, root, "rust/ingest", "rust/ingest/src/bin/ingest_glossary.rs")

	// NOT VACUOUS. A deriver that silently found nothing would make every
	// declaration pass; these are the files the binary names outright.
	for _, must := range []string{
		"rust/ingest/src/bin/ingest_glossary.rs",
		"rust/parse/src/lib.rs",
		"rust/parse/src/glossary.rs",
		"rust/parse/src/etsi.rs",
		"rust/parse/src/html_bytes.rs",
		"rust/store/src/lib.rs",
		"internal/store/schema.sql",
		"rust/ingest/Cargo.toml",
		"rust/Cargo.lock",
	} {
		if !containsString(closure, must) {
			t.Errorf("the derived closure lacks %s — the deriver is broken, not the step: %v", must, closure)
		}
	}

	impl := stepEnrich(corpusETSI()).Impl
	for _, f := range closure {
		if !implCoversFile(impl, f) {
			t.Errorf("enrich-etsi runs ingest-glossary, which is built from %s, and does not declare it — "+
				"an edit there changes the ETSI glossary and leaves this step skipped", f)
		}
	}
	t.Logf("ingest-glossary is built from %d file(s): %s", len(closure), strings.Join(closure, " "))
}

// implCoversFile reports whether an Impl entry names the file or a directory
// holding it.
func implCoversFile(impl []string, f string) bool {
	for _, e := range impl {
		e = strings.TrimSuffix(filepath.ToSlash(e), "/")
		if f == e || strings.HasPrefix(f, e+"/") {
			return true
		}
	}
	return false
}

// rustCrate is one crate of the workspace, as its manifest describes it.
type rustCrate struct {
	dir  string   // repo-relative, e.g. "rust/parse"
	lib  string   // the name code uses for it, e.g. "parse3gpp", "store_rs"
	deps []string // lib names of its path dependencies
	mods []string // modules its crate root declares
}

var (
	manifestName = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)
	manifestDep  = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_-]+)\s*=\s*\{[^}\n]*\bpath\s*=\s*"([^"]+)"`)
	modDecl      = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+([a-z_][a-z0-9_]*)\s*;`)
	includeMacro = regexp.MustCompile(`include_(?:str|bytes)!\(\s*"([^"]+)"\s*\)`)
)

// loadRustCrates reads every rust/*/Cargo.toml. The lib name is the [lib] name
// when there is one, else the package name with '-' turned into '_' — cargo's
// own rule.
func loadRustCrates(t *testing.T, root string) map[string]*rustCrate {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join(root, "rust", "*", "Cargo.toml"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no Rust manifest under %s/rust (%v)", root, err)
	}
	byDir := map[string]*rustCrate{}
	depPaths := map[string][]string{}
	for _, m := range manifests {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		dir := filepath.ToSlash(mustRel(t, root, filepath.Dir(m)))
		c := &rustCrate{dir: dir}
		if n := manifestName.FindStringSubmatch(manifestSection(s, "package")); n != nil {
			c.lib = strings.ReplaceAll(n[1], "-", "_")
		}
		if n := manifestName.FindStringSubmatch(manifestSection(s, "lib")); n != nil {
			c.lib = strings.ReplaceAll(n[1], "-", "_")
		}
		if c.lib == "" {
			t.Fatalf("%s names no package: the deriver cannot tell which crate it is", m)
		}
		for _, d := range manifestDep.FindAllStringSubmatch(manifestSection(s, "dependencies"), -1) {
			depPaths[dir] = append(depPaths[dir], filepath.ToSlash(filepath.Join(dir, d[2])))
		}
		if src, err := os.ReadFile(filepath.Join(root, dir, "src", "lib.rs")); err == nil {
			for _, m := range modDecl.FindAllStringSubmatch(stripRustComments(string(src)), -1) {
				c.mods = append(c.mods, m[1])
			}
		}
		byDir[dir] = c
	}
	for dir, paths := range depPaths {
		for _, p := range paths {
			if d, ok := byDir[p]; ok {
				byDir[dir].deps = append(byDir[dir].deps, d.lib)
			}
		}
	}
	byLib := map[string]*rustCrate{}
	for _, c := range byDir {
		if other, dup := byLib[c.lib]; dup {
			t.Fatalf("%s and %s both build a library named %q", other.dir, c.dir, c.lib)
		}
		byLib[c.lib] = c
	}
	return byLib
}

// manifestSection returns the body of one `[name]` table of a Cargo.toml — from
// its header to the next header — or "" when there is none. A leading comment
// block is not part of any table, which is what the identity manifest has.
func manifestSection(s, name string) string {
	h := regexp.MustCompile(`(?m)^\[` + regexp.QuoteMeta(name) + `\]\s*$`).FindStringIndex(s)
	if h == nil {
		return ""
	}
	body := s[h[1]:]
	if n := regexp.MustCompile(`(?m)^\[`).FindStringIndex(body); n != nil {
		body = body[:n[0]]
	}
	return body
}

// rustSourceClosure returns, sorted, every repo-relative file the entry source
// reaches, and the manifests that compile them. crateDir is the crate the entry
// belongs to (a binary's crate root is the entry itself).
func rustSourceClosure(t *testing.T, root, crateDir, entry string) []string {
	t.Helper()
	crates := loadRustCrates(t, root)
	var own *rustCrate
	for _, c := range crates {
		if c.dir == crateDir {
			own = c
		}
	}
	if own == nil {
		t.Fatalf("no crate at %s", crateDir)
	}
	seen := map[string]bool{}
	used := map[string]bool{own.dir: true}
	queue := []string{entry}
	add := func(f string) {
		if f == "" || seen[f] {
			return
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			return
		}
		seen[f] = true
		if strings.HasSuffix(f, ".rs") {
			queue = append(queue, f)
		}
	}
	// moduleFile resolves `<crate>::<name>` to the module's file, or "" when the
	// name is an item of the crate root rather than a module.
	moduleFile := func(c *rustCrate, name string) string {
		for _, cand := range []string{c.dir + "/src/" + name + ".rs", c.dir + "/src/" + name + "/mod.rs"} {
			if _, err := os.Stat(filepath.Join(root, cand)); err == nil {
				return cand
			}
		}
		return ""
	}
	seen[entry] = true
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		src := stripRustComments(string(b))
		// Which crate this file belongs to, and whether it is a binary root.
		var in *rustCrate
		for _, c := range crates {
			if strings.HasPrefix(f, c.dir+"/src/") {
				in = c
			}
		}
		isBin := strings.Contains(f, "/src/bin/")
		refer := func(c *rustCrate, rest string) {
			used[c.dir] = true
			add(c.dir + "/src/lib.rs")
			if m := regexp.MustCompile(`^([a-z_][a-z0-9_]*)`).FindString(rest); m != "" {
				add(moduleFile(c, m))
			}
		}
		if in != nil {
			for _, lib := range in.deps {
				dep := crates[lib]
				re := regexp.MustCompile(`\b` + regexp.QuoteMeta(lib) + `\b\s*(?:::\s*([A-Za-z_][A-Za-z0-9_]*)|\s+as\b|;)`)
				for _, m := range re.FindAllStringSubmatch(src, -1) {
					refer(dep, m[1])
				}
			}
			if !isBin {
				for _, m := range regexp.MustCompile(`\b(?:crate|super)::\s*([A-Za-z_][A-Za-z0-9_*]*)`).FindAllStringSubmatch(src, -1) {
					refer(in, m[1])
				}
				for _, mod := range in.mods {
					if regexp.MustCompile(`\b` + mod + `::`).MatchString(src) {
						add(moduleFile(in, mod))
					}
				}
			}
		}
		for _, m := range includeMacro.FindAllStringSubmatch(src, -1) {
			add(filepath.ToSlash(filepath.Join(filepath.Dir(f), m[1])))
		}
	}
	for dir := range used {
		add(dir + "/Cargo.toml")
	}
	add("rust/Cargo.toml")
	add("rust/Cargo.lock")
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// stripRustComments drops line comments — doc comments included, since a path
// in prose is not a path the compiler follows. A "//" preceded by ':' is a URL
// inside a string and is kept.
func stripRustComments(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		for j := 0; j+1 < len(l); j++ {
			if l[j] == '/' && l[j+1] == '/' && (j == 0 || l[j-1] != ':') {
				lines[i] = l[:j]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func mustRel(t *testing.T, base, p string) string {
	t.Helper()
	r, err := filepath.Rel(base, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
