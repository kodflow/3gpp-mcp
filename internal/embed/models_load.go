//go:build onnx

package embed

import (
	"os"
	"path/filepath"
	"strings"
)

// These helpers resolve a model's ON-DISK location at load time. They live behind
// the onnx tag because only the real backend (and its tests) touch the filesystem;
// the default/CGO-free build computes identity from the spec without needing paths.

// tokenizerDirOrModel returns the tokenizer dir, defaulting to the model dir.
func (m ModelSpec) tokenizerDirOrModel() string {
	if m.TokenizerDir != "" {
		return m.TokenizerDir
	}
	return m.Dir
}

// resolveBase makes a (possibly relative) model dir portable across deployments:
// an absolute dir is returned as-is; a relative one is joined onto EMBED_MODELS_BASE
// when set (default: the process cwd). This is the DEPLOYMENT-path seam — kept in
// env, separate from the registry's declarative identity/wiring — so the same
// models.yaml works from the repo root, a test's package-dir cwd, or a Kaggle box.
func resolveBase(dir string) string {
	if !filepath.IsAbs(dir) {
		if base := strings.TrimSpace(os.Getenv("EMBED_MODELS_BASE")); base != "" {
			return filepath.Join(base, dir)
		}
	}
	return dir
}

// activeModelDir is the on-disk model dir: EMBED_MODEL_DIR (absolute override for
// the active model — used by single-model deployments like the Kaggle kernel) wins,
// else the spec's dir resolved via resolveBase.
func activeModelDir(spec ModelSpec) string {
	if o := strings.TrimSpace(os.Getenv("EMBED_MODEL_DIR")); o != "" {
		return o
	}
	return resolveBase(spec.Dir)
}
