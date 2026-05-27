package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// runBootstrap implements `mcp-3gpp bootstrap`: provision the per-user cache
// with the indexed DuckDB snapshot, and with --semantic the ONNX models +
// runtime, so a fresh machine can serve. These are plain artifact downloads
// (DB from a release, models from HuggingFace) — the binary never pulls a
// container nor runs a daemon.
func runBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	cache := fs.String("cache", "", "cache dir (default: per-user cache, or $MCP3GPP_CACHE)")
	dbURL := fs.String("db-url", "", "URL of the indexed DuckDB snapshot (.duckdb or .duckdb.zst)")
	dbSHA := fs.String("db-sha256", "", "expected SHA-256 of the decompressed DB (recommended)")
	semantic := fs.Bool("semantic", false, "also fetch BGE-M3 + reranker models and ONNX Runtime (~5 GB)")
	ortVer := fs.String("ort-version", bootstrap.DefaultORTVersion, "ONNX Runtime version")
	_ = fs.Parse(args)

	if *cache != "" {
		_ = os.Setenv("MCP3GPP_CACHE", *cache)
	}
	ctx := context.Background()

	dbPath, err := bootstrap.DBPath()
	if err != nil {
		return err
	}
	switch {
	case *dbURL != "":
		fmt.Fprintf(os.Stderr, "[bootstrap] DB → %s\n", dbPath)
		if err := bootstrap.Fetch(ctx, bootstrap.Artifact{URL: *dbURL, SHA256: *dbSHA, Dest: dbPath}); err != nil {
			return err
		}
	case !fileExists(dbPath):
		return fmt.Errorf("no --db-url given and no cached DB at %s", dbPath)
	}

	if *semantic {
		models, err := bootstrap.ModelsDir()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[bootstrap] ONNX Runtime + models → %s (large, ~5 GB)\n", models)
		if err := bootstrap.FetchORT(ctx, models, *ortVer); err != nil {
			return err
		}
		arts := append(bootstrap.EmbedderArtifacts(models), bootstrap.RerankerArtifacts(models)...)
		if err := bootstrap.FetchAll(ctx, arts); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stderr, "[bootstrap] done")
	return nil
}

// resolveDB returns the DB path serve should open: the explicit/-default path if
// it exists, else the cached snapshot. Returns an actionable error if neither
// exists, rather than failing deep in DuckDB.
func resolveDB(flagPath string) (string, error) {
	if fileExists(flagPath) {
		return flagPath, nil
	}
	cached, err := bootstrap.DBPath()
	if err == nil && fileExists(cached) {
		return cached, nil
	}
	return "", fmt.Errorf("no DB at %q and none cached at %q — run `mcp-3gpp bootstrap --db-url <url>` first", flagPath, cached)
}

// pointModelsAtCache lets the (onnx) embedder/reranker find cache-bootstrapped
// models without the user exporting env vars: if a var is unset and the cached
// file exists, point at it. No-op on the lexical build. Safe + idempotent.
func pointModelsAtCache() {
	models, err := bootstrap.ModelsDir()
	if err != nil {
		return
	}
	if os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH") == "" {
		if lib, err := bootstrap.ORTLibPath(models, bootstrap.DefaultORTVersion); err == nil && fileExists(lib) {
			_ = os.Setenv("ONNXRUNTIME_SHARED_LIBRARY_PATH", lib)
		}
	}
	setIfPresent("BGE_M3_DIR", models+"/bge-m3", "model.onnx")
	setIfPresent("BGE_RERANKER_DIR", models+"/bge-reranker-v2-m3", "model.onnx")
}

func setIfPresent(env, dir, sentinel string) {
	if os.Getenv(env) == "" && fileExists(dir+"/"+sentinel) {
		_ = os.Setenv(env, dir)
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
