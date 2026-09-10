package goal

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestImageVerifiesTheORTItBakes fails when build-image.sh stops checking the ONNX
// Runtime tarball between downloading it and unpacking it into the image.
//
// THE DEFECT THIS PINS, found by an independent review on 2026-09-10. The comment
// over the ORT block said "pinned and checksummed exactly as scripts/fetch-model.sh
// pins it", and only the first half was true: the tarball was fetched with a bare
// curl and untarred. internal/bootstrap calls an unverified ORT "an RCE vector" and
// fails closed; fetch-model.sh fails closed; the published image — the one place the
// library actually runs for users — checked nothing.
//
// WHY ORDER, AND NOT JUST PRESENCE. A verification that runs AFTER the untar has
// already put the bytes into the rootfs, and a `set -e` script that dies there still
// leaves a staged tree a later step could pack. The check has to sit between the
// download and the unpack, so that is what is asserted.
//
// It also asserts the pin is READ from fetch-model.sh and that the default version
// has one: a lookup that finds nothing makes build-image.sh die on every publish,
// which is safe but would be discovered 25 minutes into a build rather than here.
func TestImageVerifiesTheORTItBakes(t *testing.T) {
	root := repoRootForTest()
	b, err := os.ReadFile(filepath.Join(root, "scripts", "local", "build-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	find := func(what string, re *regexp.Regexp) int {
		t.Helper()
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if re.MatchString(l) {
				return i
			}
		}
		t.Fatalf("build-image.sh has no line that %s (pattern %q)", what, re)
		return -1
	}
	download := find("downloads the ORT tarball", regexp.MustCompile(`curl .*-o "\$STAGE/ort\.tgz"`))
	pinRead := find("reads the ORT pin from fetch-model.sh", regexp.MustCompile(`ORT_SHA=.*scripts/fetch-model\.sh`))
	hash := find("hashes the downloaded tarball", regexp.MustCompile(`sha256sum "\$STAGE/ort\.tgz"`))
	compare := find("compares it to the pin and dies otherwise", regexp.MustCompile(`\[ "\$ORT_GOT" = "\$ORT_SHA" \]`))
	unpack := find("unpacks the tarball into the image", regexp.MustCompile(`untar --in "\$STAGE/ort\.tgz"`))

	if !(pinRead < download && download < hash && hash < compare && compare < unpack) {
		t.Errorf("build-image.sh must read the pin, download, hash, compare and only then unpack; "+
			"got pin=%d download=%d hash=%d compare=%d unpack=%d (0-based lines)",
			pinRead, download, hash, compare, unpack)
	}
	// The comparison must be able to stop the build: `|| die` on it or the next line.
	window := lines[compare]
	if compare+1 < len(lines) {
		window += lines[compare+1]
	}
	if !strings.Contains(window, "die ") {
		t.Errorf("build-image.sh:%d compares the ORT checksum but does not die on a mismatch", compare+1)
	}

	// The default version must have a pin in fetch-model.sh, or every publish dies.
	fm, err := os.ReadFile(filepath.Join(root, "scripts", "fetch-model.sh"))
	if err != nil {
		t.Fatal(err)
	}
	v := regexp.MustCompile(`(?m)^ORT_VERSION="\$\{ORT_VERSION:-([0-9][0-9.]*)\}"$`).FindStringSubmatch(string(fm))
	if v == nil {
		t.Fatal("scripts/fetch-model.sh declares no ORT_VERSION default build-image.sh can read")
	}
	pkg := "onnxruntime-linux-x64-" + v[1]
	pin := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(pkg) + `\)\s*ORT_SHA=([0-9a-f]{64})\s*;;`)
	if pin.FindStringSubmatch(string(fm)) == nil {
		t.Errorf("scripts/fetch-model.sh pins no sha256 for %s, the package the image bakes: every "+
			"publish would die at the ORT step", pkg)
	}
}
