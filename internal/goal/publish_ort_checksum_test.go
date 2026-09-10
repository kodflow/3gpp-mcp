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
	lines := strings.Split(readLF(t, filepath.Join(root, "scripts", "local", "build-image.sh")), "\n")
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
	// THE VERSION IS VALIDATED BEFORE IT REACHES A sed PROGRAM. ORT_VERSION is an
	// operator override and the pin lookup interpolates it into a sed script, so an
	// unchecked value like `x//;e id;#` runs `id` on the build host (GNU sed's `e`)
	// before any pin check can refuse it. Found by review of #324.
	validated := find("rejects an ORT_VERSION that is not a dotted number",
		regexp.MustCompile(`^\s*''\|\*\[!0-9\.\]\*.*die "ORT_VERSION=`))
	download := find("downloads the ORT tarball", regexp.MustCompile(`curl .*-o "\$STAGE/ort\.tgz"`))
	pinRead := find("reads the ORT pin from fetch-model.sh", regexp.MustCompile(`ORT_SHA=.*scripts/fetch-model\.sh`))
	hash := find("hashes the downloaded tarball", regexp.MustCompile(`sha256sum "\$STAGE/ort\.tgz"`))
	compare := find("compares it to the pin and dies otherwise", regexp.MustCompile(`\[ "\$ORT_GOT" = "\$ORT_SHA" \]`))
	unpack := find("unpacks the tarball into the image", regexp.MustCompile(`untar --in "\$STAGE/ort\.tgz"`))

	if validated > pinRead {
		t.Errorf("build-image.sh validates ORT_VERSION at line %d, after the sed pin lookup at "+
			"line %d interpolates it — the injection is already possible", validated+1, pinRead+1)
	}
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
	fm := readLF(t, filepath.Join(root, "scripts", "fetch-model.sh"))
	v := regexp.MustCompile(`(?m)^ORT_VERSION="\$\{ORT_VERSION:-([0-9][0-9.]*)\}"$`).FindStringSubmatch(fm)
	if v == nil {
		t.Fatal("scripts/fetch-model.sh declares no ORT_VERSION default build-image.sh can read")
	}
	pkg := "onnxruntime-linux-x64-" + v[1]
	pin := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(pkg) + `\)\s*ORT_SHA=([0-9a-f]{64})\s*;;`)
	if pin.FindStringSubmatch(fm) == nil {
		t.Errorf("scripts/fetch-model.sh pins no sha256 for %s, the package the image bakes: every "+
			"publish would die at the ORT step", pkg)
	}
}

// readLF reads a text file with its line endings normalised to LF.
//
// A TEST ABOUT A SCRIPT MUST NOT DEPEND ON HOW GIT CHECKED THE SCRIPT OUT. The
// repository says eol=lf, but a working tree extracted before that attribute
// existed keeps CRLF — measured 2026-09-11 on the build machine's own checkout:
// 211 files, scripts/fetch-model.sh among them, with 180 CRs. A fresh worktree
// is LF. So this test passed in every worktree it was written in and failed the
// pipeline's `test` step on the checkout that actually builds, because the
// version pattern ends in `"$` and a `\r` sits between the quote and the line end.
// The scripts themselves were never affected: Git Bash's sed opens files in text
// mode and strips the CR, which is why every publish read the pin correctly.
func readLF(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
