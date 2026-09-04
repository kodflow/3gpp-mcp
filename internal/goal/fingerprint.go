package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are never walked when hashing an implementation. They hold generated
// artefacts, runtime state and vendored toolchains — folding them into a
// fingerprint would make every step dirty on every run (a cargo build writes
// into target/, the pipeline writes into .local/), which defeats the whole
// mechanism. This is also the exclusion list any code index must honour.
//
// Matching is by directory NAME, at any depth — see skipDir for the one place
// that is wrong and why.
var skipDirs = map[string]bool{
	".git":         true,
	".local":       true, // runtime state + portable toolchain
	"target":       true, // cargo
	"node_modules": true,
	"data":         true, // corpus, shards, DB, models
	"image-data":   true,
	"bin":          true,
	"dist":         true,
	"vendor":       true,
	"__pycache__":  true,
}

// skipDir decides whether a walked directory is pruned. It exists because
// skipDirs matches a BASENAME at any depth, and exactly one entry — "bin" — is
// ambiguous: ./bin and .local/bin hold built artefacts, but Cargo puts the
// SOURCE of every extra binary of a crate in src/bin.
//
// Pruning src/bin made rust/store/src/bin/{embed_io,merge,overlay,freeze_hnsw}.rs
// invisible to build-rust's fingerprint. A fix confined to one of those files
// left the step reporting VALID, so cargo never re-ran and the stale .exe kept
// being launched — a build that is wrong while the state machine swears it is
// right. That is the failure this whole mechanism exists to prevent, so the
// exclusion is narrowed rather than the file being added to some allow-list.
func skipDir(path, name string) bool {
	if !skipDirs[name] {
		return false
	}
	// A "bin" whose parent is "src" is Cargo's binary-source directory, not an
	// output directory. Everything else named "bin" is still pruned.
	if name == "bin" && filepath.Base(filepath.Dir(path)) == "src" {
		return false
	}
	return true
}

// hashableExt is the set of extensions whose line endings are normalised before
// hashing. A repository checked out on Windows carries CRLF; the same content
// must fingerprint identically on every machine, otherwise "skip when unchanged"
// breaks the moment two people (or a WSL and a Windows shell) look at the same
// tree. .gitattributes pins LF at checkout; this is the belt to that braces, and
// it costs nothing.
var hashableExt = map[string]bool{
	".go": true, ".rs": true, ".sh": true, ".sql": true, ".toml": true,
	".yaml": true, ".yml": true, ".json": true, ".md": true, ".mk": true,
	".mod": true, ".sum": true, ".txt": true, ".asn": true,
}

func newHasher() *hasher { return &hasher{h: sha256.New()} }

// hasher accumulates key=value lines. Feeding KEYS as well as values makes the
// digest unambiguous: two different determinant sets can never collide just
// because their concatenated values happen to line up.
type hasher struct{ h hash.Hash }

func (x *hasher) add(k, v string) { fmt.Fprintf(x.h, "%s=%s\n", k, v) }
func (x *hasher) sum() string     { return hex.EncodeToString(x.h.Sum(nil))[:16] }

// implHash hashes the CONTENT of the files that define a step. Directories are
// walked deterministically (sorted) with skipDirs pruned. A missing path is an
// error, not a silently-empty hash: a typo in a step's Impl list would otherwise
// produce a stable fingerprint that never changes, and the step would never be
// replayed again.
func implHash(root string, patterns []string, excludeTests bool) (string, map[string]string, error) {
	per := map[string]string{}
	var files []string

	for _, p := range patterns {
		abs := filepath.Join(root, filepath.FromSlash(p))
		matches, err := filepath.Glob(abs)
		if err != nil {
			return "", nil, fmt.Errorf("bad impl pattern %q: %w", p, err)
		}
		if len(matches) == 0 {
			if _, err := os.Stat(abs); err != nil {
				return "", nil, fmt.Errorf("impl path %q does not exist (typo? moved?): %w", p, err)
			}
			matches = []string{abs}
		}
		for _, m := range matches {
			st, err := os.Stat(m)
			if err != nil {
				return "", nil, err
			}
			if !st.IsDir() {
				files = append(files, m)
				continue
			}
			err = filepath.WalkDir(m, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if skipDir(path, d.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				if excludeTests && isTestArtefact(path) {
					return nil
				}
				files = append(files, path)
				return nil
			})
			if err != nil {
				return "", nil, err
			}
		}
	}

	sort.Strings(files)
	x := newHasher()
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = f
		}
		rel = filepath.ToSlash(rel)
		sum, err := fileContentHash(f)
		if err != nil {
			return "", nil, err
		}
		per[rel] = sum
		x.add(rel, sum)
	}
	return x.sum(), per, nil
}

// fileContentHash hashes a file, normalising CRLF to LF for text-like content so
// the same bytes-of-meaning hash the same on every platform.
func fileContentHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if hashableExt[strings.ToLower(filepath.Ext(path))] {
		b = normaliseEOL(b)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16], nil
}

func normaliseEOL(b []byte) []byte {
	if !strings.Contains(string(b), "\r\n") {
		return b
	}
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}

// inputsHash fingerprints DATA inputs by size and modification time rather than
// by content. The corpus is ~37 GB of converted HTML: content-hashing it would
// take longer than the ingestion the fingerprint exists to avoid. Size+mtime
// detects every realistic change (a re-conversion always rewrites the file) and
// is what the incremental converters already rely on.
//
// A missing input is recorded as "absent" rather than failing: a step may
// legitimately declare inputs that do not exist yet on a first run.
func inputsHash(paths []string) (string, map[string]string) {
	sort.Strings(paths)
	per := map[string]string{}
	x := newHasher()
	for _, p := range paths {
		st, err := os.Stat(p)
		var v string
		switch {
		case err != nil:
			v = "absent"
		case st.IsDir():
			v = "dir"
		default:
			v = fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UTC().UnixNano())
		}
		key := filepath.ToSlash(p)
		per[key] = v
		x.add(key, v)
	}
	return x.sum(), per
}

// Fingerprint composes the deterministic identity of a step.
//
// Only determinants enter it. DATA dependencies contribute their OWN
// fingerprints, which is what makes invalidation transitive: if merge changes,
// embed's fingerprint changes because merge's did, without embed knowing
// anything about merge's internals.
//
// TOOL dependencies are excluded, and the exclusion is the load-bearing part —
// see Step.Tool. They are ordered and force-built for this step, but a rebuilt
// compiler output is not new provenance for 22 GB of corpus.
func (r *Runner) Fingerprint(s *Step, ctx *Ctx) (string, *Record, error) {
	rec := &Record{
		Step:        s.Name,
		StepVersion: s.Version,
		Deps:        map[string]string{},
		Environment: map[string]string{},
	}

	x := newHasher()
	x.add("step", s.Name)
	x.add("version", fmt.Sprint(s.Version))

	ih, per, err := implHash(ctx.Root, s.Impl, s.ExcludeTests)
	if err != nil {
		return "", nil, fmt.Errorf("step %s: implementation hash: %w", s.Name, err)
	}
	rec.Impl = per
	x.add("impl", ih)

	// Dependencies: their fingerprint, from their persisted record. A dependency
	// that has never run contributes "missing", which correctly makes this step
	// dirty.
	// Alternatives fold in exactly like Deps: a seeded corpus and a merged one
	// are different corpora, so switching producer must replay the step.
	//
	// Tool dependencies are filtered out HERE rather than at the declaration site,
	// so the graph keeps saying the true thing — ingest really does need cargo to
	// have run — while the fingerprint keeps to what can actually change ingest's
	// output. rec.Deps records only what was hashed, so `goal status` and
	// explainDrift never name a determinant that had no vote.
	deps := make([]string, 0, len(s.Deps)+len(s.AnyDeps))
	for _, d := range append(append([]string(nil), s.Deps...), s.AnyDeps...) {
		if dep := r.byName[d]; dep != nil && dep.Tool {
			continue
		}
		deps = append(deps, d)
	}
	sort.Strings(deps)
	for _, d := range deps {
		prev, _ := r.store.Load(d)
		fp := "missing"
		if prev != nil && prev.Status == StatusSuccess {
			fp = publishedProvenance(prev)
		}
		rec.Deps[d] = fp
		x.add("dep:"+d, fp)
	}

	if s.Inputs != nil {
		paths, err := s.Inputs(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("step %s: enumerating inputs: %w", s.Name, err)
		}
		inh, iper := inputsHash(paths)
		rec.Inputs = iper
		x.add("inputs", inh)
	}

	if s.Extra != nil {
		extra, err := s.Extra(ctx)
		if err != nil {
			return "", nil, fmt.Errorf("step %s: extra determinants: %w", s.Name, err)
		}
		keys := make([]string, 0, len(extra))
		for k := range extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			rec.Environment[k] = extra[k]
			x.add("extra:"+k, extra[k])
		}
	}

	// The toolchain identity is deliberately NOT global: a Go or gcc upgrade must
	// rebuild the binaries, but it must not re-download 37 GB of specs nor re-run
	// hours of GPU work whose result does not depend on the compiler.
	if s.Toolchain {
		ti := r.toolchainIdentity()
		rec.Environment["toolchain"] = ti
		x.add("toolchain", ti)
	}

	fp := x.sum()
	rec.Fingerprint = fp
	return fp, rec, nil
}

// isTestArtefact reports whether a path is test-only material. `go build` never
// compiles _test.go, and testdata/ holds fixtures for tests — so for a build step
// neither can change the produced binary. Counting them would relink every binary
// on a test edit, which is exactly the over-invalidation fingerprints exist to
// avoid. The `test` step, which DOES depend on them, simply does not set the flag.
func isTestArtefact(path string) bool {
	if strings.HasSuffix(filepath.Base(path), "_test.go") {
		return true
	}
	return strings.Contains(filepath.ToSlash(path), "/testdata/")
}
