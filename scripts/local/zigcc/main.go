// Command zigcc is the C compiler/linker shim that lets this Windows machine
// cross-build the Linux artefacts the container image needs, with no Docker, no
// WSL and no administrator rights.
//
//	zigcc  [args…]   → zig cc  -target <T> [args…]
//	zigcxx [args…]   → zig c++ -target <T> [args…]   (argv[0] ends in "xx" or "++")
//
// It exists because two callers disagree about how to spell a target triple.
// zig wants "x86_64-linux-gnu.2.36" (a glibc version may be pinned); cc-rs, the
// build-script compiler driver behind several Rust crates, passes
// "--target=x86_64-unknown-linux-gnu" of its own accord. Handed both, zig fails
// with "unable to parse target query 'x86_64-unknown-linux-gnu':
// UnknownOperatingSystem" — the LLVM-style triple with a vendor field that zig's
// parser does not take. So this shim drops any --target/-target the caller
// supplies and substitutes the one it was configured with, which is the single
// place the real target is decided.
//
// Configuration is environment, not flags, because the callers (go build's CC,
// cargo's linker) own the command line:
//
//	ZIG        path to zig.exe            (required)
//	ZIG_TARGET target triple for zig      (default x86_64-linux-gnu.2.36)
//
// Why 2.36: the runtime image is debian:bookworm-slim, whose glibc is 2.36.
// Building against a NEWER glibc than the runtime has is the classic way to get
// a binary that links here and dies with "version `GLIBC_2.xx' not found" there,
// so the floor is pinned to the image rather than left to the host.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	zig := os.Getenv("ZIG")
	if zig == "" {
		fmt.Fprintln(os.Stderr, "zigcc: ZIG is not set (path to zig.exe)")
		os.Exit(2)
	}
	target := os.Getenv("ZIG_TARGET")
	if target == "" {
		target = "x86_64-linux-gnu.2.36"
	}

	// C or C++ driver, decided by how this binary was invoked — the same shim
	// serves CC and CXX, and cc-rs picks one by name.
	self := strings.ToLower(os.Args[0])
	driver := "cc"
	if strings.Contains(self, "xx") || strings.Contains(self, "++") {
		driver = "c++"
	}

	args := []string{driver, "-target", target}
	for i := 1; i < len(os.Args); i++ {
		a := os.Args[i]
		switch {
		case strings.HasPrefix(a, "--target="), strings.HasPrefix(a, "-target="):
			// dropped: ours wins, see the package comment
		case a == "-target" || a == "--target":
			i++ // drop its value too
		default:
			args = append(args, a)
		}
	}

	cmd := exec.Command(zig, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "zigcc:", err)
		os.Exit(1)
	}
}
