package archguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// requestPathPackages are the packages wired into the SERVED request path. Once
// cmd/server hands them a store, they must only ever read it. They are guarded;
// cmd/server itself is NOT (it owns the OpenReadOnly-vs-Open wiring decision and
// legitimately names store.Open behind the --writable maintenance hatch).
var requestPathPackages = []string{
	"internal/mcp",
	"internal/search",
	"internal/releaseview",
}

// storeWriteMethods is the set of write methods on *store.Store. Keep it in sync
// when a write method is added: the guard fails a request-path package that calls
// any of these, so a missing name would only be silently uncaught if a brand-new
// write method were ALSO wired into a handler (the rare regression this guards).
var storeWriteMethods = []string{
	"InsertAPIOperations", "InsertAPISchemas", "ClearAPI", "ClearAPIReleases",
	"UpsertReleases", "ApplyReleaseFreeze", "SetVersionMetadataSource",
	"BuildAndFreezeHNSW", "RebuildHNSW", "BuildHNSW", "PrepareForEmbedUpdate",
	"SetSparse", "SetMeta", "MarkIngestStarted", "MarkIngestDone", "PurgeSpecScope",
	"ReplaceChanges", "ReplaceEvolutions", "ResetIngestLog", "UpsertSpec",
	"UpsertVersion", "InsertClauses", "InsertChanges", "UpsertAcronym",
	"InsertEvolutions", "EnableFTS", "EnableVSS", "SetEmbedding",
	"SetEmbeddingWithHash", "SetEmbeddingsBatch", "InsertEvents", "InsertASN1Types",
	"Reset", "Checkpoint",
}

// TestServedPackagesAreReadOnly fails if any request-path package references the
// read-write store.Open constructor or a store write method in its non-test code.
// This is the lexical, CGO-safe proof of the Phase 11a "serve never writes" invariant.
func TestServedPackagesAreReadOnly(t *testing.T) {
	root := repoRoot(t)

	// `store.Open(` is the rw constructor; `.Method(` matches a write-method call.
	// We accept the small lexical-false-positive risk (a same-named method on
	// another type) in exchange for not needing go/types over CGO — in practice
	// these names are store-specific and unique in the request path.
	openRe := regexp.MustCompile(`\bstore\.Open\(`)
	writeRe := regexp.MustCompile(`\.(` + strings.Join(storeWriteMethods, "|") + `)\(`)

	var violations []string
	for _, pkg := range requestPathPackages {
		dir := filepath.Join(root, pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				code := stripComment(line)
				if openRe.MatchString(code) {
					violations = append(violations, rel(root, path)+":"+strconv.Itoa(i+1)+" calls store.Open (rw) — serve must OpenReadOnly")
				}
				if m := writeRe.FindString(code); m != "" {
					violations = append(violations, rel(root, path)+":"+strconv.Itoa(i+1)+" calls write method "+strings.Trim(m, ".("))
				}
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("served request-path packages must be read-only (Phase 11a / A14); found %d write reference(s):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// TestGuardDetectsViolations proves the guard actually fires (plan Phase 11a
// acceptance: "a write in a handler → guard red"). It exercises the same matchers
// the live scan uses against synthetic good/bad lines, so a future refactor that
// neuters the detection is itself caught.
func TestGuardDetectsViolations(t *testing.T) {
	openRe := regexp.MustCompile(`\bstore\.Open\(`)
	writeRe := regexp.MustCompile(`\.(` + strings.Join(storeWriteMethods, "|") + `)\(`)

	mustFire := []string{
		`	s, _ := store.Open(path)`,
		`	st.InsertClauses(clauses)`,
		`	st.SetEmbeddingsBatch(ctx, ids, vecs, hashes)`,
		`	st.BuildAndFreezeHNSW(ctx, model)`,
	}
	for _, line := range mustFire {
		code := stripComment(line)
		if !openRe.MatchString(code) && writeRe.FindString(code) == "" {
			t.Errorf("guard failed to flag a write reference: %q", line)
		}
	}

	mustPass := []string{
		`	clauses, _ := st.GetClauses(ctx, ids)`,
		`	st.ListReleases(specID)`,
		`	// never call InsertClauses or store.Open from a handler`,
		`	res := st.SearchClauses(ctx, q)`,
	}
	for _, line := range mustPass {
		code := stripComment(line)
		if openRe.MatchString(code) || writeRe.FindString(code) != "" {
			t.Errorf("guard false-positive on read-only/comment line: %q", line)
		}
	}
}

// stripComment drops a trailing line comment so a write-method name mentioned in
// prose ("// never call InsertClauses here") does not trip the lexical guard. It
// is deliberately simple: it does not parse strings, which the request path does
// not use to name these methods.
func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd() // .../internal/archguard
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found walking up from the test dir")
	return ""
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
