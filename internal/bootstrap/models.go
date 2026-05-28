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
	"onnxruntime-linux-x64-1.20.1":     "67db4dc1561f1e3fd42e619575c82c601ef89849afc7ea85a003abbac1a1a105",
	"onnxruntime-linux-aarch64-1.20.1": "ae4fedbdc8c18d688c01306b4b50c63de3445cdf2dbd720e01a2fa3810b8106a",
	"onnxruntime-osx-arm64-1.20.1":     "b678fc3c2354c771fea4fba420edeccfba205140088334df801e7fc40e83a57a",
	"onnxruntime-osx-x86_64-1.20.1":    "0f73006813af2a1a5d1723ed7dfb694fc629d15037124081bb61b7bf7d99fc78",
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

// DefaultORTVersion matches onnxruntime_go v1.14.0 (ORT C API 20).
const DefaultORTVersion = "1.20.1"

const (
	bgeBase      = "https://huggingface.co/BAAI/bge-m3/resolve/main"
	rerankBase   = "https://huggingface.co/celinehoang/bge-reranker-v2-m3-onnx/resolve/main"
	rerankTokURL = "https://huggingface.co/BAAI/bge-reranker-v2-m3/resolve/main/tokenizer.json"
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
	case "darwin/amd64":
		return "onnxruntime-osx-x86_64-" + version, "libonnxruntime.dylib", nil
	default:
		return "", "", fmt.Errorf("no ONNX Runtime build for %s/%s", runtime.GOOS, runtime.GOARCH)
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
	// so an unverified/tampered tarball is an RCE vector (fail closed).
	body, err := io.ReadAll(resp.Body)
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
