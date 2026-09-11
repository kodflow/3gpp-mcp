package goal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// THE TOOLS THAT WRITE THE IMAGE ARE DETERMINANTS OF IT, like the files they read.
//
// publish's fingerprint named every file the image is made of and every knob that
// changes it, and no tool that writes it. Yet four do, and each can move a digest
// while no source file moves:
//
//   - Go compiles imgtar, and imgtar's compress/gzip has written every layer blob
//     since #328. A Go whose gzip emits other bytes for the same tar gives the two
//     corpus layers new digests: the registry dedupes nothing and the next publish
//     re-uploads ~42 GB. imgtar's layer cache does not shield it — the cache key
//     holds imgtar's own executable, which a new Go rebuilds. Go also compiles the
//     server binary.
//   - zig compiles and links that binary; rustc and cargo compile the embed-core
//     cdylib shipped beside it.
//   - crane writes the config and the manifest, whose digest is the image's.
//   - the Debian libstdc++/libgomp the binary links and layer 10 ships are fetched
//     only when absent, so their CONTENT, not the pin in the fetch script, ships.
//
// None of it was recorded. A toolchain upgrade left publish "fingerprint
// unchanged" and the image on the registry built by the previous tools — which is
// right — and then the NEXT publish, replayed for any unrelated reason, re-uploaded
// the corpus with nothing in the plan or the record saying why. This is not a
// correctness defect; it is the provenance failing to name what moved the digests.
//
// THE VERSIONS ARE THE SCRIPT'S, READ WHERE THEY APPLY. build-image.sh resolves its
// own toolchain — it re-sources toolchain-env.sh, puts .local/toolchain/cargo/bin
// first, globs .local/toolchain/zig-*, falls back to crane on PATH — and `go` and
// rustup pick their toolchain from the working directory besides. A Go copy of that
// resolution would be a second one, free to answer for a different go than the one
// that builds. So the script answers: `build-image.sh --print-toolchain` runs the
// resolution the build runs and prints what it found, before anything writes
// (TestPrintToolchainExitsBeforeTheBuildWrites). Every line it prints is folded in,
// so a tool the script starts to report reaches the fingerprint without a Go edit.
//
// WHY NOT Step.Toolchain. That folds toolchain_identity (toolchain-env.sh) in: the
// HOST gcc — which writes no byte of this image — and neither zig, crane nor the
// sysroot. A gcc upgrade would replay a 22-minute publish for nothing, and a zig
// upgrade would still go unrecorded.

// imageToolchainPrefix prefixes each tool's key in publish's Extra, so the record
// reads toolchain_go, toolchain_zig, … beside image_tag and the knobs.
const imageToolchainPrefix = "toolchain_"

// imageToolchainRequired are the tools the script must report. It may report more
// (they are folded in too); it may not stop reporting one of these, or the
// fingerprint would lose a determinant without anything noticing.
var imageToolchainRequired = []string{"go", "zig", "rustc", "cargo", "crane", "sysroot"}

// imageToolchain asks build-image.sh for the toolchain it builds with. A variable so
// that tests about everything else in publish's Extra can hold it constant; the
// real one is exercised by TestTheImageToolchainIsTheScriptsOwnAnswer.
var imageToolchain = probeImageToolchain

// probeImageToolchain runs `bash build-image.sh --print-toolchain` from the checkout
// root, in the environment runPublish hands the script (goal's own, which is where
// c.Run starts it too), and parses its answer.
//
// A script that cannot answer fails the plan rather than recording a guess: a
// fingerprint that said "unknown" today would change the day it answers again, and
// replay a 22-minute publish for a toolchain that never moved.
func probeImageToolchain(c *Ctx) (map[string]string, error) {
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "bash", buildImageScript, "--print-toolchain")
	cmd.Dir = c.Root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s --print-toolchain failed (%v): %s — the tools that write the image "+
			"cannot be named, so publish cannot be fingerprinted", buildImageScript, err, strings.TrimSpace(stderr.String()))
	}
	return parseImageToolchain(string(out))
}

// parseImageToolchain reads the `name=value` lines of --print-toolchain, strictly:
// a malformed or repeated line, an empty value, or a required tool missing is an
// error, because each is a determinant the fingerprint would silently lose.
func parseImageToolchain(out string) (map[string]string, error) {
	got := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		// A blank value names no version, however many spaces it holds (review of
		// #330, CodeRabbit): "zig=   " would otherwise count as zig reported.
		if !ok || k == "" || strings.ContainsAny(k, " \t") || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("%s --print-toolchain printed %q, which is not a name=version line", buildImageScript, line)
		}
		if _, dup := got[k]; dup {
			return nil, fmt.Errorf("%s --print-toolchain reports %s twice", buildImageScript, k)
		}
		got[k] = v
	}
	for _, k := range imageToolchainRequired {
		if _, ok := got[k]; !ok {
			return nil, fmt.Errorf("%s --print-toolchain no longer reports %s: the fingerprint would lose a "+
				"tool that writes the image", buildImageScript, k)
		}
	}
	return got, nil
}

// addImageToolchain folds the toolchain into publish's Extra.
func addImageToolchain(c *Ctx, m map[string]string) error {
	tc, err := imageToolchain(c)
	if err != nil {
		return err
	}
	for k, v := range tc {
		m[imageToolchainPrefix+k] = v
	}
	return nil
}
