package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// holdImageToolchain makes publish's toolchain probe answer m (a constant, complete
// answer when m is nil) for the rest of the test. The tests about publish's knobs
// call Extra dozens of times, each would otherwise run bash and six version
// commands, and PATH — which they probe — would then move the fingerprint through
// the go it finds rather than through anything they are testing.
func holdImageToolchain(t *testing.T, m map[string]string) {
	t.Helper()
	if m == nil {
		m = map[string]string{}
		for _, k := range imageToolchainRequired {
			m[k] = "held-" + k
		}
	}
	prev := imageToolchain
	imageToolchain = func(*Ctx) (map[string]string, error) {
		out := map[string]string{}
		for k, v := range m {
			out[k] = v
		}
		return out, nil
	}
	t.Cleanup(func() { imageToolchain = prev })
}

// A TOOLCHAIN CHANGE REPLAYS PUBLISH, AND THE RECORD NAMES THE TOOL.
//
// This is the whole point: a Go whose compress/gzip writes other bytes re-uploads
// the 42 GB corpus, and before image_toolchain.go the plan said "fingerprint
// unchanged" and the record said nothing.
func TestAToolchainChangeReplaysPublish(t *testing.T) {
	c := publishCtx(t)
	s := publishStep(t)
	answer := func(goVersion string) map[string]string {
		m := map[string]string{}
		for _, k := range imageToolchainRequired {
			m[k] = "v-" + k
		}
		m["go"] = goVersion
		return m
	}
	holdImageToolchain(t, answer("go version go1.26.3 windows/amd64"))
	before, err := s.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range imageToolchainRequired {
		if before[imageToolchainPrefix+k] == "" {
			t.Errorf("publish's Extra does not record %s%s: %v", imageToolchainPrefix, k, before)
		}
	}
	holdImageToolchain(t, answer("go version go1.27.0 windows/amd64"))
	after, err := s.Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, after) {
		t.Fatal("a Go upgrade leaves publish's determinants unchanged: its gzip writes every layer blob, " +
			"and the next publish would re-upload the corpus with nothing saying why")
	}
	if after["toolchain_go"] != "go version go1.27.0 windows/amd64" {
		t.Errorf("the record names go as %q", after["toolchain_go"])
	}

	// A probe that cannot answer stops the plan instead of recording a guess.
	imageToolchain = func(*Ctx) (map[string]string, error) { return nil, fmt.Errorf("no answer") }
	if _, err := s.Extra(c); err == nil {
		t.Fatal("publish fingerprinted with no toolchain answer")
	}
}

// THE PARSER REFUSES WHAT WOULD LOSE A DETERMINANT.
func TestParseImageToolchainIsStrict(t *testing.T) {
	full := "go=go version go1\nzig=0.13.0\nrustc=rustc 1\ncargo=cargo 1\ncrane=0.20.2\nsysroot=absent\n"
	if m, err := parseImageToolchain(strings.ReplaceAll(full, "\n", "\r\n")); err != nil || m["zig"] != "0.13.0" {
		t.Fatalf("a CRLF answer does not parse: %v %v", m, err)
	}
	if m, err := parseImageToolchain(full + "strip=gnu 2.40\n"); err != nil || m["strip"] != "gnu 2.40" {
		t.Errorf("a tool the script adds must be folded in too: %v %v", m, err)
	}
	for name, out := range map[string]string{
		"a required tool missing": strings.Replace(full, "zig=0.13.0\n", "", 1),
		"a tool twice":            full + "go=go version go2\n",
		"an empty value":          strings.Replace(full, "crane=0.20.2", "crane=", 1),
		"not a name=value line":   full + "warning: something\n",
	} {
		if m, err := parseImageToolchain(out); err == nil {
			t.Errorf("%s: parsed as %v", name, m)
		}
	}
}

// THE PROBE ANSWERS FOR THE TOOLS THE SCRIPT'S OWN RESOLUTION FINDS — here, a copy
// of the real script in a root whose every tool is a fake with a known version, so
// that the answer can only have come from the script resolving them the way the
// build does: zig out of <root>/.local/toolchain/zig-*, crane out of
// <root>/.local/bin, the sysroot out of <root>/.local/toolchain/sysroot-linux, and
// go, rustc and cargo off the PATH it builds under.
func TestTheProbeReportsTheToolsTheScriptResolves(t *testing.T) {
	root := fakeImageRoot(t)
	c := &Ctx{Root: root}

	t.Setenv("FAKE_GO_VERSION", "go version go0.0.1-fake")
	got, err := probeImageToolchain(c)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"go":    "go version go0.0.1-fake",
		"zig":   "0.0.1-fake-zig",
		"rustc": "rustc 0.0.1-fake",
		"cargo": "cargo 0.0.1-fake",
		"crane": "v0.0.1-fake-crane",
		"sysroot": "libstdc++.so.6=" + sha256Hex("fake libstdc++") +
			" libgomp.so.1=" + sha256Hex("fake libgomp"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--print-toolchain answered\n\t%v\nwant\n\t%v", got, want)
	}

	t.Setenv("FAKE_GO_VERSION", "go version go0.0.2-fake")
	again, err := probeImageToolchain(c)
	if err != nil {
		t.Fatal(err)
	}
	if again["go"] != "go version go0.0.2-fake" {
		t.Fatalf("the probe did not follow the go on the script's PATH: %q", again["go"])
	}

	// A tool that is THERE but gives no version fails the probe: recording
	// "absent" for a broken zig would move the fingerprint twice for nothing.
	broken := filepath.Join(root, ".local", "toolchain", "zig-fake", "zig.exe")
	write(t, broken, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if m, err := probeImageToolchain(c); err == nil {
		t.Fatalf("a zig that is present and will not say its version was recorded as %q", m["zig"])
	}
}

// NOTHING THE PLAN RUNS MAY WRITE. The probe runs on every plan that contains
// publish — possibly while another `goal run` is publishing out of .local/image,
// which the script deletes first thing. So --print-toolchain must leave the tree
// exactly as it found it, in a root where every check the build makes after it
// passes — so that a mode which forgot to exit WOULD go on to write.
func TestPrintToolchainExitsBeforeTheBuildWrites(t *testing.T) {
	root := fakeImageRoot(t)
	before := snapshotTree(t, root)
	if _, err := probeImageToolchain(&Ctx{Root: root}); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("build-image.sh --print-toolchain changed the tree it was run in:\n\tbefore %v\n\tafter  %v",
			before, after)
	}
}

// THE REAL SCRIPT, IN THIS CHECKOUT, ANSWERS EVERY REQUIRED TOOL IN THE SHAPE THAT
// TOOL PRINTS — or "absent" where this machine lacks it (a worktree has no
// .local/toolchain; the pipeline's checkout has all of them).
func TestTheImageToolchainIsTheScriptsOwnAnswer(t *testing.T) {
	root, err := filepath.Abs(repoRootForTest())
	if err != nil {
		t.Fatal(err)
	}
	got, err := probeImageToolchain(&Ctx{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	shape := map[string]*regexp.Regexp{
		"go":      regexp.MustCompile(`^go version go\d`),
		"zig":     regexp.MustCompile(`^\d+\.\d+`),
		"rustc":   regexp.MustCompile(`^rustc \d+\.\d+`),
		"cargo":   regexp.MustCompile(`^cargo \d+\.\d+`),
		"crane":   regexp.MustCompile(`^v?\d+\.\d+`),
		"sysroot": regexp.MustCompile(`^libstdc\+\+\.so\.6=[0-9a-f]{64} libgomp\.so\.1=[0-9a-f]{64}$`),
	}
	for _, k := range imageToolchainRequired {
		v := got[k]
		if v != "absent" && !shape[k].MatchString(v) {
			t.Errorf("%s=%q is neither \"absent\" nor what %s prints", k, v, k)
		}
		t.Logf("%s=%s", k, v)
	}
}

// fakeImageRoot is a checkout holding a copy of the real build-image.sh and
// toolchain-env.sh, and a fake of every tool the image build resolves, each
// printing a known version: zig, crane and the sysroot where the script looks
// for them under its root; go, rustc and cargo first on PATH.
func fakeImageRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{buildImageScript, "scripts/local/toolchain-env.sh"} {
		write(t, filepath.Join(root, filepath.FromSlash(rel)), readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(rel))))
	}
	script := func(p, body string) {
		write(t, p, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script(filepath.Join(root, ".local", "toolchain", "zig-fake", "zig.exe"), `echo 0.0.1-fake-zig`)
	script(filepath.Join(root, ".local", "bin", "crane.exe"), `echo v0.0.1-fake-crane`)
	write(t, filepath.Join(root, ".local", "toolchain", "sysroot-linux", "lib", "libstdc++.so.6"), "fake libstdc++")
	write(t, filepath.Join(root, ".local", "toolchain", "sysroot-linux", "lib", "libgomp.so.1"), "fake libgomp")

	bin := t.TempDir()
	// Every fake answers its version and fails anything else, so a build that ran
	// past the probe would stop at its first compile — after having written.
	script(filepath.Join(bin, "go"), `[ "$1" = version ] && echo "${FAKE_GO_VERSION:-go version go0.0.1-fake}" || exit 1`)
	script(filepath.Join(bin, "rustc"), `[ "$1" = --version ] && echo "rustc 0.0.1-fake" || exit 1`)
	script(filepath.Join(bin, "cargo"), `[ "$1" = --version ] && echo "cargo 0.0.1-fake" || exit 1`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// snapshotTree lists every directory under root, and every file with its size and
// mtime. A directory's own mtime is left out: NTFS reports it lazily through the
// parent's listing, so it moves between two walks with nothing written — measured,
// on this test's first run. A write shows up as a new name or a file that changed.
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out = append(out, filepath.ToSlash(rel)+"/")
			return nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %d", filepath.ToSlash(rel), st.Size(), st.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
