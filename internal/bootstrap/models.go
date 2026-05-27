package bootstrap

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
	// Extract, stripping the leading "<pkg>/" component into modelsDir/onnxruntime.
	return extractTarGz(resp.Body, filepath.Join(modelsDir, "onnxruntime"), pkg+"/")
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
