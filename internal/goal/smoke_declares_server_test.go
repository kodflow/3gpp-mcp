package goal

import (
	"os"
	"os/exec"
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
// the server's closure. The image build's own tools (scripts/local/imgtar, zigcc,
// elfneeded) run in no probe, and rust/embed-core is loaded only by the image's
// embed_ffi build, never by the lexical server.exe smoke drives.
func TestSmokeJudgesEveryPackagePublishShips(t *testing.T) {
	root := repoRootForTest()
	smoke := stepSmoke()
	var shipped []string
	for _, p := range stepPublish().Impl {
		if !strings.HasPrefix(p, "cmd/") && !strings.HasPrefix(p, "internal/") {
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
// package graph of the two binaries it runs: server.exe for the probes, bench.exe
// for the retrieval gate.
//
// The test above proves smoke covers what publish DECLARES; this one proves the
// declaration covers what the binaries LINK, so neither a new import in the server
// nor one in bench can sit outside the gate's fingerprint. bench is the half no
// other test reaches: its closure is not publish's business, and smoke's list used
// to leave two of its packages out by argument rather than by measurement.
func TestSmokeDeclaresEveryPackageItsBinariesLink(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH — this test compares against `go list -deps`")
	}
	impl := stepSmoke().Impl
	for _, bin := range []string{"./cmd/server", "./cmd/bench"} {
		deps := moduleDepsOf(t, bin)
		if len(deps) < 2 {
			t.Fatalf("go list -deps %s named %d in-module package(s); the reader is broken and this "+
				"test would pass while checking nothing", bin, len(deps))
		}
		for _, rel := range deps {
			if !declaresPath(impl, rel) {
				t.Errorf("%s links %s, which smoke does not fingerprint: a change there rebuilds the "+
					"binary smoke runs through build-go, a Tool dep, and smoke SKIPs over it", bin, rel)
			}
		}
	}
}

// moduleDepsOf lists, relative to the module root, every package of THIS module
// that `go list -deps pkg` reports.
func moduleDepsOf(t *testing.T, pkg string) []string {
	t.Helper()
	// From the repository root: pkg is relative, and running from this package's
	// directory is what once made TestPublishCoversEveryPackageTheServerLinks skip
	// itself.
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = repoRootForTest()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s failed in %s: %v", pkg, cmd.Dir, err)
	}
	const mod = "github.com/kodflow/3gpp-mcp/"
	var rels []string
	for _, line := range strings.Split(string(out), "\n") {
		if p := strings.TrimSpace(line); strings.HasPrefix(p, mod) {
			rels = append(rels, strings.TrimPrefix(p, mod))
		}
	}
	return rels
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
