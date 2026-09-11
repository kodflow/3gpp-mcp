package goal

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestPublishDeclaresEveryScriptTheImageBuildReads fails when build-image.sh reads
// a file under scripts/ that the publish step does not name in its Impl.
//
// THE DEFECT THIS PINS. publish declared build-image.sh and the two helpers someone
// remembered (imgtar, zigcc). The script also reads the ORT pin table
// (fetch-model.sh), the data contract (data-contract.sh), the loader check
// (elfneeded), the toolchain fetch that decides which libstdc++ the binary links
// (fetch-linux-toolchain.sh) and the environment it builds under
// (toolchain-env.sh). An edit to any of them changes what is baked or whether it
// is let through, and none of them moved publish's fingerprint — the step reported
// the previous image as current. Review of #324 found the first; reading the
// script found the other four.
//
// It reads the SCRIPT, not a list kept beside it, for the same reason the knob test
// next door does: a list drifts from the thing it describes, and a script cannot.
func TestPublishDeclaresEveryScriptTheImageBuildReads(t *testing.T) {
	root := repoRootForTest()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(buildImageScript)))
	if err != nil {
		t.Fatal(err)
	}
	var referenced []string
	re := regexp.MustCompile(`scripts/[A-Za-z0-9_./-]+`)
	for _, line := range strings.Split(string(b), "\n") {
		// A path named only in a comment is prose, not a dependency.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, p := range re.FindAllString(line, -1) {
			p = strings.TrimRight(p, ".")
			if !slices.Contains(referenced, p) {
				referenced = append(referenced, p)
			}
		}
	}
	if len(referenced) < 2 {
		t.Fatalf("found %d scripts/ path(s) in %s; the reader is stale and this test would pass "+
			"while checking nothing", len(referenced), buildImageScript)
	}

	impl := stepPublish().Impl
	for _, p := range referenced {
		// Only paths that exist are dependencies; a string that merely looks like a
		// path (a usage message, a glob) is not.
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			continue
		}
		if !slices.Contains(impl, p) {
			t.Errorf("%s reads %s, and publish's Impl does not name it: editing it changes the "+
				"image or whether it is let through, and publish would report the old one as current",
				buildImageScript, p)
		}
	}
	for _, f := range []string{"go.mod", "go.sum"} {
		if !slices.Contains(impl, f) {
			t.Errorf("publish does not declare %s: the image's binary is compiled inside this step "+
				"from the module graph, so a dependency bump would ship without replaying it", f)
		}
	}
}

// TestSmokeDeclaresTheModuleGraph fails when smoke stops naming go.mod and go.sum.
//
// smoke judges binaries that build-go compiled, and build-go is a Tool dep: a dirty
// Tool dep deliberately invalidates no consumer. So a DuckDB bump rebuilt server.exe
// and bench.exe with a different engine and smoke kept the verdict the old engine
// earned — including the retrieval gate's.
func TestSmokeDeclaresTheModuleGraph(t *testing.T) {
	impl := stepSmoke().Impl
	for _, f := range []string{"go.mod", "go.sum"} {
		if !slices.Contains(impl, f) {
			t.Errorf("smoke does not declare %s: a dependency bump would keep the old verdict", f)
		}
	}
}

// TestPublishDeclaresEveryCrateTheImageBuildCompiles fails when build-image.sh
// compiles a Rust crate whose sources, manifest or lockfile publish does not name.
//
// THE DEFECT THIS PINS, found by review on 2026-09-11. The script builds
// rust/embed-core --features ort and ships it as /usr/local/lib/libembed_core.so,
// and publish declared none of the crate. The steps that did cover it are all
// Tools (build-rust hashes rust/, build-serve and build-sparse its src), and a
// dirty Tool invalidates no consumer — so a fix to the query embedder, or an ort
// bump in its manifest, left publish "fingerprint unchanged" and the previous
// cdylib on the registry as current.
//
// It reads every --manifest-path the script passes, so the NEXT cargo build added
// to it fails here until publish declares that crate too. The lockfile required
// is the one cargo obeys: the nearest Cargo.lock above the crate, which for
// embed-core — excluded from the rust/ workspace — is its own, and for a workspace
// member would be rust/Cargo.lock.
func TestPublishDeclaresEveryCrateTheImageBuildCompiles(t *testing.T) {
	root := repoRootForTest()
	impl := stepPublish().Impl
	crates := 0
	for _, inv := range buildImageCargoInvocations(t) {
		manifest := flagValue(inv.Args, "--manifest-path")
		if manifest == "" {
			t.Errorf("%s:%d runs `cargo %s` with no --manifest-path: this test cannot tell which "+
				"crate it compiles into the image, so it cannot hold publish to it",
				buildImageScript, inv.Line, strings.Join(inv.Args, " "))
			continue
		}
		crates++
		crate := path.Dir(manifest)
		want := []string{crate + "/src", manifest}
		if lock := nearestLockfile(root, crate); lock != "" {
			want = append(want, lock)
		} else {
			t.Errorf("%s:%d compiles %s and no Cargo.lock above it decides its versions: the image "+
				"would carry whatever cargo resolved that day", buildImageScript, inv.Line, crate)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(crate), "build.rs")); err == nil {
			want = append(want, crate+"/build.rs")
		}
		for _, w := range want {
			if !declaresPath(impl, w) {
				t.Errorf("%s:%d compiles %s into the image, and publish's Impl does not name %s: "+
					"editing it changes what ships while publish reports the previous image as current",
					buildImageScript, inv.Line, crate, w)
			}
		}
	}
	if crates == 0 {
		t.Fatalf("found no --manifest-path in %s; the reader is stale and this test would pass "+
			"while checking nothing", buildImageScript)
	}
}

// TestEveryCargoBuildInTheImageIsLocked fails when build-image.sh runs a cargo
// command that resolves dependencies without --locked.
//
// Without the flag cargo may re-resolve and rewrite the lockfile as a side effect
// of the build. It did for rust/discover/Cargo.lock on 2026-09-10, mid-step, and
// #323 closed that for build-rust and test; this script's cdylib build was left
// open. Since publish fingerprints rust/embed-core/Cargo.lock, a rewrite there
// would ship versions no commit names AND move publish's fingerprint behind its
// back, replaying the whole publish on the next plan.
func TestEveryCargoBuildInTheImageIsLocked(t *testing.T) {
	invs := buildImageCargoInvocations(t)
	if len(invs) == 0 {
		t.Fatalf("found no cargo command in %s; the reader is stale and this test would pass "+
			"while checking nothing", buildImageScript)
	}
	for _, inv := range invs {
		// --frozen is --locked plus --offline, so it satisfies the rule too.
		if !slices.Contains(inv.Args, "--locked") && !slices.Contains(inv.Args, "--frozen") {
			t.Errorf("%s:%d runs `cargo %s` without --locked: cargo may rewrite the lockfile publish "+
				"fingerprints, and the image would carry versions no commit names",
				buildImageScript, inv.Line, strings.Join(inv.Args, " "))
		}
	}
}

// cargoInvocation is one cargo command build-image.sh runs that resolves the
// dependency graph.
type cargoInvocation struct {
	Line int      // 1-based line the command starts on
	Args []string // the words after `cargo`, subcommand first
}

// cargoResolves are the cargo subcommands that resolve dependencies — and so read,
// and without --locked may rewrite, a Cargo.lock. `cargo --version` and the like
// are not commands this test has anything to say about.
var cargoResolves = map[string]bool{
	"build": true, "test": true, "run": true, "check": true, "bench": true,
	"doc": true, "rustc": true, "clippy": true, "install": true, "fetch": true,
	"metadata": true, "tree": true,
}

// buildImageCargoInvocations reads every dependency-resolving cargo command out of
// build-image.sh, the way bash reads it.
//
// LINE BY LINE WOULD NOT DO. The cdylib build is one command over six lines — four
// environment assignments, then `cargo build` on one line and --manifest-path on
// the next — so a per-line reader could not say which crate a flag belongs to, or
// that a --locked two lines down is part of the same command. Continuations are
// joined first. A comment is never continued: bash ends it at the newline whatever
// its last character is. The file is read LF-normalised, because the checkout the
// pipeline tests in has kept CRLF files before (#325).
//
// It finds `cargo` as a bare word or opening a $( ) or backtick substitution. A
// cargo reached through a variable ("$CARGO build") is invisible to it; the script
// has none, and the count checks in the callers fail if every invocation vanishes.
func buildImageCargoInvocations(t *testing.T) []cargoInvocation {
	t.Helper()
	lines := strings.Split(readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(buildImageScript))), "\n")
	var out []cargoInvocation
	for i := 0; i < len(lines); i++ {
		start := i
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			continue
		}
		cmd := strings.TrimRight(lines[i], " \t")
		for strings.HasSuffix(cmd, `\`) && i+1 < len(lines) {
			i++
			cmd = strings.TrimSuffix(cmd, `\`) + " " + strings.TrimRight(lines[i], " \t")
		}
		words := strings.Fields(cmd)
		for j := 0; j+1 < len(words); j++ {
			if strings.TrimLeft(words[j], "$(`") != "cargo" || !cargoResolves[words[j+1]] {
				continue
			}
			out = append(out, cargoInvocation{Line: start + 1, Args: words[j+1:]})
			break
		}
	}
	return out
}

// flagValue is the value of a long flag in an argument list, in either spelling
// cargo accepts ("--flag value" and "--flag=value"), with shell quoting and a
// leading $ROOT/ removed so the answer is a repository-relative path.
func flagValue(args []string, flag string) string {
	for i, a := range args {
		var v string
		switch {
		case a == flag && i+1 < len(args):
			v = args[i+1]
		case strings.HasPrefix(a, flag+"="):
			v = strings.TrimPrefix(a, flag+"=")
		default:
			continue
		}
		v = strings.Trim(v, `"'`)
		for _, prefix := range []string{"$ROOT/", "${ROOT}/", "./"} {
			v = strings.TrimPrefix(v, prefix)
		}
		return v
	}
	return ""
}

// nearestLockfile is the repository-relative path of the Cargo.lock cargo would
// obey for the crate at crateDir: its own if it has one, else the first one above
// it, which is where a workspace keeps the lock of all its members. Empty when
// there is none below the repository root.
func nearestLockfile(root, crateDir string) string {
	for d := crateDir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d), "Cargo.lock")); err == nil {
			return d + "/Cargo.lock"
		}
	}
	return ""
}
