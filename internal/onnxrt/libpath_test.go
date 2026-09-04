//go:build onnx

package onnxrt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLibPathIsPerPlatform pins a default that was the LINUX filename on every
// platform.
//
// internal/rerank checks fileExists(LibPath()) before it builds a session, so on
// Windows the cross-encoder could never start. Measured on this machine, with
// onnxruntime.dll present beside the binary, server_info reported
//
//	"reranker": false
//	"reranker_reason": "the ONNX runtime library is missing at
//	                    data/models/onnxruntime/lib/libonnxruntime.so"
//
// The embedder stayed live the whole time, because it reaches ORT through the
// Rust cdylib and never calls this function — which is exactly why the gap
// survived: the two arms share a runtime but not the path to it.
func TestLibPathIsPerPlatform(t *testing.T) {
	t.Setenv("ONNXRUNTIME_SHARED_LIBRARY_PATH", "")

	got := LibPath()
	switch runtime.GOOS {
	case "windows":
		if !strings.HasSuffix(got, ".dll") {
			t.Errorf("LibPath() = %q on windows; a .so can never exist here, so the reranker is unreachable", got)
		}
	case "darwin":
		if !strings.HasSuffix(got, ".dylib") {
			t.Errorf("LibPath() = %q on darwin, want a .dylib", got)
		}
	default:
		if !strings.HasSuffix(got, ".so") {
			t.Errorf("LibPath() = %q on %s, want a .so", got, runtime.GOOS)
		}
	}

	// The environment still wins: the image sets it from pointModelsAtCache, and
	// an operator pointing at a private build must not be second-guessed.
	want := filepath.Join(t.TempDir(), "custom-runtime.bin")
	if err := os.WriteFile(want, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONNXRUNTIME_SHARED_LIBRARY_PATH", want)
	if got := LibPath(); got != want {
		t.Errorf("LibPath() = %q, want the explicit %q", got, want)
	}
}
