//go:build onnx && !embed_ffi

package embed

import (
	"os"
	"path/filepath"
	"strings"
)

// These helpers resolve a model's ON-DISK location at load time. They live behind
// the onnx tag because only the real backend (and its tests) touch the filesystem;
// the default/CGO-free build computes identity from the spec without needing paths.

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

// activeTokenizerDir is the on-disk dir holding tokenizer.json. An explicit
// TokenizerDir (e.g. the fp16 variant pointing at the fp32 dir) always wins. With
// no explicit dir the tokenizer lives ALONGSIDE the model, so it must follow the
// SAME EMBED_MODEL_DIR override as activeModelDir — otherwise a single-model
// deployment (Kaggle kernel, fetch-model.sh: all files in one dir) finds model.onnx
// via the override but loses tokenizer.json to the registry's relative spec.Dir,
// silently disabling the embedder.
func activeTokenizerDir(spec ModelSpec) string {
	if spec.TokenizerDir != "" {
		return resolveBase(spec.TokenizerDir)
	}
	return activeModelDir(spec)
}
