// Command imgtar packs container image layers, and reads a single member out of
// an existing tar. It replaces GNU tar for both jobs on this machine.
//
//	imgtar pack --root <dir> --out <layer.tar> [--uid N --gid N] <path>…
//	imgtar cat   --in <archive.tar> <member>
//	imgtar untar --in <archive.tar[.gz]> --dest <dir> [--strip N]
//
// WHY NOT tar(1). Git for Windows ships GNU tar, and it is wrong here in two
// ways that both produce a broken image rather than an error:
//
//   - It treats an argument beginning "C:" as a REMOTE HOST and tries to reach an
//     rmt server: "tar: Cannot connect to C: resolve failed". --force-local fixes
//     that one.
//   - It then renders and matches member names with BACKSLASHES. Listing a base
//     image shows "etc\.pwd.lock", and `-xO etc/passwd` reports "Not found in
//     archive" for a file that is plainly there. A layer packed that way would
//     carry "usr\local\bin\mcp-3gpp" as a member name, and the container runtime
//     — which splits on "/" — would unpack one file with a backslash-laden name
//     into the root instead of a binary on the PATH. The image would pull, start,
//     and fail with "docker-entrypoint.sh: not found".
//
// archive/tar has neither problem, and lets ownership be set per entry without
// depending on the host's user database: uid/gid 10001 is what the data path and
// the entrypoint expect, and this machine has no such user.
//
// Symlinks are preserved; anything that is not a regular file, directory or
// symlink is skipped loudly rather than silently flattened.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// epoch is the fixed modification time stamped on every packed entry. 2000-01-01
// rather than the Unix epoch because some tools treat a zero timestamp as "unset"
// and substitute the current time, which would silently reintroduce the very
// non-determinism this exists to remove.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "pack":
		pack(os.Args[2:])
	case "cat":
		cat(os.Args[2:])
	case "untar":
		untar(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: imgtar pack --root <dir> --out <layer.tar> [--uid N --gid N] <path>…")
	fmt.Fprintln(os.Stderr, "       imgtar cat  --in <archive.tar> <member>")
	os.Exit(2)
}

func pack(args []string) {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	root := fs.String("root", "", "staged rootfs directory (required)")
	out := fs.String("out", "", "layer tarball to write (required)")
	uid := fs.Int("uid", 0, "owner uid for every entry")
	gid := fs.Int("gid", 0, "owner gid for every entry")
	_ = fs.Parse(args)
	if *root == "" || *out == "" || fs.NArg() == 0 {
		usage()
	}

	f, err := os.Create(*out)
	must(err)
	tw := tar.NewWriter(f)

	// Deterministic: the paths are walked in the order given and lexically within
	// each, and no timestamp from this machine leaks in. Two runs over unchanged
	// content therefore produce the same bytes, so the registry deduplicates the
	// blob and a re-push does not move it again.
	seen := map[string]bool{}
	var n int
	for _, p := range fs.Args() {
		abs := filepath.Join(*root, filepath.FromSlash(p))
		if _, err := os.Lstat(abs); err != nil {
			continue // an optional path (etsi.duckdb, the reranker) simply absent
		}
		must(filepath.Walk(abs, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(*root, path)
			if rerr != nil {
				return rerr
			}
			name := filepath.ToSlash(rel)
			if name == "." || seen[name] {
				return nil
			}
			seen[name] = true

			link := ""
			if fi.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}
			hdr, herr := tar.FileInfoHeader(fi, link)
			if herr != nil {
				return herr
			}
			switch {
			case fi.IsDir():
				hdr.Name = name + "/"
			default:
				hdr.Name = name
			}
			if !fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && !fi.Mode().IsRegular() {
				fmt.Fprintf(os.Stderr, "imgtar: skipping %s (%s)\n", name, fi.Mode())
				return nil
			}
			hdr.Uid, hdr.Gid = *uid, *gid
			hdr.Uname, hdr.Gname = "", ""
			// A FIXED timestamp, not the file's own. This is what makes an
			// unchanged corpus produce a byte-identical layer, and therefore the
			// same digest, and therefore NO upload: the registry already has that
			// blob. Keeping the on-disk mtime would defeat it — the build copies
			// the corpus into a staging tree, and cp stamps "now" on the copy, so
			// an 11 GB layer that has not changed would get a fresh digest and be
			// pushed again on every build.
			//
			// Splitting data from code into two images was the CI-era answer to
			// the same problem ("the bake produces non-reproducible bytes"). One
			// deterministic packer removes the need for the split.
			hdr.ModTime = epoch
			hdr.AccessTime, hdr.ChangeTime = epoch, epoch
			// Windows has no execute bit, so it is set from the path: the two
			// entries under /usr/local/bin are the entrypoint and the server, and
			// a non-executable entrypoint is an image that pulls and cannot run.
			if fi.Mode().IsRegular() && (strings.Contains(name, "bin/") || strings.HasSuffix(name, ".sh")) {
				hdr.Mode = 0o755
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				src, oerr := os.Open(path)
				if oerr != nil {
					return oerr
				}
				_, cerr := io.Copy(tw, src)
				_ = src.Close()
				if cerr != nil {
					return cerr
				}
			}
			n++
			return nil
		}))
	}
	must(tw.Close())
	must(f.Close())

	st, err := os.Stat(*out)
	must(err)
	fmt.Printf("  %s  %d entries  %.1f MiB  uid=%d\n",
		filepath.Base(*out), n, float64(st.Size())/(1<<20), *uid)
}

func cat(args []string) {
	fs := flag.NewFlagSet("cat", flag.ExitOnError)
	in := fs.String("in", "", "tar archive to read (required)")
	_ = fs.Parse(args)
	if *in == "" || fs.NArg() != 1 {
		usage()
	}
	want := norm(fs.Arg(0))

	f, err := os.Open(*in)
	must(err)
	defer func() { _ = f.Close() }()

	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(err)
		// Separators are normalised on the way in because `crane export` run on
		// Windows writes BACKSLASHES into the archive: the base image's password
		// file comes out as the member "etc\\passwd", which neither tar(1) nor a
		// literal comparison finds. The archive is otherwise perfectly readable,
		// so this is a matching problem, not a corrupt input — and it is only ever
		// applied to archives being READ. What imgtar WRITES is always POSIX.
		if norm(hdr.Name) == want {
			_, err := io.Copy(os.Stdout, tr)
			must(err)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "imgtar: %s not found in %s\n", want, *in)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "imgtar:", err)
		os.Exit(1)
	}
}

// norm reduces a tar member name to the portable form: forward slashes, no
// leading "./". See the note in cat about why this is needed on the read side.
func norm(name string) string {
	return strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
}

// untar extracts a (optionally gzipped) tarball into dest, stripping the first
// --strip path components.
//
// Present for the same reason as the rest of this tool: the tar on this machine's
// PATH is w64devkit's BUSYBOX build, which has no --force-local and therefore
// refuses any path beginning "C:" ("Cannot connect to C: resolve failed" is the
// GNU one; busybox simply prints its usage). The ONNX Runtime release tarball has
// to land in the staged rootfs, and going through Go removes the question of
// which tar happens to be first on PATH.
func untar(args []string) {
	fs := flag.NewFlagSet("untar", flag.ExitOnError)
	in := fs.String("in", "", "tarball to extract, .tar or .tar.gz (required)")
	dest := fs.String("dest", "", "destination directory (required)")
	strip := fs.Int("strip", 0, "leading path components to strip")
	_ = fs.Parse(args)
	if *in == "" || *dest == "" {
		usage()
	}

	f, err := os.Open(*in)
	must(err)
	defer func() { _ = f.Close() }()

	var r io.Reader = f
	if strings.HasSuffix(*in, ".gz") || strings.HasSuffix(*in, ".tgz") {
		gz, gerr := gzip.NewReader(f)
		must(gerr)
		defer func() { _ = gz.Close() }()
		r = gz
	}

	tr := tar.NewReader(r)
	n := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(err)

		parts := strings.Split(norm(hdr.Name), "/")
		if len(parts) <= *strip {
			continue
		}
		rel := strings.Join(parts[*strip:], "/")
		if rel == "" {
			continue
		}
		// Refuse to escape dest. An archive from the internet is untrusted input,
		// and ".." in a member name is the oldest way to write outside the tree.
		out := filepath.Join(*dest, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(out)+string(os.PathSeparator),
			filepath.Clean(*dest)+string(os.PathSeparator)) {
			fmt.Fprintf(os.Stderr, "imgtar: refusing %q — it escapes %s\n", hdr.Name, *dest)
			os.Exit(1)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			must(os.MkdirAll(out, 0o755))
		case tar.TypeSymlink:
			must(os.MkdirAll(filepath.Dir(out), 0o755))
			_ = os.Remove(out)
			// Windows needs a privilege for symlinks; the layer packer reads what
			// is on disk, so a copy is the portable stand-in and the image sees a
			// regular file where the tarball had a link.
			if err := os.Symlink(hdr.Linkname, out); err != nil {
				src := filepath.Join(filepath.Dir(out), filepath.FromSlash(hdr.Linkname))
				if b, rerr := os.ReadFile(src); rerr == nil {
					must(os.WriteFile(out, b, 0o644))
				}
			}
			n++
		case tar.TypeReg:
			must(os.MkdirAll(filepath.Dir(out), 0o755))
			w, cerr := os.Create(out)
			must(cerr)
			_, cerr = io.Copy(w, tr)
			must(cerr)
			must(w.Close())
			n++
		}
	}
	fmt.Printf("  extracted %d file(s) from %s\n", n, filepath.Base(*in))
}
