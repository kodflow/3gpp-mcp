package goal

import (
	"os"
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
