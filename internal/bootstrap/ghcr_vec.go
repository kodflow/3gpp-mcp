package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// vecImage is the GHCR package holding the Option-B vector sub-bases; the repo is
// "<owner>/<vecImage>" (e.g. kodflow/3gpp-vec). Published by corpus-matrix's
// publish-vec job as an OCI artifact (one zstd layer per sub-base + the manifest).
const vecImage = "3gpp-vec"

// FetchVecBases pulls the vector sub-bases + vec-manifest.json from the public
// GHCR OCI artifact ghcr.io/<owner>/3gpp-vec:latest into dir (decompressing each
// .zst), and returns the local vec-manifest.json path to hand to serve's
// --vec-manifest. Anonymous pull (public package); any error is returned so the
// caller can degrade to single-DB / lexical.
func FetchVecBases(ctx context.Context, owner, dir string) (string, error) {
	repo := owner + "/" + vecImage
	tok, err := ghcrToken(ctx, repo)
	if err != nil {
		return "", err
	}
	layers, err := ghcrLayers(ctx, repo, "latest", tok)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	manifestPath := ""
	for _, l := range layers {
		if l.title == "" {
			continue
		}
		dest := filepath.Join(dir, strings.TrimSuffix(l.title, ".zst"))
		if err := ghcrPullBlob(ctx, repo, l.digest, tok, dest, strings.HasSuffix(l.title, ".zst")); err != nil {
			return "", fmt.Errorf("pull %s: %w", l.title, err)
		}
		if filepath.Base(dest) == "vec-manifest.json" {
			manifestPath = dest
		}
	}
	if manifestPath == "" {
		return "", fmt.Errorf("vec-manifest.json missing from GHCR artifact %s", repo)
	}
	return manifestPath, nil
}

// ghcrToken fetches an anonymous pull token for a public GHCR repository.
func ghcrToken(ctx context.Context, repo string) (string, error) {
	u := "https://ghcr.io/token?service=ghcr.io&scope=repository:" + repo + ":pull"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ghcr token: %s", resp.Status)
	}
	var t struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	return t.Token, nil
}

type ghcrLayer struct{ digest, title string }

// ghcrLayers reads an OCI manifest and returns its layers' digest + filename
// (the org.opencontainers.image.title annotation set by `oras push`).
func ghcrLayers(ctx context.Context, repo, ref, token string) ([]ghcrLayer, error) {
	u := "https://ghcr.io/v2/" + repo + "/manifests/" + ref
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.oci.image.index.v1+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ghcr manifest: %s", resp.Status)
	}
	var m struct {
		Layers []struct {
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"layers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	out := make([]ghcrLayer, 0, len(m.Layers))
	for _, l := range m.Layers {
		out = append(out, ghcrLayer{digest: l.Digest, title: l.Annotations["org.opencontainers.image.title"]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ghcr manifest %s has no layers", repo)
	}
	return out, nil
}

// ghcrPullBlob downloads a blob by digest to dest, zstd-decompressing if asked.
func ghcrPullBlob(ctx context.Context, repo, digest, token, dest string, decompress bool) error {
	u := "https://ghcr.io/v2/" + repo + "/blobs/" + digest
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("blob %s: %s", digest, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var src io.Reader = resp.Body
	var zr *zstd.Decoder
	if decompress {
		if zr, err = zstd.NewReader(resp.Body); err != nil {
			_ = f.Close()
			return err
		}
		src = zr
	}
	_, copyErr := io.Copy(f, src)
	if zr != nil {
		zr.Close()
	}
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dest) // atomic
}
