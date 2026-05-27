//go:build onnx

// Package onnxrt centralises ONNX Runtime initialisation. ort.InitializeEnvironment
// is process-global and returns an error if called twice, so when both the
// embedder (internal/embed) and the reranker (internal/rerank) are active in the
// same process (the serve path embeds the query AND reranks), they must share a
// single init. This guards it behind a sync.Once + IsInitialized check.
package onnxrt

import (
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	once    sync.Once
	initErr error
)

// LibPath resolves the ONNX Runtime shared-library path from the environment,
// falling back to the in-repo location populated by scripts/fetch-model.sh.
func LibPath() string {
	if p := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); p != "" {
		return p
	}
	return "data/models/onnxruntime/lib/libonnxruntime.so"
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
