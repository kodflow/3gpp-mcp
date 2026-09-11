package bootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeRegistry serves ONE corpus package whose manifest can be swapped (a tag
// that moves) and records every manifest path it was asked for.
type fakeRegistry struct {
	mu        sync.Mutex
	manifest  []byte // the exact bytes served for any /manifests/ request
	layer     []byte
	manifests []string // request paths, in order
	blobHits  int32
}

func (f *fakeRegistry) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"token":"test-token"}`)
		case strings.Contains(r.URL.Path, "/manifests/"):
			f.mu.Lock()
			f.manifests = append(f.manifests, r.URL.Path)
			body := f.manifest
			f.mu.Unlock()
			_, _ = w.Write(body)
		case strings.Contains(r.URL.Path, "/blobs/"):
			atomic.AddInt32(&f.blobHits, 1)
			_, _ = w.Write(f.layer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase, oldService := registryBase, registryService
	registryBase, registryService = srv.URL, "test"
	t.Cleanup(func() { registryBase, registryService = oldBase, oldService })
}

func digestOf(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

// corpusLayer builds a one-member tar and the manifest that names it. `note`
// makes two manifests for the same layer differ, the way a re-push does.
func corpusLayer(t *testing.T, member, body, note string) (layer, manifest []byte) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layer = buf.Bytes()
	manifest = []byte(fmt.Sprintf(`{"schemaVersion":2,"annotations":{"note":%q},"layers":[{"digest":%q,"size":%d}]}`,
		note, digestOf(layer), len(layer)))
	return layer, manifest
}

// A DIGEST PIN IS ONLY A GUARANTEE IF THE CLIENT CHECKS IT. Asking the registry
// for /manifests/sha256:X and trusting whatever comes back makes a pin exactly as
// good as a tag. The fetch must refuse a manifest whose bytes do not hash to the
// digest it asked for — before a single layer byte is transferred.
func TestAPinnedDigestIsCheckedAgainstTheManifestServed(t *testing.T) {
	const member = "3gpp.duckdb"
	layer, manifest := corpusLayer(t, member, "the pinned corpus", "v1")
	f := &fakeRegistry{manifest: manifest, layer: layer}
	f.serve(t)
	quiet := func(string, ...any) {}

	// The honest registry: the digest asked for is the digest served.
	pin := digestOf(manifest)
	src := CorpusSource{Owner: "o", Image: Image3GPP, Ref: pin, Member: member}
	dest := filepath.Join(t.TempDir(), member)
	got, err := FetchCorpus(context.Background(), src, "pat", dest, quiet)
	if err != nil {
		t.Fatalf("pull by the digest the registry serves: %v", err)
	}
	if got != pin {
		t.Errorf("FetchCorpus reported %s, want the pinned %s", got, pin)
	}
	// By digest, in the path the distribution API wants — not `@sha256:`.
	if last := f.manifests[len(f.manifests)-1]; !strings.HasSuffix(last, "/manifests/"+pin) {
		t.Errorf("manifest requested at %q, want …/manifests/%s", last, pin)
	}

	// The registry now serves something else under that digest (a substituted or
	// corrupted manifest). Nothing may be transferred, nothing may be written.
	_, other := corpusLayer(t, member, "a different corpus", "v2")
	f.mu.Lock()
	f.manifest = other
	f.mu.Unlock()
	before := atomic.LoadInt32(&f.blobHits)
	dest2 := filepath.Join(t.TempDir(), member)
	_, err = FetchCorpus(context.Background(), src, "pat", dest2, quiet)
	if err == nil {
		t.Fatal("a manifest whose digest is not the pinned one was accepted")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("the error does not say the manifest was refused: %v", err)
	}
	if n := atomic.LoadInt32(&f.blobHits) - before; n != 0 {
		t.Errorf("%d blob request(s) made after the manifest failed its digest", n)
	}
	if _, err := os.Stat(dest2); err == nil {
		t.Error("a corpus was written from a manifest that failed its digest")
	}
}

// A TAG THAT MOVED MUST BE VISIBLE IN WHAT THE FETCH REPORTS. The digest returned
// is the only record of what a tag pull actually took; two pulls of `latest`
// across a re-push must report two different snapshots.
func TestATagPullReportsTheSnapshotItResolvedTo(t *testing.T) {
	const member = "etsi.duckdb"
	layer1, m1 := corpusLayer(t, member, "etsi v1", "v1")
	f := &fakeRegistry{manifest: m1, layer: layer1}
	f.serve(t)
	quiet := func(string, ...any) {}

	src := CorpusSource{Owner: "o", Image: ImageETSI, Ref: "latest", Member: member}
	first, err := FetchCorpus(context.Background(), src, "pat", filepath.Join(t.TempDir(), member), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if first != digestOf(m1) {
		t.Fatalf("reported %s, want the digest of the manifest served (%s)", first, digestOf(m1))
	}

	layer2, m2 := corpusLayer(t, member, "etsi v2", "v2")
	f.mu.Lock()
	f.manifest, f.layer = m2, layer2
	f.mu.Unlock()
	second, err := FetchCorpus(context.Background(), src, "pat", filepath.Join(t.TempDir(), member), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("`latest` moved and the fetch reported the same snapshot %s both times", first)
	}
}

// A PIN THAT NAMES NOTHING FAILS AT ONCE. A 404 on a manifest is the registry's
// answer ("no such tag or digest here"), and retrying it cost 36 s of backoff
// against ghcr.io before saying the same thing — the likeliest way to meet it is
// a pin copied from the other package.
func TestAMissingManifestIsNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"token":"test-token"}`)
		case strings.Contains(r.URL.Path, "/manifests/"):
			atomic.AddInt32(&hits, 1)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldBase, oldService := registryBase, registryService
	registryBase, registryService = srv.URL, "test"
	defer func() { registryBase, registryService = oldBase, oldService }()

	src := CorpusETSI("o", digestOf([]byte("the 3GPP manifest")))
	dest := filepath.Join(t.TempDir(), "etsi.duckdb")
	_, err := FetchCorpus(context.Background(), src, "pat", dest, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "no manifest") {
		t.Fatalf("want a 'no manifest' error, got %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("a 404 manifest was requested %d times, want 1", n)
	}
}

// RefOverride is the ONE place both consumers (the pipeline's seed steps and
// cmd/server) read the environment, so the rules are pinned here once.
func TestRefOverride(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	clear := func() {
		t.Setenv(EnvCorpusTag, "")
		t.Setenv(EnvCorpusRef3GPP, "")
		t.Setenv(EnvCorpusRefETSI, "")
	}

	t.Run("nothing set leaves the choice to the caller", func(t *testing.T) {
		clear()
		for _, img := range []string{Image3GPP, ImageETSI} {
			if ref, origin, err := RefOverride(img); ref != "" || origin != "" || err != nil {
				t.Errorf("%s: got (%q, %q, %v), want nothing", img, ref, origin, err)
			}
		}
	})
	t.Run("the shared tag applies to both packages", func(t *testing.T) {
		clear()
		t.Setenv(EnvCorpusTag, "2026-08-26")
		for _, img := range []string{Image3GPP, ImageETSI} {
			ref, origin, err := RefOverride(img)
			if err != nil || ref != "2026-08-26" || origin != "$"+EnvCorpusTag {
				t.Errorf("%s: got (%q, %q, %v)", img, ref, origin, err)
			}
		}
	})
	t.Run("a digest in the shared tag is refused, for both", func(t *testing.T) {
		clear()
		t.Setenv(EnvCorpusTag, "@"+d)
		for _, img := range []string{Image3GPP, ImageETSI} {
			if _, _, err := RefOverride(img); err == nil || !strings.Contains(err.Error(), EnvCorpusRef3GPP) {
				t.Errorf("%s: a digest in the shared variable was not refused with a way out: %v", img, err)
			}
		}
	})
	t.Run("each package's own variable wins, and only for that package", func(t *testing.T) {
		clear()
		t.Setenv(EnvCorpusTag, "latest")
		t.Setenv(EnvCorpusRefETSI, "@"+d) // the spelling crane --full-ref prints
		ref, origin, err := RefOverride(ImageETSI)
		if err != nil || ref != d || origin != "$"+EnvCorpusRefETSI {
			t.Errorf("etsi: got (%q, %q, %v), want (%q, $%s)", ref, origin, err, d, EnvCorpusRefETSI)
		}
		ref, _, err = RefOverride(Image3GPP)
		if err != nil || ref != "latest" {
			t.Errorf("3gpp took the ETSI digest or lost the shared tag: (%q, %v)", ref, err)
		}
	})
	t.Run("malformed references are refused, not sent to the registry", func(t *testing.T) {
		for _, bad := range []string{"sha256:abc", "sha256:" + strings.Repeat("AB", 32), "a tag with spaces", "x:y"} {
			clear()
			t.Setenv(EnvCorpusRef3GPP, bad)
			if _, _, err := RefOverride(Image3GPP); err == nil {
				t.Errorf("%q was accepted", bad)
			}
		}
	})
}

func TestADigestSourceIsSpelledWithAnAt(t *testing.T) {
	d := "sha256:" + strings.Repeat("0f", 32)
	if got, want := Corpus3GPP("o", "@"+d).String(), "ghcr.io/o/3gpp-corpus@"+d; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := CorpusETSI("o", "").String(), "ghcr.io/o/etsi-corpus:latest"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseFullRef(t *testing.T) {
	d := "sha256:" + strings.Repeat("12", 32)
	owner, image, digest, err := ParseFullRef("ghcr.io/kodflow/3gpp-corpus@" + d)
	if err != nil || owner != "kodflow" || image != Image3GPP || digest != d {
		t.Fatalf("got (%q, %q, %q, %v)", owner, image, digest, err)
	}
	for _, bad := range []string{
		"ghcr.io/kodflow/3gpp-corpus:latest", // a tag pins nothing
		"docker.io/kodflow/3gpp-corpus@" + d,
		"ghcr.io/kodflow@" + d,
		"ghcr.io/kodflow/a/b@" + d,
		"ghcr.io/kodflow/3gpp-corpus@sha256:short",
	} {
		if _, _, _, err := ParseFullRef(bad); err == nil {
			t.Errorf("%q was accepted as a pin", bad)
		}
	}
}
