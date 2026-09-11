// Command imgtar packs container image layers, and reads a single member out of
// an existing tar. It replaces GNU tar for both jobs on this machine.
//
//	imgtar pack --root <dir> --out <layer.tar[.gz]> [--uid N --gid N] [--gzip] [--cache <dir>] <path>…
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
	"crypto/sha256"
	"encoding/hex"
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
	gz := fs.Bool("gzip", false, "write the layer gzip-compressed, byte for byte as crane would (see layerGzip)")
	cacheDir := fs.String("cache", "", "reuse the layer written last time when every packed file is unchanged (see layerKey)")
	_ = fs.Parse(args)
	if *root == "" || *out == "" || fs.NArg() == 0 {
		usage()
	}

	entries, err := collect(*root, fs.Args())
	must(err)

	var key, cached string
	if *cacheDir != "" {
		key, err = layerKey(entries, *uid, *gid, *gz)
		must(err)
		cached = filepath.Join(*cacheDir, filepath.Base(*out))
		if hit(cached, key) {
			must(linkOrCopy(cached, *out))
			st, err := os.Stat(*out)
			must(err)
			fmt.Printf("  %s  %d entries  %.1f MiB  uid=%d  (cached: every packed file unchanged)\n",
				filepath.Base(*out), len(entries), float64(st.Size())/(1<<20), *uid)
			return
		}
	}

	// WRITE ASIDE, THEN RENAME. A layer name that exists is a layer that is whole:
	// the cache below links to this file, and a half-written blob reachable under
	// the final name would be reused by the next build as if it were complete.
	tmp := *out + ".tmp"
	must(writeLayer(tmp, entries, *uid, *gid, *gz))
	must(os.Rename(tmp, *out))

	if cached != "" {
		must(store(cached, key, *out))
	}
	st, err := os.Stat(*out)
	must(err)
	fmt.Printf("  %s  %d entries  %.1f MiB  uid=%d\n",
		filepath.Base(*out), len(entries), float64(st.Size())/(1<<20), *uid)
}

// entry is one member of a layer, in the order it is written.
type entry struct {
	name string // member name, "/"-separated; directories end in "/"
	path string // the file on disk
	fi   os.FileInfo
	link string // symlink target
}

// collect walks the paths in the order given and lexically within each — the
// order that makes two packs of unchanged content byte-identical. Anything that
// is not a regular file, directory or symlink is skipped loudly.
func collect(root string, paths []string) ([]entry, error) {
	seen := map[string]bool{}
	var out []entry
	for _, p := range paths {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Lstat(abs); err != nil {
			continue // an optional path (etsi.duckdb, the reranker) simply absent
		}
		err := filepath.Walk(abs, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			name := filepath.ToSlash(rel)
			if name == "." || seen[name] {
				return nil
			}
			seen[name] = true
			e := entry{name: name, path: path, fi: fi}
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				if e.link, err = os.Readlink(path); err != nil {
					return err
				}
			case fi.IsDir():
				e.name = name + "/"
			case !fi.Mode().IsRegular():
				fmt.Fprintf(os.Stderr, "imgtar: skipping %s (%s)\n", name, fi.Mode())
				return nil
			}
			out = append(out, e)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// writeLayer writes the entries as one tar, optionally gzip-compressed.
func writeLayer(dst string, entries []entry, uid, gid int, gz bool) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	var w io.Writer = f
	var zw *gzip.Writer
	if gz {
		// LAYERGZIP: the stream crane itself produces. go-containerregistry
		// compresses an uncompressed layer with compress/gzip at BestSpeed and a
		// default header, and a layer handed to it already gzipped is used as-is.
		// Measured 2026-09-11 on a 296 MiB layer of real corpus: this writer and
		// `crane append -o` give the SAME blob (sha256:4f06a906…) and the same
		// diff_id. So a layer compressed here keeps the digest the registry
		// already holds — nothing is re-uploaded for having moved the compression
		// out of crane — and crane no longer spends ~15 minutes recompressing 40
		// GB to learn digests it could have been handed.
		zw, err = gzip.NewWriterLevel(f, gzip.BestSpeed)
		if err != nil {
			_ = f.Close()
			return err
		}
		w = zw
	}
	tw := tar.NewWriter(w)
	for _, e := range entries {
		hdr, err := tar.FileInfoHeader(e.fi, e.link)
		if err != nil {
			_ = f.Close()
			return err
		}
		hdr.Name = e.name
		hdr.Uid, hdr.Gid = uid, gid
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
		if e.fi.Mode()&os.ModeSymlink == 0 {
			hdr.Mode = packedMode(e)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = f.Close()
			return err
		}
		if e.fi.Mode().IsRegular() {
			src, err := os.Open(e.path)
			if err != nil {
				_ = f.Close()
				return err
			}
			_, cerr := io.Copy(tw, src)
			_ = src.Close()
			if cerr != nil {
				_ = f.Close()
				return cerr
			}
		}
	}
	if err := tw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

// packedMode is the mode an entry is written with.
//
// MODES ARE ASSIGNED, NOT INHERITED. Windows has no POSIX permission bits, so
// FileInfoHeader hands back 0666 for every regular file and 0777 for every
// directory — a corpus and a set of model weights that the whole world may
// rewrite, which is not what the Dockerfile these layers replace ever produced.
//
// The execute bit therefore comes from the path rather than the filesystem:
// everything under a bin/ directory, plus any .sh, is a program. A
// non-executable entrypoint is an image that pulls and cannot start, so this is
// not cosmetic. Shared libraries stay 0644 — the loader does not need +x to map
// them.
func packedMode(e entry) int64 {
	// A symlink never reaches here: its own mode is meaningless, the target's
	// applies, and writeLayer leaves what FileInfoHeader set.
	switch {
	case e.fi.IsDir():
		return 0o755
	case strings.Contains(e.name, "bin/") || strings.HasSuffix(e.name, ".sh"):
		return 0o755
	default:
		return 0o644
	}
}

// layerKey names the content a layer would be packed from, WITHOUT reading it.
//
// The corpus layers are 23 and 19 GB, and packing then compressing them is most of
// a publish that did not change them: 6m18 of packing and ~15 minutes of crane
// recompression on 2026-09-11, to land on digests the registry already held. The
// key is what the packer's output is a function of:
//
//	this packer          the running executable's own hash — a change to how
//	                     layers are written invalidates every cached one
//	--gzip, uid, gid
//	every entry          member name, type, symlink target, and for a regular file
//	                     its size plus its content hash (up to contentKeyMax) or
//	                     its mtime to the nanosecond (above it)
//
// SIZE AND MTIME, NOT CONTENT, and that is the same identity the pipeline already
// trusts for these files (outputIdentity in internal/goal): hashing 42 GB to
// decide whether to skip reading 42 GB saves nothing. DuckDB rewrites move the
// mtime; a copy stamps "now". The corpus reaches the rootfs by HARD LINK
// (build-image.sh stage()), so its mtime here is the corpus's own, and an
// unchanged half keeps its key. A miss only ever costs the old price.
func layerKey(entries []entry, uid, gid int, gz bool) (string, error) {
	h := sha256.New()
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err := os.Open(self)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(h, exe)
	_ = exe.Close()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(h, "\nimgtar-layer gzip=%t uid=%d gid=%d\n", gz, uid, gid)
	for _, e := range entries {
		switch {
		case e.link != "":
			fmt.Fprintf(h, "L %q -> %q\n", e.name, e.link)
		case e.fi.IsDir():
			fmt.Fprintf(h, "D %q\n", e.name)
		case e.fi.Size() <= contentKeyMax:
			sum, err := fileSHA256(e.path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(h, "F %q %d sha=%s\n", e.name, e.fi.Size(), sum)
		default:
			fmt.Fprintf(h, "F %q %d @%d\n", e.name, e.fi.Size(), e.fi.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// contentKeyMax is the size up to which a file is keyed by its CONTENT rather than
// its mtime — the threshold internal/goal's outputIdentity uses, for the same
// reason. The script regenerates small files on every build (models.yaml is a
// heredoc, ONNX Runtime is re-extracted, passwd is re-derived), so their mtime is
// always "now"; keyed by mtime, the 6.4 GB model layer that carries models.yaml
// would never be reused. Hashing 64 MiB costs a fraction of a second; the corpus
// and the model weights are far above it and keep the cheap identity.
const contentKeyMax = 64 << 20

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hit reports whether the cache holds a complete layer for exactly this key. The
// key file is written LAST (store), so a key that matches vouches for the blob.
func hit(blob, key string) bool {
	b, err := os.ReadFile(blob + ".key")
	if err != nil || strings.TrimSpace(string(b)) != key {
		return false
	}
	st, err := os.Stat(blob)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// store records layer as the cached blob for key: the old key goes first, so an
// interruption anywhere leaves a miss, never a key pointing at the wrong blob.
func store(blob, key, layer string) error {
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		return err
	}
	_ = os.Remove(blob + ".key")
	tmp := blob + ".tmp"
	_ = os.Remove(tmp)
	if err := linkOrCopy(layer, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, blob); err != nil {
		return err
	}
	return os.WriteFile(blob+".key", []byte(key+"\n"), 0o644)
}

// linkOrCopy makes dst the same bytes as src: a hard link when the filesystem
// allows it (no copy of a 16 GB blob), a copy otherwise. NOTHING WRITES THROUGH
// EITHER NAME: a layer is written once, to a temporary name, and renamed.
func linkOrCopy(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
