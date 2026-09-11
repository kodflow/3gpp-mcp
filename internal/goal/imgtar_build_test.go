package goal

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestImgtarIsTheSameBinaryOnEveryCommit builds imgtar the way build-image.sh
// builds it, at two commits that differ only in an unrelated file, and requires
// the same bytes both times.
//
// WHY IT MATTERS. imgtar keys every cached layer on the hash of its own executable
// (layerKey in scripts/local/imgtar), so that a change to how layers are written
// invalidates them. A plain `go build` in a git checkout stamps vcs.revision,
// vcs.time and vcs.modified into the binary — the imgtar.exe of the 2026-09-11
// publish carries vcs.revision=2cfa4a70… — so every commit, to any file, gave
// imgtar a new hash and every cached layer a new key. The cache of #328 could
// only ever hit on the commit that filled it: the first publish after each merge
// repacked all 42 GB (~12 minutes) into byte-identical layers. (Found by the
// knobs agent, 2026-09-11.)
//
// IN A THROWAWAY REPOSITORY, because go stamps nothing in a git WORKTREE — its
// .git is a file, and go only recognises a .git directory — so a test run from a
// worktree would pass whatever the flags. The publish runs from the main checkout,
// where the stamp is real. imgtar imports only the standard library, so its
// sources build as a module of their own.
//
// A POSITIVE CONTROL: the same sources built with no flags must differ between the
// two commits. Otherwise this environment stamps nothing and "the script's build
// is stable" would be true for the wrong reason.
func TestImgtarIsTheSameBinaryOnEveryCommit(t *testing.T) {
	requireGo(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH: a commit cannot be made, so its effect on the binary cannot be observed")
	}
	root := repoRootForTest()
	const pkg = "./scripts/local/imgtar"

	var words []string
	for _, c := range readShellCommands(readLF(t, filepath.Join(root, filepath.FromSlash(buildImageScript)))) {
		if len(c.Words) > 2 && c.Words[0] == "go" && c.Words[1] == "build" && slices.Contains(c.Words, pkg) {
			if words != nil {
				t.Fatalf("%s builds %s twice; which binary keys the cache is ambiguous", buildImageScript, pkg)
			}
			words = c.Words
		}
	}
	if words == nil {
		t.Fatalf("%s does not `go build %s`; the reader is stale and this test would check nothing",
			buildImageScript, pkg)
	}

	// imgtar's sources, as a module of their own, in a fresh repository.
	repo := t.TempDir()
	src := filepath.Join(root, filepath.FromSlash(pkg))
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "imgtar"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, n))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(`"github.com/kodflow/3gpp-mcp/`)) {
			t.Fatalf("%s imports this module; it no longer builds as a module of its own and this "+
				"test must be rewritten", n)
		}
		if err := os.WriteFile(filepath.Join(repo, "imgtar", n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goLine := ""
	for _, l := range strings.Split(readLF(t, filepath.Join(root, "go.mod")), "\n") {
		if strings.HasPrefix(l, "go ") {
			goLine = l
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module imgtarprobe\n\n"+goLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	commit := func(t *testing.T, msg string) {
		t.Helper()
		run(t, "git", "add", "-A")
		run(t, "git", "-c", "user.name=probe", "-c", "user.email=probe@invalid", "commit", "-q", "-m", msg)
	}
	// The script's arguments, the package and -o pointed into the probe. Any
	// other word the test would have to expand is refused rather than guessed.
	scripted := func(t *testing.T, out string) []string {
		t.Helper()
		args := slices.Clone(words[1:])
		found := false
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "-o" && i+1 < len(args):
				args[i+1] = out
				found = true
				i++
			case args[i] == pkg:
				args[i] = "./imgtar"
			case strings.ContainsAny(args[i], "$`"):
				t.Fatalf("%s builds imgtar with %q, which this test cannot evaluate", buildImageScript, args[i])
			}
		}
		if !found {
			t.Fatalf("%s builds imgtar with no -o; the test cannot find the binary it makes", buildImageScript)
		}
		return args
	}
	read := func(t *testing.T, p string) []byte {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	run(t, "git", "init", "-q")
	commit(t, "A")
	ctlA, outA := filepath.Join(repo, "ctl-a.exe"), filepath.Join(repo, "out-a.exe")
	run(t, "go", "build", "-o", ctlA, "./imgtar")
	run(t, "go", scripted(t, outA)...)

	if err := os.WriteFile(filepath.Join(repo, "UNRELATED.md"), []byte("a commit that touches no Go file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, "B")
	ctlB, outB := filepath.Join(repo, "ctl-b.exe"), filepath.Join(repo, "out-b.exe")
	run(t, "go", "build", "-o", ctlB, "./imgtar")
	run(t, "go", scripted(t, outB)...)

	if bytes.Equal(read(t, ctlA), read(t, ctlB)) {
		t.Skipf("a plain `go build` gives the same binary at two commits here, so this environment stamps "+
			"no revision and the script's flags cannot be observed:\n%s", run(t, "go", "version", "-m", ctlB))
	}
	if !bytes.Equal(read(t, outA), read(t, outB)) {
		t.Fatalf("imgtar built as %s builds it (go %s) differs between two commits that touch no Go "+
			"file — every commit re-keys every cached image layer:\n%s", buildImageScript,
			strings.Join(words[1:], " "), run(t, "go", "version", "-m", outB))
	}
}
