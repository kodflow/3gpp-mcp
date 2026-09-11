package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reread is what `crane append` computed for a layer by reading it: the sha256
// of the file, and the sha256 of what it decompresses to. Written here without
// any of the code under test.
func reread(t *testing.T, p string) (digest, diffID string, size int64) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Sum256(b)
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, zr); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + hex.EncodeToString(d[:]), "sha256:" + hex.EncodeToString(h.Sum(nil)), int64(len(b))
}

// bigTree is a rootfs whose layer spans many gzip flushes and more than the
// 17 x 64 KiB a content sample reads, so the hashes see a stream in many pieces
// and the sample does not degenerate into a whole-file hash.
func bigTree(t *testing.T) string {
	t.Helper()
	root := tree(t)
	var b bytes.Buffer
	for i := 0; b.Len() < 3<<20; i++ {
		// Poorly compressible: a counter through sha256.
		s := sha256.Sum256([]byte{byte(i), byte(i >> 8), byte(i >> 16)})
		b.Write(s[:])
	}
	p := filepath.Join(root, "data", "weights.bin")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// THE IDENTITY writeLayer OBSERVES IS THE ONE A RE-READ FINDS — digest, size and
// diff_id — for a gzip layer, and for a plain one (whose digest is its diff_id).
func TestWriteLayerObservesWhatAReReadFinds(t *testing.T) {
	root := bigTree(t)
	entries, err := collect(root, []string{"usr", "data"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gz := filepath.Join(dir, "l.tar.gz")
	id, err := writeLayer(gz, entries, 10001, 10001, true)
	if err != nil {
		t.Fatal(err)
	}
	if id.Size < 3<<20 {
		t.Fatalf("the layer is %d bytes; the test needs one larger than the content sample", id.Size)
	}
	d, diff, n := reread(t, gz)
	if id.Digest != d || id.DiffID != diff || id.Size != n {
		t.Fatalf("writeLayer observed digest %s diff_id %s size %d; reading the file gives %s %s %d",
			id.Digest, id.DiffID, id.Size, d, diff, n)
	}
	if id.Digest == id.DiffID {
		t.Fatal("a gzip layer's digest equals its diff_id: the two hashes saw the same stream")
	}
	if c, err := computeIdentity(gz); err != nil || c != id {
		t.Fatalf("computeIdentity = %+v, %v; writeLayer observed %+v", c, err, id)
	}

	plain := filepath.Join(dir, "l.tar")
	pid, err := writeLayer(plain, entries, 10001, 10001, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(plain)
	if sum := sha256.Sum256(b); pid.Digest != "sha256:"+hex.EncodeToString(sum[:]) || pid.DiffID != pid.Digest {
		t.Fatalf("plain layer: digest %s diff_id %s, file hashes to %x", pid.Digest, pid.DiffID, sum)
	}
	if pid.DiffID != id.DiffID {
		t.Fatal("the gzip layer's diff_id is not the hash of the plain tar of the same entries")
	}
}

// A RECORD VOUCHES ONLY FOR THE FILE IT WAS MADE FROM. Each case is a way the
// file beside a record can stop being that file, and each must be refused — the
// callers then pack again (the cache) or read the layer (imgtar oci), never trust.
func TestARecordDescribesOnlyItsOwnBlob(t *testing.T) {
	root := bigTree(t)
	entries, _ := collect(root, []string{"usr", "data"})
	fresh := func(t *testing.T) (string, identity) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "l.tar.gz")
		id, err := writeLayer(p, entries, 10001, 10001, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeIdentity(p, id); err != nil {
			t.Fatal(err)
		}
		if _, err := readIdentity(p); err != nil {
			t.Fatalf("a record just written does not describe its blob, so the refusals prove nothing: %v", err)
		}
		return p, id
	}
	flip := func(t *testing.T, p string, off int64) {
		t.Helper()
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b := make([]byte, 1)
		if _, err := f.ReadAt(b, off); err != nil {
			t.Fatal(err)
		}
		b[0] ^= 0xff
		if _, err := f.WriteAt(b, off); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("no record", func(t *testing.T) {
		p, _ := fresh(t)
		_ = os.Remove(p + idSuffix)
		if _, err := readIdentity(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("readIdentity without a record = %v, want not-exist", err)
		}
	})
	t.Run("a record that does not parse", func(t *testing.T) {
		p, _ := fresh(t)
		_ = os.WriteFile(p+idSuffix, []byte(`{"digest":`), 0o644)
		if _, err := readIdentity(p); err == nil {
			t.Fatal("a truncated record was accepted")
		}
	})
	t.Run("a record that is not a digest", func(t *testing.T) {
		p, id := fresh(t)
		id.DiffID = "sha256:" + strings.Repeat("Z", 64)
		_ = writeIdentity(p, id)
		if _, err := readIdentity(p); err == nil {
			t.Fatal("a record whose diff_id is not hex was accepted")
		}
	})
	t.Run("the blob grew", func(t *testing.T) {
		p, _ := fresh(t)
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.Write([]byte{0})
		_ = f.Close()
		if _, err := readIdentity(p); err == nil {
			t.Fatal("a record accepted a blob of another size")
		}
	})
	// The same size, one byte changed — where the gzip header is, and where the
	// trailer's CRC-32 of the whole tar is. This is the replaced-under-the-same-name
	// case; the byte flipped is covered by the sample by construction.
	for _, where := range []string{"header", "trailer"} {
		t.Run("one byte of the "+where+" changed, size kept", func(t *testing.T) {
			p, id := fresh(t)
			off := int64(4) // gzip MTIME field
			if where == "trailer" {
				off = id.Size - 6 // inside the CRC-32
			}
			flip(t, p, off)
			if st, _ := os.Stat(p); st.Size() != id.Size {
				t.Fatal("the test changed the size, so it proves nothing about the sample")
			}
			if _, err := readIdentity(p); err == nil {
				t.Fatal("a record accepted a blob whose content changed under the same size")
			}
		})
	}
}

// THE CACHE HANDS BACK A LAYER ONLY WITH ITS OWN IDENTITY: the record must name the
// key the entry was stored under, and describe the blob. A #328-era entry (a key
// and no record) misses, so its digest is never guessed.
func TestTheCacheHandsBackAnIdentityOnlyUnderItsKey(t *testing.T) {
	root := tree(t)
	entries, _ := collect(root, []string{"usr", "data"})
	k := key(t, root, "usr", "data")
	dir := t.TempDir()
	layer := filepath.Join(dir, "10-x.tar.gz")
	id, err := writeLayer(layer, entries, 10001, 10001, true)
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(dir, "cache", "10-x.tar.gz")
	if err := store(blob, k, layer, id); err != nil {
		t.Fatal(err)
	}
	got, err := cachedIdentity(blob, k)
	if err != nil {
		t.Fatalf("a stored entry has no identity under its own key: %v", err)
	}
	if got.Digest != id.Digest || got.DiffID != id.DiffID || got.Size != id.Size {
		t.Fatalf("the cache handed back %+v for %+v", got, id)
	}
	if _, err := cachedIdentity(blob, k+"0"); err == nil {
		t.Fatal("an entry's identity was handed back under another key")
	}
	_ = os.Remove(blob + idSuffix)
	if _, err := cachedIdentity(blob, k); err == nil {
		t.Fatal("an entry with no record handed back an identity: a #328-era cache would be trusted blind")
	}
}

// THROUGH packLayer, end to end: an unchanged tree hits and hands back the same
// identity without packing; a changed file misses and records the new diff_id; a
// cache blob replaced behind the key's back is packed again rather than trusted.
func TestPackLayerNeverServesAStaleIdentity(t *testing.T) {
	root := tree(t)
	cache := filepath.Join(t.TempDir(), "cache")
	stage := func() string { return filepath.Join(t.TempDir(), "10-x.tar.gz") }
	check := func(t *testing.T, out string, id identity) {
		t.Helper()
		d, diff, n := reread(t, out)
		if id.Digest != d || id.DiffID != diff || id.Size != n {
			t.Fatalf("packLayer reported %s/%s/%d; the layer it left is %s/%s/%d", id.Digest, id.DiffID, id.Size, d, diff, n)
		}
		rec, err := readIdentity(out)
		if err != nil || rec.Digest != d || rec.DiffID != diff {
			t.Fatalf("the record next to the layer = %+v, %v; the layer is %s/%s", rec, err, d, diff)
		}
	}

	out1 := stage()
	id1, _, cached, err := packLayer(root, out1, cache, 10001, 10001, true, []string{"usr", "data"})
	if err != nil || cached {
		t.Fatalf("first pack: cached=%v err=%v", cached, err)
	}
	check(t, out1, id1)

	out2 := stage()
	id2, _, cached, err := packLayer(root, out2, cache, 10001, 10001, true, []string{"usr", "data"})
	if err != nil || !cached {
		t.Fatalf("an unchanged tree did not hit: cached=%v err=%v", cached, err)
	}
	if id2.Digest != id1.Digest || id2.DiffID != id1.DiffID {
		t.Fatalf("a hit handed back %s/%s for a layer packed as %s/%s", id2.Digest, id2.DiffID, id1.Digest, id1.DiffID)
	}
	check(t, out2, id2)

	if err := os.WriteFile(filepath.Join(root, "data", "models.yaml"), []byte("models: {changed: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out3 := stage()
	id3, _, cached, err := packLayer(root, out3, cache, 10001, 10001, true, []string{"usr", "data"})
	if err != nil || cached {
		t.Fatalf("a changed file hit the cache: cached=%v err=%v", cached, err)
	}
	if id3.DiffID == id1.DiffID {
		t.Fatal("a changed file kept its diff_id")
	}
	check(t, out3, id3)

	// Behind the key's back: the cache blob replaced by another layer (the first
	// one, restored from somewhere), key and record left in place. The key still
	// matches the tree; only the record can tell the file is not what it
	// describes.
	blob := filepath.Join(cache, "10-x.tar.gz")
	_ = os.Remove(blob)
	if err := linkOrCopy(out1, blob); err != nil {
		t.Fatal(err)
	}
	out4 := stage()
	id4, _, cached, err := packLayer(root, out4, cache, 10001, 10001, true, []string{"usr", "data"})
	if err != nil || cached {
		t.Fatalf("a cache blob replaced behind its record was reused: cached=%v err=%v", cached, err)
	}
	if id4.DiffID != id3.DiffID {
		t.Fatalf("the repack gave diff_id %s, the tree's layer is %s", id4.DiffID, id3.DiffID)
	}
	check(t, out4, id4)
}

// imgtar oci COMPUTES WHAT HAS NO VALID RECORD, and records it: correct first,
// then cheap.
func TestLayerIdentityComputesAMissingOrFalseRecord(t *testing.T) {
	root := bigTree(t)
	entries, _ := collect(root, []string{"usr", "data"})
	p := filepath.Join(t.TempDir(), "l.tar.gz")
	want, err := writeLayer(p, entries, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	id, recorded, err := layerIdentity(p)
	if err != nil || recorded || id != want {
		t.Fatalf("no record: layerIdentity = %+v recorded=%v %v, want computed %+v", id, recorded, err, want)
	}
	id, recorded, err = layerIdentity(p)
	if err != nil || !recorded || id != want {
		t.Fatalf("second call: %+v recorded=%v %v; the computed record was not kept", id, recorded, err)
	}
	// A record for ANOTHER blob of this name: wrong size, so it cannot describe it.
	other := want
	other.Size++
	other.DiffID = "sha256:" + strings.Repeat("a", 64)
	if err := writeIdentity(p, other); err != nil {
		t.Fatal(err)
	}
	id, recorded, err = layerIdentity(p)
	if err != nil || recorded || id != want {
		t.Fatalf("false record: %+v recorded=%v %v; its diff_id must not reach the image", id, recorded, err)
	}
}
