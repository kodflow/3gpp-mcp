//go:build onnx

// Package onnxrt centralises ONNX Runtime initialisation. ort.InitializeEnvironment
// is process-global and returns an error if called twice, so when both the
// embedder (internal/embed) and the reranker (internal/rerank) are active in the
// same process (the serve path embeds the query AND reranks), they must share a
// single init. This guards it behind a sync.Once + IsInitialized check.
package onnxrt

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	once    sync.Once
	initErr error
)

// LibPath resolves the ONNX Runtime shared-library path from the environment,
// falling back to where this platform actually puts it.
//
// THE FALLBACK USED TO BE THE LINUX FILENAME, UNCONDITIONALLY. That is not a
// default in a helper both the Linux image and the Windows dev build call — it is
// a guarantee that one of them never finds the library. internal/rerank checks
// fileExists(LibPath()) before building its session, so on Windows the
// cross-encoder could never start; measured, the reason it reported was
//
//	the ONNX runtime library is missing at
//	data/models/onnxruntime/lib/libonnxruntime.so
//
// on a machine carrying onnxruntime.dll. The embedder stayed live throughout,
// because it reaches ORT through the Rust cdylib and never asks this function —
// which is why the gap survived: the two arms that share a runtime do not share
// the path to it.
//
// internal/bootstrap has no Windows entry (Microsoft's tarballs are Linux/macOS,
// and ortPackage errors for windows), so the DLLs are staged NEXT TO THE BINARY
// by the build-serve step — which is also the first place Windows itself looks
// when resolving them. Prefer that, then the plain name so the OS search path
// still applies.
func LibPath() string {
	if p := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "windows":
		if exe, err := os.Executable(); err == nil {
			if beside := filepath.Join(filepath.Dir(exe), "onnxruntime.dll"); fileExists(beside) {
				return beside
			}
		}
		return "onnxruntime.dll"
	case "darwin":
		return "data/models/onnxruntime/lib/libonnxruntime.dylib"
	default:
		return "data/models/onnxruntime/lib/libonnxruntime.so"
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Init loads the shared library and initialises the global ORT environment
// exactly once. Safe to call from any package, any number of times.
func Init() error {
	once.Do(func() {
		ort.SetSharedLibraryPath(LibPath())
		if !ort.IsInitialized() {
			initErr = ort.InitializeEnvironment()
		}
	})
	return initErr
}
