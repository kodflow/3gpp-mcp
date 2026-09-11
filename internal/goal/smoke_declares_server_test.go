package goal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSmokeJudgesEveryPackagePublishShips fails when publish ships a Go package
// that smoke, the gate in front of it, does not fingerprint.
//
// THE DEFECT THIS PINS, found by review on 2026-09-11. publish ships thirteen
// server packages and smoke named five of them. build-go, which rebuilds the
// server smoke starts, is a Tool dep, and a dirty Tool dep invalidates no
// consumer. So an edit confined to internal/subject/li that broke resolve_term
// planned as
//
//	[SKIP] smoke     fingerprint unchanged, outputs present and valid
//	[RUN ] publish   implementation changed: internal/subject/li/…
//
// and the broken server went to the registry recorded as gated, with no probe run
// against it. A gate that watches less than the thing it gates approves whatever
// changed in the difference.
//
// It reads publish's DECLARATION, not serverImplPackages: a package added to
// publish's Impl by hand, beside the function, ships just the same and is held to
// the same rule. What it selects are the Go packages under cmd/ and internal/ —
// the server's closure — minus imageGuardPackages, the two commands publish runs
// on this machine to decide whether to push, which ship nothing and run in no
// probe. The image build's own tools (scripts/local/imgtar, zigcc, elfneeded) run
// in no probe either, and rust/embed-core is loaded only by the image's embed_ffi
// build, never by the lexical server.exe smoke drives.
func TestSmokeJudgesEveryPackagePublishShips(t *testing.T) {
	root := repoRootForTest()
	smoke := stepSmoke()
	var shipped []string
	for _, p := range stepPublish().Impl {
		if !strings.HasPrefix(p, "cmd/") && !strings.HasPrefix(p, "internal/") {
			continue
		}
		if slices.Contains(imageGuardPackages(), p) {
			continue
		}
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !st.IsDir() {
			continue
		}
		shipped = append(shipped, p)
		if !slices.Contains(smoke.Impl, p) {
			t.Errorf("publish ships %s and smoke does not fingerprint it: an edit there rebuilds "+
				"server.exe through build-go, a Tool dep that invalidates no consumer, so smoke SKIPs "+
				"with no probe run and publish republishes the change recorded as gated. Declare it "+
				"through serverImplPackages, the function both steps call", p)
		}
	}
	// COUNT, so the loop cannot pass by finding nothing: a publish that stopped
	// calling serverImplPackages, or a filter above that stopped matching, would
	// leave zero packages checked and every assertion vacuously true.
	if want := len(serverImplPackages()); len(shipped) < want {
		t.Fatalf("found %d server package(s) in publish's Impl, want at least %d (serverImplPackages): "+
			"this test would pass while checking nothing", len(shipped), want)
	}
	if !smoke.ExcludeTests {
		t.Error("smoke declares the server's packages as directories and counts their _test.go: " +
			"every test edit in any of them would replay the gate and re-compose the image")
	}
}

// TestSmokeDeclaresEveryPackageItsBinariesLink holds smoke's Impl to the real
// package graphs of everything it answers for: server.exe for the probes and
// bench.exe for the retrieval gate, as build-go builds them, and the server the
// image ships, which smoke is the last gate in front of.
//
// The test above proves smoke covers what publish DECLARES; this one proves the
// declaration covers what the binaries LINK, so neither a new import in the server
// nor one in bench can sit outside the gate's fingerprint. bench is the half no
// other test reaches: its closure is not publish's business, and smoke's list used
// to leave two of its packages out by argument rather than by measurement.
//
// EACH GRAPH UNDER THE TAGS ITS BINARY IS BUILT WITH, found by review on
// 2026-09-11. This test asked `go list -deps` with no tags, which is none of the
// three builds: build-go passes GOTAGS (duckdb_use_lib on Windows, read out of
// scripts/local/toolchain-env.sh), and the image's server is `-tags
// "onnx,embed_ffi"` for GOOS=linux, read out of build-image.sh. The last one links
// internal/onnxrt, which no untagged graph contains, so smoke and publish both
// left it out and both tests agreed. The union is the right set because smoke
// judges all three: an edit to a package only the image's server links cannot be
// probed by server.exe, and still has to replay the gate in front of the image, or
// publish ships it recorded as gated.
func TestSmokeDeclaresEveryPackageItsBinariesLink(t *testing.T) {
	requireGo(t)
	builds := append(buildGoSpecs(t, "server", "bench"), imageServerBuild(t))
	checkDeclaresWhatItsBinariesLink(t, "smoke", stepSmoke().Impl, builds,
		"a change there rebuilds a binary smoke answers for, through build-go (a Tool dep) or inside "+
			"publish, and smoke SKIPs over it with no probe run")
}

// declaresPath reports whether an Impl list fingerprints rel: named outright, or
// under a declared directory, since Impl walks directories.
func declaresPath(impl []string, rel string) bool {
	for _, d := range impl {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}
