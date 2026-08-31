// Command elfneeded prints an ELF binary's dynamic dependencies, and can assert
// that every one of them is a bare SONAME.
//
//	elfneeded <file> [--require-sonames]
//
// It exists because of a failure that is invisible until the container starts.
// The Linux artefacts are cross-linked on Windows with zig/lld, and when a shared
// library carries no SONAME, lld records THE PATH IT FOUND IT AT as the NEEDED
// entry — here, something like
// "C:/Users/.../target/x86_64-unknown-linux-gnu/release\libembed_core.so". The
// binary links, `file` reports a well-formed ELF, every local check passes, and
// the image then dies at startup because the loader is looking for a Windows path
// that does not exist in the container.
//
// The fix is a -soname on the cdylib; this is the check that proves it took, and
// that will catch it again the next time a dependency is added without one.
// Using debug/elf rather than readelf keeps it working on a machine that has no
// binutils for the target.
package main

import (
	"debug/elf"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: elfneeded <file> [--require-sonames]")
		os.Exit(2)
	}
	path := os.Args[1]
	strict := false
	for _, a := range os.Args[2:] {
		if a == "--require-sonames" {
			strict = true
		}
	}

	f, err := elf.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "elfneeded:", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	libs, err := f.DynString(elf.DT_NEEDED)
	if err != nil {
		fmt.Fprintln(os.Stderr, "elfneeded: no dynamic section:", err)
		os.Exit(1)
	}

	fmt.Printf("elfneeded: %s %s %s\n", path, f.Class, f.Machine)
	bad := []string{}
	for _, l := range libs {
		fmt.Println("  NEEDED", l)
		// A SONAME is a plain file name. Anything carrying a separator — including
		// the backslash a Windows-hosted linker leaves behind — is a build path.
		if strings.ContainsAny(l, `/\`) || strings.Contains(l, ":") {
			bad = append(bad, l)
		}
	}
	if strict && len(bad) > 0 {
		fmt.Fprintf(os.Stderr,
			"\nelfneeded: %d NEEDED entr%s carr%s a PATH rather than a SONAME:\n",
			len(bad), plural(len(bad), "y", "ies"), plural(len(bad), "ies", "y"))
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "  "+b)
		}
		fmt.Fprintln(os.Stderr,
			"\nThe container's loader resolves NEEDED against its own search path, so a\n"+
				"build path from this machine means the image starts and immediately fails.\n"+
				"Build the shared library with -Wl,-soname,<name>.")
		os.Exit(1)
	}
	if strict {
		fmt.Println("elfneeded: every NEEDED entry is a bare SONAME")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
