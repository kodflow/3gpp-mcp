package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"regexp"
)

// identity is everything an image manifest and config say about one layer: the
// digest and size of the compressed blob the registry stores, and the diff_id —
// the sha256 of the tar the blob decompresses to, which the config's
// rootfs.diff_ids lists and a runtime checks after unpacking.
//
// WHY IT IS RECORDED. After #328 imgtar writes each layer gzipped and reuses an
// unchanged one from .local/image-cache, yet `crane append` still read every
// layer twice before pushing anything: once to sha256 the blob, once to gunzip it
// and sha256 the tar. That was 7m42 of the 2026-09-11 publish (12:27:50 ->
// 12:35:32), 26 GB of blobs read and 42 GB decompressed, for layers the registry
// then answered "existing blob" to. imgtar is the one program that sees both byte
// streams as they are produced, so it hashes them on the way out (writeLayer) and
// the image is assembled from this record instead of from a re-read (oci.go).
//
// THE PAIR IS NEVER DERIVED, ONLY OBSERVED. Digest and diff_id come from ONE pass
// over the bytes: the tees in writeLayer, or computeIdentity reading the blob. They
// are never inferred from the layer key, from a file name or from a timestamp,
// so a recorded diff_id always belongs to the digest recorded beside it. What
// can go stale is the link between the record and the FILE it sits next to, and
// Sample is the cheap witness for that link (see describes).
type identity struct {
	Digest string `json:"digest"`  // "sha256:<hex>" of the .tar.gz
	Size   int64  `json:"size"`    // bytes of the .tar.gz
	DiffID string `json:"diff_id"` // "sha256:<hex>" of the tar inside it
	// Sample is sampleHash of the .tar.gz: 17 blocks of 64 KiB, the first and the
	// last included, so the gzip header and its trailer — the CRC-32 and length of
	// the whole tar — are always among the bytes compared.
	Sample string `json:"sample"`
	// Key is the layer key the blob was packed under. Set on the CACHE's record
	// only: a record is valid for the cache entry whose key it names, so a record
	// left behind by another key can never vouch for this one's blob.
	Key string `json:"key,omitempty"`
}

// idSuffix names the record that sits next to a layer blob.
const idSuffix = ".id"

var sha256Ref = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// wellFormed rejects a record that could not have been written by this program.
func (id identity) wellFormed() error {
	switch {
	case !sha256Ref.MatchString(id.Digest):
		return fmt.Errorf("digest %q is not sha256:<64 hex>", id.Digest)
	case !sha256Ref.MatchString(id.DiffID):
		return fmt.Errorf("diff_id %q is not sha256:<64 hex>", id.DiffID)
	case id.Size <= 0:
		return fmt.Errorf("size %d is not a blob size", id.Size)
	case len(id.Sample) != 64:
		return fmt.Errorf("sample %q is not a sha256", id.Sample)
	}
	return nil
}

// describes reports whether id is the record of the file at blob, WITHOUT reading
// the file: same size, and the same content sample (~1 MiB of reads).
//
// A FULL HASH IS WHAT THIS IS SPARING. Re-hashing the blob is the 26 GB read the
// record exists to remove. What the check has to catch is a record that outlived
// its file — a blob replaced or rewritten under the same name — and a different
// gzip stream differs in its header, its trailer (CRC-32 and length of the whole
// tar) or its size. What slips past is a same-size edit confined to bytes no
// sampled block covers, and it does not become a wrong image even then: a blob
// the registry already holds is served from the registry's own copy, whose
// diff_id is the recorded one, and a blob it lacks is uploaded under the recorded
// digest, which the registry verifies and refuses on mismatch.
func (id identity) describes(blob string) error {
	if err := id.wellFormed(); err != nil {
		return err
	}
	st, err := os.Stat(blob)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", blob)
	}
	if st.Size() != id.Size {
		return fmt.Errorf("%s is %d bytes, the record says %d", blob, st.Size(), id.Size)
	}
	sample, err := sampleHash(blob, st.Size())
	if err != nil {
		return err
	}
	if sample != id.Sample {
		return fmt.Errorf("%s does not carry the recorded content sample: it is not the blob the record was made for", blob)
	}
	return nil
}

// readIdentity loads the record that sits next to blob, and checks it describes
// blob. Any doubt is an error; the callers treat every error as "no record".
func readIdentity(blob string) (identity, error) {
	var id identity
	b, err := os.ReadFile(blob + idSuffix)
	if err != nil {
		return id, err
	}
	if err := json.Unmarshal(b, &id); err != nil {
		return id, fmt.Errorf("%s%s does not parse: %w", blob, idSuffix, err)
	}
	return id, id.describes(blob)
}

// writeIdentity writes the record next to blob, aside then renamed, so a record
// that exists is a record that is whole.
func writeIdentity(blob string, id identity) error {
	b, err := json.Marshal(id)
	if err != nil {
		return err
	}
	tmp := blob + idSuffix + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, blob+idSuffix)
}

// computeIdentity reads a gzipped layer once, hashing the compressed bytes and
// the tar they decompress to in the same pass. It is what `crane append` did for
// every layer on every publish; here it runs only for a layer that has no valid
// record, and says so.
func computeIdentity(blob string) (identity, error) {
	f, err := os.Open(blob)
	if err != nil {
		return identity{}, err
	}
	defer f.Close()
	dig := sha256.New()
	var n countWriter
	zr, err := gzip.NewReader(io.TeeReader(f, io.MultiWriter(dig, &n)))
	if err != nil {
		return identity{}, fmt.Errorf("%s: %w", blob, err)
	}
	// ONE MEMBER, AS THE REGISTRY'S CLIENTS READ IT. gzip.Reader follows
	// concatenated members by default, and so does every runtime that unpacks a
	// layer; the diff_id is the hash of all of it.
	diff := sha256.New()
	if _, err := io.Copy(diff, zr); err != nil {
		return identity{}, fmt.Errorf("%s: %w", blob, err)
	}
	if err := zr.Close(); err != nil {
		return identity{}, err
	}
	// The digest is of the FILE, every byte of it: the reader stops at the end of
	// the last gzip member, so whatever it did not ask for is read here.
	if _, err := io.Copy(dig, io.TeeReader(f, &n)); err != nil {
		return identity{}, err
	}
	st, err := f.Stat()
	if err != nil {
		return identity{}, err
	}
	if int64(n) != st.Size() {
		return identity{}, fmt.Errorf("%s: read %d bytes of a %d-byte file", blob, n, st.Size())
	}
	sample, err := sampleHash(blob, st.Size())
	if err != nil {
		return identity{}, err
	}
	id := identity{
		Digest: "sha256:" + hex.EncodeToString(dig.Sum(nil)),
		Size:   st.Size(),
		DiffID: "sha256:" + hex.EncodeToString(diff.Sum(nil)),
		Sample: sample,
	}
	return id, id.wellFormed()
}

type countWriter int64

func (c *countWriter) Write(p []byte) (int, error) {
	*c += countWriter(len(p))
	return len(p), nil
}

// asyncHash is a sha256 fed from a goroutine, so hashing the 42 GB tar and the 26
// GB gzip stream runs BESIDE the compressor instead of in front of it.
//
// Measured on this machine (2026-09-11, 8 vCPU EPYC 7543P under QEMU): sha256
// 305 MB/s, gzip BestSpeed 72 MB/s, both single-threaded. Inline, the two hashes
// would add roughly (42+26) GB / 305 MB/s ≈ 3.7 minutes to a miss that already
// costs ~12 for the gzip; in their own goroutines they finish while the compressor
// is still producing.
type asyncHash struct {
	h    hash.Hash
	ch   chan []byte
	done chan struct{}
	n    int64
}

func newAsyncHash() *asyncHash {
	a := &asyncHash{h: sha256.New(), ch: make(chan []byte, 64), done: make(chan struct{})}
	go func() {
		for b := range a.ch {
			a.h.Write(b)
		}
		close(a.done)
	}()
	return a
}

// Write copies p: the tar and gzip writers reuse their buffers.
func (a *asyncHash) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	a.ch <- b
	a.n += int64(len(p))
	return len(p), nil
}

// sum stops the goroutine and returns "sha256:<hex>" and the bytes hashed. It
// must be called exactly once.
func (a *asyncHash) sum() (string, int64) {
	close(a.ch)
	<-a.done
	return "sha256:" + hex.EncodeToString(a.h.Sum(nil)), a.n
}
