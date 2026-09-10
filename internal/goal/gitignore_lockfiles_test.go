package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitignoreDoesNotHideCrateLockfiles fails when .gitignore carries a rule that
// would hide a Rust crate's Cargo.lock.
//
// THE DEFECT THIS PINS, measured 2026-09-10. .gitignore carried a bare
// `Cargo.lock`. Three of the four crates here had been force-added past it, one at
// a time; rust/discover never was. So cargo was free to rewrite that lockfile, and
// it did — DURING `build-rust`, after the step had already hashed it into its
// fingerprint. `build-rust` and `test` both replayed on the next plan for a change
// that appears in no commit, no diff and no `git status`.
//
// WHY THE RULE AND NOT THE FILE. Adding rust/discover/Cargo.lock alone fixes the
// four crates that exist today and leaves the trap armed for the fifth: a new crate
// arrives, its lockfile is ignored by default, and the same silent drift comes back
// under a different name. The rule is the cause; the missing file was the symptom.
//
// This checks the RULE rather than asking git what is tracked, deliberately. A test
// that shells out to `git ls-files` reports "nothing is ignored" just as cheerfully
// when git is absent, when the checkout is a tarball, or when the command fails for
// any other reason — and a gate that turns a failed read into a pass is worse than
// no gate, which is the same reasoning ReingestedDeliverables states about
// discarding a scan error.
func TestGitignoreDoesNotHideCrateLockfiles(t *testing.T) {
	path := filepath.Join(repoRootForTest(), ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	// Patterns that would match a crate lockfile at any depth. A leading "!" is a
	// negation and re-includes, so it is not a hazard.
	hides := map[string]bool{
		"Cargo.lock":      true,
		"/Cargo.lock":     true,
		"**/Cargo.lock":   true,
		"*.lock":          true,
		"/*.lock":         true,
		"**/*.lock":       true,
		"rust/Cargo.lock": true,
	}
	for i, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "!") {
			continue
		}
		if hides[s] {
			t.Fatalf(".gitignore:%d ignores %q — a Rust crate's lockfile is the record of "+
				"what was built, and an ignored one lets cargo rewrite it under a step's own "+
				"fingerprint with nothing to show for it (build-rust and test, 2026-09-10). "+
				"Track the lockfiles instead.", i+1, s)
		}
	}
}

// TestEveryRustCrateHasItsLockfile fails when a crate under rust/ has a Cargo.toml
// and no Cargo.lock beside it.
//
// The rule above stops a lockfile being HIDDEN. This one stops it being ABSENT,
// which is the same drift arriving by the other door: a crate with no lockfile
// resolves its dependencies afresh, so two builds of identical sources can produce
// different binaries and the pipeline has nothing that would notice.
//
// The workspace is the exception it looks like and not a hole: crates that are
// MEMBERS of rust/Cargo.toml are locked by rust/Cargo.lock and must not carry one
// of their own — cargo ignores it, and a second file that looks authoritative and
// is not is worse than none.
func TestEveryRustCrateHasItsLockfile(t *testing.T) {
	root := filepath.Join(repoRootForTest(), "rust")
	ws, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	if err != nil {
		t.Fatalf("read the rust workspace manifest: %v", err)
	}
	// ONLY the members line. `exclude = ["embedder", "embed-core", "discover"]`
	// quotes the same names, so scanning the whole manifest would call every
	// excluded crate a member — and report the three that correctly carry their own
	// lockfile as carrying a misleading one.
	members := map[string]bool{}
	for _, line := range strings.Split(string(ws), "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "members") {
			continue
		}
		for _, part := range strings.Split(s, `"`)[1:] {
			if name := strings.TrimSpace(part); name != "" && !strings.ContainsAny(name, "[],= ") {
				members[name] = true
			}
		}
	}
	if len(members) == 0 {
		t.Fatal("read no workspace members out of rust/Cargo.toml — this test would pass vacuously")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read rust/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "target" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "Cargo.toml")); err != nil {
			continue
		}
		_, lockErr := os.Stat(filepath.Join(root, e.Name(), "Cargo.lock"))
		hasLock := lockErr == nil
		member := members[e.Name()]
		switch {
		case member && hasLock:
			t.Errorf("rust/%s is a workspace member AND carries its own Cargo.lock; "+
				"cargo resolves it through rust/Cargo.lock, so this file is ignored and "+
				"misleading", e.Name())
		case !member && !hasLock:
			t.Errorf("rust/%s is outside the workspace and has no Cargo.lock; its "+
				"dependencies resolve afresh on every machine, so identical sources can "+
				"build different binaries", e.Name())
		}
	}
}
