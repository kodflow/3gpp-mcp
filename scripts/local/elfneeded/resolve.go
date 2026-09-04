package main

import (
	"archive/tar"
	"debug/elf"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// resolveMode answers the one question no check on this machine could answer:
// will the loader find every library the image's own binaries ask for?
//
// WHY IT IS NEEDED AT ALL. There is no container runtime here and no WSL
// distribution (virtualisation is off at the firmware/feature level), so the
// image cannot be started before it is published. Every other check is either
// structural (crane validate: the manifest and blobs are well-formed) or
// semantic-on-the-same-corpus (server-full.exe answers real JSON-RPC). Between
// them sits the failure this closes: an ELF whose DT_NEEDED names a library that
// exists on THIS machine and in no layer of the image. `crane validate` passes,
// the image pulls, and the container dies on its first instruction with
//
//	error while loading shared libraries: libfoo.so.1: cannot open shared object file
//
// The --require-sonames check above is the sibling of this one and NOT a
// substitute: it proves the NEEDED entry is a name rather than a Windows path.
// A perfectly formed SONAME for a library nobody shipped fails exactly the same
// way. It also only ever looked at the server binary, while the cdylib and the
// ONNX Runtime shared objects travel in the image too and are loaded by it.
//
// HOW IT DECIDES. The image's filesystem is the rootfs overlay staged by
// build-image.sh UNION the base image's own layers, so both are read: the
// overlay from disk, the base from the tar `crane export` already produces (the
// build reads /etc/passwd out of it, so exporting it costs nothing extra here).
// A NEEDED entry is satisfied when a file of that name sits in one of the
// loader's real search directories, which is ld.so's default set plus whatever
// --ld-library-path names — the image config sets LD_LIBRARY_PATH=/usr/local/lib
// and that is where libembed_core.so is staged. Comparing basenames anywhere in
// the image would have called a library in an unsearched directory "present",
// which is the kind of check that passes while the container still dies.
//
// It deliberately does NOT resolve transitively into the base's own libraries
// (a NEEDED of a NEEDED): those come from Debian's own dependency graph, which
// apt already satisfied when the base was built. What is unverified is what WE
// add, and that is exactly what this walks.
func resolveMode(args []string) {
	var rootfs, baseTar, ldPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rootfs":
			i++
			if i < len(args) {
				rootfs = args[i]
			}
		case "--base-tar":
			i++
			if i < len(args) {
				baseTar = args[i]
			}
		case "--ld-library-path":
			i++
			if i < len(args) {
				ldPath = args[i]
			}
		}
	}
	if rootfs == "" {
		fmt.Fprintln(os.Stderr, "elfneeded --resolve: --rootfs is required")
		os.Exit(2)
	}

	// The loader's search directories, in the container's own vocabulary. The
	// defaults are ld.so's for a Debian amd64 image; LD_LIBRARY_PATH is prepended
	// because that is what the image config actually sets.
	searchDirs := []string{
		"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu",
		"/lib", "/usr/lib", "/usr/lib64", "/lib64",
	}
	// The value is a CONTAINER path list, and it is written without leading
	// slashes on purpose: this runs under Git Bash, whose MSYS layer rewrites an
	// argument that looks like a Unix absolute path into a Windows one before the
	// binary ever sees it. "/usr/local/lib" arrived here as
	// "C:/…/w64devkit/usr/local/lib", which matched no container directory, so the
	// only library staged outside ld.so's defaults — libembed_core.so, the cdylib
	// the whole semantic arm hangs on — was reported MISSING while sitting exactly
	// where it belongs. A search path that silently loses an entry turns this gate
	// into noise, so a mangled value is a hard error rather than a shorter list.
	// The guard has to run on the WHOLE value, before the split: ":" is both the
	// list separator and the drive-letter separator, so splitting first turns
	// "C:/…/usr/local/lib" into "C" plus a plausible-looking absolute path and the
	// mangling walks straight through.
	if strings.Contains(ldPath, `\`) || (len(ldPath) > 1 && ldPath[1] == ':') {
		fmt.Fprintf(os.Stderr,
			"elfneeded --resolve: --ld-library-path got %q, which is a HOST path.\n"+
				"MSYS rewrites a leading slash; pass the container directories without one\n"+
				"(--ld-library-path usr/local/lib), or set MSYS2_ARG_CONV_EXCL='*'.\n", ldPath)
		os.Exit(2)
	}
	for _, d := range strings.Split(ldPath, ":") {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		searchDirs = append([]string{path.Clean("/" + strings.TrimPrefix(d, "/"))}, searchDirs...)
	}
	inSearchPath := func(containerPath string) bool {
		dir := path.Dir(containerPath)
		for _, d := range searchDirs {
			if dir == d {
				return true
			}
		}
		return false
	}

	// available maps a SONAME to where it was found, so a failure can say which
	// half of the image was expected to carry it.
	available := map[string]string{}
	var elves []string // rootfs-relative paths of the ELF files WE ship

	err := filepath.WalkDir(rootfs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil //nolint:nilerr // an unreadable entry is not a resolution failure
		}
		rel, rerr := filepath.Rel(rootfs, p)
		if rerr != nil {
			return nil //nolint:nilerr
		}
		container := "/" + filepath.ToSlash(rel)
		if !isELF(p) {
			return nil
		}
		elves = append(elves, container)
		if soname := readSoname(p); soname != "" && inSearchPath(container) {
			available[soname] = "overlay:" + container
		}
		// A library is findable by its file name too — ld.so opens the file, it does
		// not read SONAMEs out of a directory — so record both.
		if base := path.Base(container); strings.Contains(base, ".so") && inSearchPath(container) {
			if _, seen := available[base]; !seen {
				available[base] = "overlay:" + container
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "elfneeded --resolve: walking the rootfs:", err)
		os.Exit(1)
	}

	if baseTar != "" {
		n, terr := addTarLibs(baseTar, inSearchPath, available)
		if terr != nil {
			fmt.Fprintln(os.Stderr, "elfneeded --resolve: reading the base image:", terr)
			os.Exit(1)
		}
		fmt.Printf("elfneeded --resolve: base image contributes %d librar%s on the search path\n",
			n, plural(n, "y", "ies"))
	}

	sort.Strings(elves)
	missing := 0
	for _, container := range elves {
		p := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(container, "/")))
		f, oerr := elf.Open(p)
		if oerr != nil {
			continue
		}
		libs, derr := f.DynString(elf.DT_NEEDED)
		_ = f.Close()
		if derr != nil || len(libs) == 0 {
			continue // static, or no dynamic section: nothing to resolve
		}
		fmt.Printf("  %s\n", container)
		// RUNPATH is reported, not enforced. cgo's -Wl,-rpath carries this
		// machine's absolute build directory into the published binary
		// ("/usr/local/lib:C:/Users/…/rust/embed-core/target/release"). The loader
		// takes the entries in order and ignores one that does not exist, so the
		// image works — but a host path inside a shipped artefact is the same
		// family of leak as the SONAME defect --require-sonames exists for, and it
		// tells a puller where this was built. Failing the build over it would
		// block a working image on cosmetics; going unsaid is how it survived.
		if rp := runPath(p); rp != "" && (strings.Contains(rp, `\`) || strings.Contains(rp, ":/")) {
			fmt.Printf("      NOTE    RUNPATH carries a host path: %s\n", rp)
		}
		for _, l := range libs {
			if where, ok := available[l]; ok {
				fmt.Printf("      OK      %-28s %s\n", l, where)
				continue
			}
			fmt.Printf("      MISSING %s\n", l)
			missing++
		}
	}

	if missing > 0 {
		fmt.Fprintf(os.Stderr,
			"\nelfneeded: %d NEEDED entr%s resolve%s to nothing in the image.\n"+
				"The container will pull, start, and die on its first instruction with\n"+
				"\"cannot open shared object file\". Stage the library into the overlay\n"+
				"(as build-image.sh does for libstdc++.so.6 and libgomp.so.1) or link\n"+
				"against one the base already carries.\n",
			missing, plural(missing, "y", "ies"), plural(missing, "s", ""))
		os.Exit(1)
	}
	fmt.Println("elfneeded: every NEEDED entry of every ELF we ship resolves inside the image")
}

// addTarLibs records the libraries the BASE image carries on the loader's search
// path. Members are read by name only — the base is Debian's, already coherent;
// what is being answered is "does this file exist in the image", not "is it well
// formed". Symlinks count: /lib/x86_64-linux-gnu/libm.so.6 is one in most bases,
// and the loader follows it.
func addTarLibs(tarPath string, inSearchPath func(string) bool, available map[string]string) (int, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	n := 0
	tr := tar.NewReader(f)
	for {
		h, terr := tr.Next()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return n, terr
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeSymlink && h.Typeflag != tar.TypeLink {
			continue
		}
		// crane export writes members without a leading slash; on Windows it has
		// been observed writing BACKSLASH separators, which is why imgtar exists.
		// Normalise both before asking where the file lands in the container.
		name := "/" + strings.TrimPrefix(strings.ReplaceAll(h.Name, `\`, "/"), "/")
		base := path.Base(name)
		if !strings.Contains(base, ".so") || !inSearchPath(name) {
			continue
		}
		if _, seen := available[base]; !seen {
			available[base] = "base:" + name
			n++
		}
	}
	return n, nil
}

// isELF reads the four magic bytes. Cheaper and more honest than trusting an
// extension: the server binary has none and the ORT libraries are versioned
// (libonnxruntime.so.1.20.1).
func isELF(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic == [4]byte{0x7f, 'E', 'L', 'F'}
}

// readSoname returns the DT_SONAME of a shared object, or "" when it carries none
// (an executable, or a library built without one — the very defect the strict mode
// above exists to catch on the consuming side).
func readSoname(p string) string {
	f, err := elf.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	names, err := f.DynString(elf.DT_SONAME)
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0]
}

// runPath returns DT_RUNPATH, falling back to the older DT_RPATH. Either can be
// the one a toolchain wrote, and only one of them is usually present.
func runPath(p string) string {
	f, err := elf.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
		if v, err := f.DynString(tag); err == nil && len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}
	return ""
}
