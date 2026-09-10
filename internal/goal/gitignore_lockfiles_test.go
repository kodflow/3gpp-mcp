package goal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryCrateLockfileIsTracked fails when a Rust crate's Cargo.lock is not in
// git.
//
// THE DEFECT THIS PINS, measured 2026-09-10. .gitignore carried a bare
// `Cargo.lock`. Three of the four crates here had been force-added past it, one at
// a time; rust/discover never was. So cargo was free to rewrite that lockfile, and
// it did — DURING `build-rust`, after the step had already hashed it into its
// fingerprint. `build-rust` and `test` both replayed on the next plan for a change
// that appears in no commit, no diff and no `git status`.
//
// IT ASKS GIT, AND THE FIRST VERSION OF THIS TEST DID NOT. That version read
// .gitignore and rejected a list of literal rules, which is a PROXY for the
// property and a leaky one: `rust/*/Cargo.lock` in the root file, or a crate-local
// .gitignore, hides a lockfile without matching any literal in the list, and the
// test goes green. Tracked-ness is the property itself, and it is also exactly
// right about the escape hatch — a force-added file IS tracked despite any rule,
// which is the state three of these four were already in.
//
// A FAILED READ MUST NOT LOOK LIKE A PASS, which was the real objection to
// shelling out, and it is answered by how the failure is handled rather than by
// avoiding git: an unexpected git error FAILS, and a checkout with no repository
// SKIPS loudly. A tarball cannot answer this question, and pretending it did is
// the failure mode being avoided.
func TestEveryCrateLockfileIsTracked(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skip("no .git here, so nothing can be asked about tracking; this is a skip and " +
			"not a pass on purpose")
	}

	// THE REQUIRED SET, NOT THE SET THAT HAPPENS TO EXIST. Walking for lockfiles
	// and checking what it finds cannot notice a lockfile that is GONE — delete
	// rust/Cargo.lock, the three standalone locks still satisfy a non-empty check,
	// and the authoritative lock for store, parse, ingest and identity disappears
	// with both tests green. Deriving the set from the manifest is what makes
	// absence a failure.
	locks := expectedLockfiles(t, root)
	if len(locks) == 0 {
		t.Fatal("derived no required lockfiles from rust/Cargo.toml; either the crates " +
			"moved or this test is looking in the wrong place, and either way it is " +
			"checking nothing")
	}

	for _, rel := range locks {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is missing: %v", rel, err)
			continue
		}
		cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", rel)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err == nil {
			continue
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Skipf("could not run git (%v); this test needs it to mean anything", err)
		}
		t.Errorf("%s is not tracked by git: cargo can rewrite it under build-rust's own "+
			"fingerprint, and the replay that follows appears in no commit and no diff. "+
			"git said: %s", rel, strings.TrimSpace(string(out)))
	}
}

// TestEveryRustCrateHasItsLockfile fails when a crate outside the workspace has no
// Cargo.lock, or a member of it has one.
//
// The test above stops a lockfile being HIDDEN. This one stops it being ABSENT,
// which is the same drift arriving by the other door: a crate with no lockfile
// resolves its dependencies afresh, so two builds of identical sources can produce
// different binaries and nothing would notice.
//
// The workspace is the exception it looks like and not a hole: MEMBERS are locked
// by rust/Cargo.lock and must not carry one of their own — cargo ignores it, and a
// second file that looks authoritative and is not is worse than none.
func TestEveryRustCrateHasItsLockfile(t *testing.T) {
	root := filepath.Join(repoRootForTest(), "rust")
	members := workspaceMembers(t, filepath.Join(root, "Cargo.toml"))
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
		switch {
		case members[e.Name()] && hasLock:
			t.Errorf("rust/%s is a workspace member AND carries its own Cargo.lock; cargo "+
				"resolves it through rust/Cargo.lock, so this file is ignored and misleading",
				e.Name())
		case !members[e.Name()] && !hasLock:
			t.Errorf("rust/%s is outside the workspace and has no Cargo.lock; its dependencies "+
				"resolve afresh on every machine, so identical sources can build different "+
				"binaries", e.Name())
		}
	}
}

// expectedLockfiles derives, from the workspace manifest, every Cargo.lock this
// repository must have: the workspace's own, plus one for each crate the workspace
// excludes. Paths are repo-relative with forward slashes, the form git wants on
// every platform.
//
// THE WORKSPACE ROOT IS THE ONE THAT WAS MISSED. rust/Cargo.lock is the
// authoritative lock for store, parse, ingest and identity — four crates with no
// lockfile of their own by design — and a check built from directory listings walks
// straight past it.
func expectedLockfiles(t *testing.T, root string) []string {
	t.Helper()
	rustDir := filepath.Join(root, "rust")
	members := workspaceMembers(t, filepath.Join(rustDir, "Cargo.toml"))
	if len(members) == 0 {
		t.Fatal("read no workspace members out of rust/Cargo.toml")
	}
	out := []string{"rust/Cargo.lock"}
	entries, err := os.ReadDir(rustDir)
	if err != nil {
		t.Fatalf("read rust/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "target" || members[e.Name()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(rustDir, e.Name(), "Cargo.toml")); err != nil {
			continue
		}
		out = append(out, "rust/"+e.Name()+"/Cargo.lock")
	}
	return out
}

// workspaceMembers reads the `members` array of a cargo workspace manifest.
//
// IT SPANS LINES, because a valid manifest may write the array over several. The
// first version read only the line beginning with `members`, so a reformat cargo
// itself would accept left the set empty — and the caller's "this test would pass
// vacuously" guard then failed a build for a workspace that had not changed.
//
// `exclude` quotes the same crate names and must not be read: taking it for
// membership would report the three correctly-locked standalone crates as carrying
// a misleading file. That is why collection stops at the array's closing bracket
// instead of scanning the whole manifest.
func workspaceMembers(t *testing.T, manifest string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read the rust workspace manifest: %v", err)
	}
	members := map[string]bool{}
	collecting := false
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "#") {
			continue
		}
		if !collecting {
			if !strings.HasPrefix(s, "members") {
				continue
			}
			collecting = true
		}
		// Odd indices are the quoted values; even ones are the punctuation between
		// them.
		for i, part := range strings.Split(s, `"`) {
			if i%2 == 1 && part != "" {
				members[part] = true
			}
		}
		if strings.Contains(s, "]") {
			break
		}
	}
	return members
}
