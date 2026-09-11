package goal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// goBuildSpec is one binary a step compiles or runs, with what decides its package
// graph: the build tags and the environment it is compiled under.
//
// WHY THE GRAPH NEEDS BOTH. A build constraint decides which files of a package
// exist, and so which packages it imports. internal/rerank/rerank_onnx.go is
// `//go:build onnx` and imports internal/onnxrt; `go list -deps ./cmd/server`
// without the tag does not contain internal/onnxrt at all. The closure tests ran
// that untagged, host-OS query until 2026-09-11 while the image's server is built
// `-tags "onnx,embed_ffi"` for GOOS=linux, so publish and smoke were held to a
// graph one package smaller than the binary that ships.
type goBuildSpec struct {
	Pkg  string   // as the build names it: ./cmd/server
	Tags string   // the -tags value, "" for none
	Env  []string // the GOOS=, GOARCH=, CGO_ENABLED= ... the build sets
	From string   // where the spec was read, for the failure message
}

func (b goBuildSpec) String() string {
	s := b.Pkg
	if b.Tags != "" {
		s += " -tags " + b.Tags
	}
	if len(b.Env) > 0 {
		s = strings.Join(b.Env, " ") + " " + s
	}
	return s + " (" + b.From + ")"
}

// goConstraintEnv are the variables that decide which files of a package build.
// CC, CGO_LDFLAGS and the like change how a binary links, never what it imports.
var goConstraintEnv = map[string]bool{
	"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GOFLAGS": true,
	"GOEXPERIMENT": true, "GOAMD64": true, "GOARM": true, "GOARM64": true, "GO386": true,
}

// goFlagTakesValue are the go build flags whose value is the next word, so that
// word is not read as a package.
var goFlagTakesValue = map[string]bool{
	"o": true, "p": true, "C": true, "ldflags": true, "gcflags": true, "asmflags": true,
	"gccgoflags": true, "buildmode": true, "compiler": true, "installsuffix": true,
	"mod": true, "modfile": true, "overlay": true, "pkgdir": true, "toolexec": true,
	"pgo": true, "coverpkg": true, "covermode": true,
}

// goBuildsIn reads every `go build`, `go run` and `go install` out of a bash
// script, one spec per package, with the tags and constraint variables that
// command sets. A value it would have to expand ($VAR) is an error: this test
// cannot know what it evaluates to, and guessing is how a graph goes wrong.
func goBuildsIn(src, from string) ([]goBuildSpec, error) {
	var out []goBuildSpec
	for _, c := range readShellCommands(src) {
		for j := 0; j+1 < len(c.Words); j++ {
			if c.Words[j] != "go" {
				continue
			}
			verb := c.Words[j+1]
			if verb != "build" && verb != "run" && verb != "install" {
				break
			}
			at := fmt.Sprintf("%s:%d", from, c.Line)
			var env []string
			// The command's own assignments, and those of an `env A=B go build`.
			for _, kv := range append(slices.Clone(c.Env), c.Words[:j]...) {
				name, val, ok := strings.Cut(kv, "=")
				if !ok || !goConstraintEnv[name] {
					continue
				}
				if strings.ContainsAny(val, "$`") {
					return nil, fmt.Errorf("%s sets %s from %q, which this test cannot evaluate", at, name, val)
				}
				env = append(env, name+"="+val)
			}
			var tags string
			var pkgs []string
			args := c.Words[j+2:]
		flags:
			for k := 0; k < len(args); k++ {
				a := args[k]
				name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
				switch {
				case !strings.HasPrefix(a, "-"):
					pkgs = append(pkgs, a)
					if verb == "run" {
						break flags // what follows the package is the program's own arguments
					}
				case name == "tags":
					if !hasVal {
						if k+1 == len(args) {
							return nil, fmt.Errorf("%s passes -tags with no value", at)
						}
						k++
						val = args[k]
					}
					tags = val
				case goFlagTakesValue[name] && !hasVal:
					k++
				}
			}
			if strings.ContainsAny(tags, "$`") {
				return nil, fmt.Errorf("%s builds with -tags %q, which this test cannot evaluate", at, tags)
			}
			for _, p := range pkgs {
				out = append(out, goBuildSpec{Pkg: p, Tags: tags, Env: env, From: at})
			}
			break
		}
	}
	return out, nil
}

// buildImageGoBuilds is every Go package build-image.sh compiles, each with the
// tags and target that build uses, read out of the script: the cross-compiled
// server the image ships, and the host tools that build it (zigcc, elfneeded,
// imgtar). A new build, or a changed -tags, is judged without editing this file.
func buildImageGoBuilds(t *testing.T) []goBuildSpec {
	t.Helper()
	src := readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(buildImageScript)))
	specs, err := goBuildsIn(src, buildImageScript)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatalf("found no go build in %s; the reader is stale and this test would pass while "+
			"checking nothing", buildImageScript)
	}
	return specs
}

// imageServerBuild is the build of the server the image ships.
func imageServerBuild(t *testing.T) goBuildSpec {
	t.Helper()
	var found []goBuildSpec
	for _, b := range buildImageGoBuilds(t) {
		if b.Pkg == "./cmd/server" {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s builds ./cmd/server %d time(s); this test needs exactly one to know which server "+
			"the image ships", buildImageScript, len(found))
	}
	return found[0]
}

// buildGoTagSets are the tag sets build-go can compile with.
//
// build-go passes os.Getenv("GOTAGS") and nothing else, and what sets it is
// scripts/local/toolchain-env.sh, which `make` sources before it starts goal
// (GOAL_ENV in the Makefile): duckdb_use_lib on Windows with a libduckdb, none
// elsewhere. So the sets are every literal `${GOTAGS:-…}` default that file gives,
// the empty one included, plus GOTAGS as this process sees it. Reading the file
// instead of copying the two values here follows shellDefault's rule: a second
// copy of a default is a second default.
func buildGoTagSets(t *testing.T) []string {
	t.Helper()
	const rel = "scripts/local/toolchain-env.sh"
	src := readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(rel)))
	re := regexp.MustCompile(`\$\{GOTAGS:-([^}]*)\}`)
	var sets []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			if strings.ContainsAny(m[1], "$`\"'\\") {
				t.Fatalf("%s gives GOTAGS the computed default %q, which this test cannot evaluate", rel, m[1])
			}
			if !slices.Contains(sets, m[1]) {
				sets = append(sets, m[1])
			}
		}
	}
	if len(sets) == 0 {
		t.Fatalf("%s gives GOTAGS no default; the reader is stale, or build-go's tags are decided "+
			"somewhere this test does not look", rel)
	}
	if env := os.Getenv("GOTAGS"); !slices.Contains(sets, env) {
		sets = append(sets, env)
	}
	return sets
}

// buildGoSpecs are the builds build-go makes of the named binaries: each under
// every tag set it can use, for the host it runs on.
func buildGoSpecs(t *testing.T, bins ...string) []goBuildSpec {
	t.Helper()
	var out []goBuildSpec
	for _, b := range bins {
		if !slices.Contains(goBins, b) {
			t.Fatalf("build-go does not build %s (goBins): the step that runs it gets it from somewhere "+
				"this test does not read", b)
		}
		for _, tags := range buildGoTagSets(t) {
			out = append(out, goBuildSpec{Pkg: "./cmd/" + b, Tags: tags, From: "build-go, GOTAGS=" + tags})
		}
	}
	return out
}

var (
	goListMu    sync.Mutex
	goListCache = map[string][]string{}
)

// goListModuleDeps lists, relative to the module root, every package of THIS
// module in the graph of b, under b's own tags and environment.
func goListModuleDeps(t *testing.T, b goBuildSpec) []string {
	t.Helper()
	goListMu.Lock()
	defer goListMu.Unlock()
	key := fmt.Sprint(b.Pkg, "|", b.Tags, "|", b.Env)
	if deps, ok := goListCache[key]; ok {
		return deps
	}
	args := []string{"list", "-deps"}
	if b.Tags != "" {
		args = append(args, "-tags", b.Tags)
	}
	args = append(args, b.Pkg)
	cmd := exec.Command("go", args...)
	// From the repository root: the package is relative, and running from this
	// package's directory is what once made TestPublishCoversEveryPackageTheServerLinks
	// skip itself.
	cmd.Dir = repoRootForTest()
	// A later duplicate wins in exec.Cmd.Env, so the build's own GOOS overrides the
	// host's.
	cmd.Env = append(os.Environ(), b.Env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s failed for %s: %v", strings.Join(args, " "), b, err)
	}
	const mod = "github.com/kodflow/3gpp-mcp/"
	var rels []string
	for _, line := range strings.Split(string(out), "\n") {
		if p := strings.TrimSpace(line); strings.HasPrefix(p, mod) {
			rels = append(rels, strings.TrimPrefix(p, mod))
		}
	}
	if len(rels) == 0 {
		t.Fatalf("go list -deps named no package of this module for %s; the reader is broken and every "+
			"assertion over it would pass while checking nothing", b)
	}
	goListCache[key] = rels
	return rels
}

// requireGo skips when there is no go on PATH to ask for a package graph.
func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH — this test compares against `go list -deps`")
	}
}

// checkDeclaresWhatItsBinariesLink holds a step's Impl to the package graphs of the
// builds it judges or ships, in BOTH directions.
//
// Forward: every package of this module a build links is fingerprinted, or an edit
// there reaches the binary through build-go, a Tool that invalidates no consumer,
// while the step keeps its verdict. why says what that costs for this step.
//
// Backward: every Go package directory the Impl names under cmd/ or internal/ is
// linked by at least one of the builds. A stale entry replays the step for a change
// it cannot see — and, for the image's server, it is how a graph read without the
// right tags shows itself: internal/onnxrt is declared, and the untagged graph does
// not contain it.
func checkDeclaresWhatItsBinariesLink(t *testing.T, step string, impl []string, builds []goBuildSpec, why string) {
	t.Helper()
	var linked []string
	for _, b := range builds {
		for _, rel := range goListModuleDeps(t, b) {
			if !declaresPath(impl, rel) {
				t.Errorf("%s links %s, which %s does not fingerprint: %s", b, rel, step, why)
			}
			if !slices.Contains(linked, rel) {
				linked = append(linked, rel)
			}
		}
	}
	root := repoRootForTest()
	for _, d := range impl {
		if !strings.HasPrefix(d, "cmd/") && !strings.HasPrefix(d, "internal/") {
			continue
		}
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(d))); err != nil || !st.IsDir() {
			continue
		}
		if !slices.ContainsFunc(linked, func(rel string) bool { return rel == d || strings.HasPrefix(rel, d+"/") }) {
			var names []string
			for _, b := range builds {
				names = append(names, b.String())
			}
			t.Errorf("%s fingerprints %s, which none of the builds it answers for links (%s): an edit there "+
				"replays the step for a change it cannot see", step, d, strings.Join(names, "; "))
		}
	}
}

// TestTheGoBuildReaderReadsTagsAndTarget pins goBuildsIn on the shapes
// build-image.sh uses: a cross-compile whose constraints sit in front of it over
// continuation lines, a -o whose value is not a package, and a `go run` whose
// trailing words belong to the program.
func TestTheGoBuildReaderReadsTagsAndTarget(t *testing.T) {
	src := "CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \\\n" +
		"  CC=\"$CC_SHIM\" CGO_LDFLAGS=\"-L$X -l:libstdc++.so.6\" \\\n" +
		"  go build -tags \"onnx,embed_ffi\" -trimpath \\\n" +
		"    -ldflags \"-s -w -X main.Version=$VERSION\" \\\n" +
		"    -o \"$STAGE/mcp-3gpp\" ./cmd/server\n" +
		"go run ./scripts/local/elfneeded \"$STAGE/mcp-3gpp\" --require-sonames\n" +
		"go build -tags=a,b -o x ./cmd/one ./cmd/two\n" +
		"V=\"$(go build ./cmd/three)\"\n"
	specs, err := goBuildsIn(src, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range specs {
		got = append(got, s.String())
	}
	want := []string{
		"CGO_ENABLED=1 GOOS=linux GOARCH=amd64 ./cmd/server -tags onnx,embed_ffi (fixture:1)",
		"./scripts/local/elfneeded (fixture:6)",
		"./cmd/one -tags a,b (fixture:7)",
		"./cmd/two -tags a,b (fixture:7)",
		"./cmd/three (fixture:8)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("read\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, bad := range []string{"GOOS=$T go build ./cmd/x\n", "go build -tags \"$GOTAGS\" ./cmd/x\n"} {
		if _, err := goBuildsIn(bad, "fixture"); err == nil {
			t.Errorf("goBuildsIn accepted %q: it would list a graph under a value it never evaluated", bad)
		}
	}
}
