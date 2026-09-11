package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden reads a file of testdata/crane-append-2026-09-11 — raw bytes as the
// registries served them — and checks it is the blob it claims to be.
//
// The registry bytes carry no CR, so one is stripped on the way in: a checkout
// that converted line endings (the main one has 211 CRLF files) must not turn
// this test into a statement about git's configuration. The digest check that
// follows is what makes the stripping safe — it passes only on the exact bytes.
func golden(t *testing.T, name, digest string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "crane-append-2026-09-11", name))
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.ReplaceAll(b, []byte("\r"), nil)
	if got := sha256Of(b); got != digest {
		t.Fatalf("testdata %s hashes to %s, not %s: it is not the blob it stands for", name, got, digest)
	}
	return b
}

// baseLayout writes an OCI layout holding one image, as `crane pull --format=oci
// --platform …` leaves it: index.json, the manifest and config blobs, and every
// layer blob the manifest names (content irrelevant here — only a push reads it).
func baseLayout(t *testing.T, mediaType string, manifest, config []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range [][]byte{manifest, config} {
		if err := os.WriteFile(blobPath(dir, sha256Of(b)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var m ociManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	for _, l := range m.Layers {
		if err := os.WriteFile(blobPath(dir, l.Digest), []byte("base layer"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, _ := json.Marshal(ociIndex{SchemaVersion: 2, MediaType: mtOCIIndex, Manifests: []ociDescriptor{{
		MediaType: mediaType, Size: int64(len(manifest)), Digest: sha256Of(manifest),
	}}})
	if err := os.WriteFile(filepath.Join(dir, "index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// THE ASSEMBLY IS THE IMAGE crane append BUILT, TO THE BYTE.
//
// Inputs: debian:bookworm-slim linux/amd64 as it was pulled on 2026-09-11
// (manifest sha256:5ae3c39e…, config sha256:160466e6…), and the seven layers of
// that day's publish, each named by the digest, size and diff_id crane computed
// by reading it. Expected: the manifest `crane append` pushed before `crane
// mutate` — sha256:778f0d31…, printed in .local/logs/20260911T101313Z-publish.log
// as "appended, … before config" — and its config sha256:bcb41360…, both
// fetched back from ghcr.io. `crane mutate` then turned that manifest into the
// published sha256:b0e4ccbf…; it reads only what the registry holds, so the
// same manifest in gives the same image out.
func TestAssemblyIsByteIdenticalToCraneAppend(t *testing.T) {
	const (
		baseManifest = "sha256:5ae3c39ebd15e229dcedd5cee596b2497182493d41ff162e824ba13fc1b2b867"
		baseConfig   = "sha256:160466e67bb85a4099d9d9c2356b4a6a64747b281a22c142efbd4539db1b8525"
		wantManifest = "sha256:778f0d317e4a76af716a046c29b9461e7caae0eccb0c5786fb377128bce8f3a6"
		wantConfig   = "sha256:bcb41360e20dc96a08c361b65ed35b81b6b47e72d8fc6fab08168be3f14cc1e7"
	)
	dir := baseLayout(t, mtOCIManifest,
		golden(t, "base-manifest.json", baseManifest), golden(t, "base-config.json", baseConfig))
	base, err := loadBase(dir)
	if err != nil {
		t.Fatal(err)
	}
	layers := []struct {
		name, digest string
		size         int64
		diffID       string
	}{
		{"10-runtime.tar.gz", "sha256:b15e7d4da436965e69fe9e3028034af648ec74a92a0fc93aae51daae95449972", 1041038, "sha256:6c7421859e3e3f14f4a5f6bb7cd01884ebb9b2b45db0b5e9ddd459ed11cb0c95"},
		{"20-duckdb-ext.tar.gz", "sha256:7f2be637684c3d9f117b0360b66198f3acb0324737ab262e23f4c5df56d25134", 17334276, "sha256:d45319c579c428347336fd79d3e3206badff8e41aa86ddad78ae67aca51c5eec"},
		{"30-ort.tar.gz", "sha256:111ecf0fe4622fee493553c6e49467bc51605bca0cc21e857a9b6850ac84d590", 9880953, "sha256:fbe32f01ccb49d982915328c2a481b0d0cc385077b6b6666e4bab0b3da9dfc20"},
		{"40-models.tar.gz", "sha256:00629cb16fc8b451b2d39a1a62578a1a331ac3b5a256135da633b1f61104f210", 4185194561, "sha256:027df6822a70c6e7b7983117b42ad6f0e2f56f64f0991d14486ca7681e7c12c1"},
		{"50-corpus-3gpp.tar.gz", "sha256:bea86f7eaed258f628560d6fdf24e6fb3d1bd206706583a2de873add9e1d24dc", 11263431485, "sha256:4f7af07b79dacaebf72d56a85d60d1d3214757ee16e61b1ccfe847635e117b96"},
		{"51-corpus-etsi.tar.gz", "sha256:f9018c14a0c400f3df004c6ef931373be9237e544eea53a514c8c49e9892b8ca", 10692943858, "sha256:d6534eba44002093154d86c107d3f99c8dca6a156a6af3c3be66acb06feb764f"},
		{"60-bin.tar.gz", "sha256:b76fcae96b1f4a9e9915c4028db6bb908df9888b5a25b6f310390cac75246c88", 30598024, "sha256:409bafd6e004a97dde18e7849773ec8161e5d4751481878a1e5f8a00ef28e033"},
	}
	var paths []string
	var ids []identity
	for _, l := range layers {
		paths = append(paths, l.name)
		ids = append(ids, identity{Digest: l.digest, Size: l.size, DiffID: l.diffID, Sample: strings.Repeat("0", 64)})
	}
	img, err := assemble(base, paths, ids)
	if err != nil {
		t.Fatal(err)
	}
	want := golden(t, "appended-config.json", wantConfig)
	if !bytes.Equal(img.config, want) {
		t.Fatalf("the config differs from the one crane append pushed:\n got %s\nwant %s", img.config, want)
	}
	wantM := golden(t, "appended-manifest.json", wantManifest)
	if !bytes.Equal(img.manifest, wantM) {
		t.Fatalf("the manifest differs from the one crane append pushed:\n got %s\nwant %s", img.manifest, wantM)
	}
	if img.digest() != wantManifest {
		t.Fatalf("manifest digest %s, crane pushed %s", img.digest(), wantManifest)
	}
}

// THE LAYOUT DECLARES WHAT crane WOULD HAVE COMPUTED, and nothing was read to
// declare it. Layers are packed by writeLayer; the layout is assembled from the
// records alone; then every layer blob in the layout is read here, the way
// tarball.LayerFromFile reads it — sha256 of the file, sha256 of what it
// decompresses to — and must match the manifest and the config. A test that
// compared the records to themselves would check nothing.
func TestTheLayoutDeclaresWhatReadingTheLayersGives(t *testing.T) {
	root := tree(t)
	work := t.TempDir()
	var layers []string
	for i, paths := range [][]string{{"usr"}, {"data"}} {
		entries, err := collect(root, paths)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(work, []string{"10-a.tar.gz", "20-b.tar.gz"}[i])
		id, err := writeLayer(p, entries, 10001, 10001, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeIdentity(p, id); err != nil {
			t.Fatal(err)
		}
		layers = append(layers, p)
	}
	base, err := loadBase(testBase(t))
	if err != nil {
		t.Fatal(err)
	}
	var ids []identity
	for _, p := range layers {
		id, recorded, err := layerIdentity(p)
		if err != nil || !recorded {
			t.Fatalf("layerIdentity(%s) = recorded %v, %v; the record writeLayer left was not used", p, recorded, err)
		}
		ids = append(ids, id)
	}
	img, err := assemble(base, layers, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "oci")
	if err := writeLayout(out, img); err != nil {
		t.Fatal(err)
	}

	var idx ociIndex
	readJSON(t, filepath.Join(out, "index.json"), &idx)
	if len(idx.Manifests) != 1 || idx.Manifests[0].MediaType != mtOCIManifest {
		t.Fatalf("index.json = %+v, want one OCI image manifest", idx.Manifests)
	}
	mraw, err := readBlob(out, idx.Manifests[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	var m ociManifest
	if err := json.Unmarshal(mraw, &m); err != nil {
		t.Fatal(err)
	}
	craw, err := readBlob(out, m.Config.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var cfg ociConfigFile
	if err := json.Unmarshal(craw, &cfg); err != nil {
		t.Fatal(err)
	}
	nb := len(base.manifest.Layers)
	if len(m.Layers) != nb+2 || len(cfg.RootFS.DiffIDs) != nb+2 || len(cfg.History) != len(base.config.History)+2 {
		t.Fatalf("%d layers, %d diff_ids, %d history entries for %d base layers plus 2", len(m.Layers),
			len(cfg.RootFS.DiffIDs), len(cfg.History), nb)
	}
	for i, p := range layers {
		d := m.Layers[nb+i]
		b, err := os.ReadFile(blobPath(out, d.Digest))
		if err != nil {
			t.Fatalf("layer %d is not in the layout under its digest: %v", i, err)
		}
		if got := sha256Of(b); got != d.Digest || int64(len(b)) != d.Size {
			t.Fatalf("%s: the manifest says %s (%d B), the blob is %s (%d B)", p, d.Digest, d.Size, got, len(b))
		}
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, zr); err != nil {
			t.Fatal(err)
		}
		if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != cfg.RootFS.DiffIDs[nb+i] {
			t.Fatalf("%s: the config declares diff_id %s, the blob decompresses to %s — a runtime would refuse "+
				"to unpack this image", p, cfg.RootFS.DiffIDs[nb+i], got)
		}
		if d.MediaType != mtOCILayer {
			t.Fatalf("layer media type %q onto an OCI base; crane.Append uses %q", d.MediaType, mtOCILayer)
		}
	}
}

// testBase is a small OCI base image with one layer.
func testBase(t *testing.T) string {
	t.Helper()
	cfg := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:` +
		strings.Repeat("1", 64) + `"]},"history":[{"created_by":"base"}],"config":{"Env":["PATH=/bin"]}}`)
	m, _ := json.Marshal(ociManifest{SchemaVersion: 2, MediaType: mtOCIManifest,
		Config: ociDescriptor{MediaType: "application/vnd.oci.image.config.v1+json", Size: int64(len(cfg)), Digest: sha256Of(cfg)},
		Layers: []ociDescriptor{{MediaType: mtOCILayer, Size: 10, Digest: "sha256:" + strings.Repeat("2", 64)}},
	})
	return baseLayout(t, mtOCIManifest, m, cfg)
}

func readJSON(t *testing.T, p string, v any) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// THE BASE IS CHECKED, NOT TRUSTED: it is the one input read from somewhere
// else, and each of these would assemble an image with no error otherwise.
func TestABaseThatIsNotOneWholeLinuxImageIsRefused(t *testing.T) {
	good := testBase(t)
	if _, err := loadBase(good); err != nil {
		t.Fatalf("the well-formed base is refused, so the refusals below prove nothing: %v", err)
	}
	var idx ociIndex
	readJSON(t, filepath.Join(good, "index.json"), &idx)
	mraw, _ := os.ReadFile(blobPath(good, idx.Manifests[0].Digest))
	var m ociManifest
	_ = json.Unmarshal(mraw, &m)

	t.Run("two images in the layout", func(t *testing.T) {
		dir := testBase(t)
		two := idx
		two.Manifests = append(two.Manifests, idx.Manifests[0])
		b, _ := json.Marshal(two)
		_ = os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644)
		if _, err := loadBase(dir); err == nil {
			t.Fatal("a layout with two images was accepted; which one is the base would be an accident")
		}
	})
	t.Run("an index, not an image", func(t *testing.T) {
		dir := testBase(t)
		one := idx
		one.Manifests = []ociDescriptor{idx.Manifests[0]}
		one.Manifests[0].MediaType = mtOCIIndex
		b, _ := json.Marshal(one)
		_ = os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644)
		if _, err := loadBase(dir); err == nil {
			t.Fatal("an index was accepted as a base image")
		}
	})
	t.Run("a config that is not its digest", func(t *testing.T) {
		dir := testBase(t)
		p := blobPath(dir, m.Config.Digest)
		b, _ := os.ReadFile(p)
		_ = os.WriteFile(p, bytes.Replace(b, []byte("PATH=/bin"), []byte("PATH=/xyz"), 1), 0o644)
		if _, err := loadBase(dir); err == nil {
			t.Fatal("a base config that does not hash to its digest was accepted")
		}
	})
	t.Run("a base layer missing", func(t *testing.T) {
		dir := testBase(t)
		_ = os.Remove(blobPath(dir, m.Layers[0].Digest))
		if _, err := loadBase(dir); err == nil {
			t.Fatal("a base whose layer blob is absent was accepted; a push that needs it would fail mid-way")
		}
	})
	t.Run("a Windows base", func(t *testing.T) {
		cfg := []byte(`{"architecture":"amd64","os":"windows","rootfs":{"type":"layers","diff_ids":[]},"config":{}}`)
		mm, _ := json.Marshal(ociManifest{SchemaVersion: 2, MediaType: mtOCIManifest,
			Config: ociDescriptor{MediaType: "application/vnd.oci.image.config.v1+json", Size: int64(len(cfg)), Digest: sha256Of(cfg)},
			Layers: []ociDescriptor{}})
		if _, err := loadBase(baseLayout(t, mtOCIManifest, mm, cfg)); err == nil {
			t.Fatal("a Windows base was accepted; crane rewrites layers for one and this does not")
		}
	})
}

// AN UNCOMPRESSED LAYER IS REFUSED: it would be declared tar+gzip.
func TestAnUncompressedLayerIsRefused(t *testing.T) {
	base, err := loadBase(testBase(t))
	if err != nil {
		t.Fatal(err)
	}
	d := "sha256:" + strings.Repeat("3", 64)
	_, err = assemble(base, []string{"x.tar"}, []identity{{Digest: d, DiffID: d, Size: 10, Sample: strings.Repeat("0", 64)}})
	if err == nil {
		t.Fatal("a layer whose digest is its diff_id was assembled as a gzip layer")
	}
}

// THE LAYOUT IS WRITTEN FRESH: crane push reads whatever index.json it finds.
func TestTheLayoutIsWrittenIntoAFreshDirectoryOnly(t *testing.T) {
	base, err := loadBase(testBase(t))
	if err != nil {
		t.Fatal(err)
	}
	img, err := assemble(base, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLayout(t.TempDir(), img); err == nil {
		t.Fatal("a layout was written into a directory that already existed")
	}
}
