package bootstrap

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestORTPinsAgreeWithFetchModel fails when the sha256 this package pins for an
// ONNX Runtime tarball differs from the one scripts/fetch-model.sh pins for it.
//
// THREE PLACES FETCH THE SAME NATIVE LIBRARY, AND TWO OF THEM CARRY A PIN TABLE.
// FetchORT verifies against ortSHA256 here; scripts/fetch-model.sh verifies the
// local embedder's download against its own `case` table; and
// scripts/local/build-image.sh — which until 2026-09-11 verified nothing — now
// reads fetch-model.sh's table. fetch-model.sh's comment says its pins "must match
// internal/bootstrap/models.go", and until this test nothing checked that: a bump
// made in one table and forgotten in the other would leave one path refusing a
// tarball the other accepts, and the image and the served binary loading different
// bytes under the same version number. ORT is dlopen'd into the server process, so
// the pins are the one place a swapped tarball is caught.
func TestORTPinsAgreeWithFetchModel(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "fetch-model.sh"))
	if err != nil {
		t.Fatalf("read scripts/fetch-model.sh: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*(onnxruntime-[A-Za-z0-9._-]+)\)\s*ORT_SHA=([0-9a-f]{64})\s*;;`)
	shell := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		shell[m[1]] = m[2]
	}
	// A reader that finds nothing would make every comparison below vacuous.
	if len(shell) == 0 {
		t.Fatal("read no ORT pins out of scripts/fetch-model.sh; the pattern is stale and " +
			"this test would pass while checking nothing")
	}
	if len(ortSHA256) == 0 {
		t.Fatal("ortSHA256 is empty; FetchORT would refuse every tarball")
	}

	for pkg, want := range ortSHA256 {
		got, ok := shell[pkg]
		if !ok {
			t.Errorf("internal/bootstrap pins %s but scripts/fetch-model.sh does not: the local "+
				"embedder would refuse a tarball the served binary accepts", pkg)
			continue
		}
		if got != want {
			t.Errorf("%s: internal/bootstrap pins %s, scripts/fetch-model.sh pins %s — two "+
				"checksums for one native library", pkg, want, got)
		}
	}
	for pkg := range shell {
		if _, ok := ortSHA256[pkg]; !ok {
			t.Errorf("scripts/fetch-model.sh pins %s but internal/bootstrap does not: FetchORT "+
				"would refuse a tarball the local embedder accepts", pkg)
		}
	}
}
