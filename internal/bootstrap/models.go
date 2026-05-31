package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ortSHA256 pins each ONNX Runtime release tarball for DefaultORTVersion. ORT is
// native code loaded via dlopen, so a swapped tarball is an RCE vector: FetchORT
// fails CLOSED on a mismatch or a missing pin. Bump these together with
// DefaultORTVersion (sha256 of the published .tgz).
var ortSHA256 = map[string]string{
	"onnxruntime-linux-x64-1.26.0":     "1254da24fb389cf39dc0ff3451ab48301740ffbfcbaf646849df92f80ee92c57",
	"onnxruntime-linux-aarch64-1.26.0": "34ff1c2d0f12e2cf3d33a0c5f82e39792e1d581fbd6968fd7c30d173654be01a",
	"onnxruntime-osx-arm64-1.26.0":     "7a1280bbb1701ea514f71828765237e7896e0f2e1cd332f1f70dbd5c3e33aca3",
	// osx-x86_64 (Intel Mac) intentionally absent: Microsoft stopped publishing
	// onnxruntime-osx-x86_64 builds at 1.25.0 (404 on the release). Intel Mac
	// stays lexical-only; verifyORT fails closed for that arch by design.
}

// verifyORT checks a downloaded ORT tarball against its pinned sha256. Unknown
// package (no pin) is a hard error — we never load an unverified native lib.
func verifyORT(pkg string, data []byte) error {
	want, ok := ortSHA256[pkg]
	if !ok {
		return fmt.Errorf("no pinned ORT checksum for %q (refusing an unverified native runtime)", pkg)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("ORT checksum mismatch for %q: got %s, want %s", pkg, got, want)
	}
	return nil
}

// DefaultORTVersion provides ORT C API 26 — needed by onnxruntime_go v1.30.x
// which requests API 25. Bump this AND ortSHA256 together (and the
// scripts/fetch-model.sh pin); the dependabot ignore keeps these coupled.
const DefaultORTVersion = "1.26.0"

// HF model sources are pinned to immutable commit SHAs (not the mutable
// `/resolve/main` ref): an upstream re-export can't silently swap the weights
// under us, and the downloaded vectors stay reproducible. Bump the SHA
// deliberately (and re-verify) when intentionally updating a model.
const (
	// BGECommit is the immutable BAAI/bge-m3 HuggingFace commit the embedder
	// weights are pinned to. Exported as the SINGLE source of truth for "which
	// BGE-M3 weights": embed.BGEModelRevision must equal its 7-hex prefix (the
	// coupling is enforced by embed's TestBGEModelRevisionMatchesPinnedCommit so a
	// future commit bump that forgets to update the embed-side identity fails CI —
	// see finding model-commit-not-in-identity / plan PR-6).
	BGECommit = "5617a9f61b028005a4858fdac845db406aefb181" // BAAI/bge-m3

	rerankCommit    = "87449985a27bbd817f13ee2338df130bdb532bad" // celinehoang/bge-reranker-v2-m3-onnx
	rerankTokCommit = "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e" // BAAI/bge-reranker-v2-m3 (tokenizer)

	bgeBase      = "https://huggingface.co/BAAI/bge-m3/resolve/" + BGECommit
	rerankBase   = "https://huggingface.co/celinehoang/bge-reranker-v2-m3-onnx/resolve/" + rerankCommit
	rerankTokURL = "https://huggingface.co/BAAI/bge-reranker-v2-m3/resolve/" + rerankTokCommit + "/tokenizer.json"
)

// EmbedderArtifacts are the BGE-M3 files (graph + external weights + tokenizer)
// under modelsDir/bge-m3. SHA256 left empty: HuggingFace is the source of truth
// and a release manifest pins hashes for the DB, not these large model blobs.
func EmbedderArtifacts(modelsDir string) []Artifact {
	d := filepath.Join(modelsDir, "bge-m3")
	return []Artifact{
		{URL: bgeBase + "/onnx/model.onnx", Dest: filepath.Join(d, "model.onnx")},
		{URL: bgeBase + "/onnx/model.onnx_data", Dest: filepath.Join(d, "model.onnx_data")},
		{URL: bgeBase + "/onnx/Constant_7_attr__value", Dest: filepath.Join(d, "Constant_7_attr__value")},
		{URL: bgeBase + "/tokenizer.json", Dest: filepath.Join(d, "tokenizer.json")},
	}
}

// RerankerArtifacts are the bge-reranker-v2-m3 ONNX files under modelsDir.
func RerankerArtifacts(modelsDir string) []Artifact {
	d := filepath.Join(modelsDir, "bge-reranker-v2-m3")
	return []Artifact{
		{URL: rerankBase + "/model.onnx", Dest: filepath.Join(d, "model.onnx")},
		{URL: rerankBase + "/model.onnx_data", Dest: filepath.Join(d, "model.onnx_data")},
		{URL: rerankTokURL, Dest: filepath.Join(d, "tokenizer.json")},
	}
}

// ortPackage returns the ONNX Runtime release archive name + shared-lib name for
// the current platform.
func ortPackage(version string) (pkg, lib string, err error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "onnxruntime-linux-x64-" + version, "libonnxruntime.so", nil
	case "linux/arm64":
		return "onnxruntime-linux-aarch64-" + version, "libonnxruntime.so", nil
	case "darwin/arm64":
		return "onnxruntime-osx-arm64-" + version, "libonnxruntime.dylib", nil
	// darwin/amd64 (Intel Mac): Microsoft stopped publishing onnxruntime-osx-x86_64
	// at 1.25.0 — no upstream tarball exists. Intel Mac stays LEXICAL-ONLY (the
	// onnx variant degrades to Disabled{} since FetchORT fails here).
	default:
		return "", "", fmt.Errorf("no ONNX Runtime build for %s/%s (Intel Mac unsupported since ORT 1.25)", runtime.GOOS, runtime.GOARCH)
	}
}

// ORTLibPath is where FetchORT places the shared library.
func ORTLibPath(modelsDir, version string) (string, error) {
	_, lib, err := ortPackage(version)
	if err != nil {
		return "", err
	}
	return filepath.Join(modelsDir, "onnxruntime", "lib", lib), nil
}

// FetchORT downloads + extracts the ONNX Runtime archive for this platform into
// modelsDir/onnxruntime (idempotent: skips if the shared lib is already there).
func FetchORT(ctx context.Context, modelsDir, version string) error {
	pkg, _, err := ortPackage(version)
	if err != nil {
		return err
	}
	libPath, _ := ORTLibPath(modelsDir, version)
	if fileExists(libPath) {
		return nil
	}
	url := fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/%s.tgz", version, pkg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("get ORT: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get ORT: status %s", resp.Status)
	}
	// Buffer + verify the sha256 BEFORE extracting: ORT is dlopen'd native code,
	// so an unverified/tampered tarball is an RCE vector (fail closed). Cap the
	// read so a malicious/broken peer can't OOM us with an unbounded body (ORT
	// tarballs are tens of MB; a real one over the cap just fails the checksum).
	const maxORTBytes = 256 << 20 // 256 MiB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxORTBytes))
	if err != nil {
		return fmt.Errorf("read ORT: %w", err)
	}
	if err := verifyORT(pkg, body); err != nil {
		return err
	}
	// Extract, stripping the leading "<pkg>/" component into modelsDir/onnxruntime.
	return extractTarGz(bytes.NewReader(body), filepath.Join(modelsDir, "onnxruntime"), pkg+"/")
}

func extractTarGz(r io.Reader, destDir, stripPrefix string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.HasPrefix(hdr.Name, stripPrefix) {
			continue // not under the expected top-level dir; skip defensively
		}
		name := strings.TrimPrefix(hdr.Name, stripPrefix)
		if name == "" {
			continue
		}
		// Guard against path traversal (zip-slip).
		target := filepath.Join(destDir, name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && target != destDir {
			return fmt.Errorf("unsafe tar path: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil { //nolint:gosec // trusted ORT release tarball
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}
