package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// The rolling "latest" GitHub release is the single source of the indexed DB.
// There is no version history: the DB's identity is its sha256 sidecar.
const (
	defaultDBURL    = "https://github.com/kodflow/3gpp-mcp/releases/latest/download/3gpp.duckdb.zst"
	defaultDBSHAURL = "https://github.com/kodflow/3gpp-mcp/releases/latest/download/3gpp.duckdb.sha256"
)

// remoteSHA fetches the published sha256 sidecar and returns its hash field
// (lowercase). Best-effort: returns "" on any error so callers can degrade.
func remoteSHA(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := bootstrap.HTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return ""
	}
	if fields := strings.Fields(string(b)); len(fields) > 0 {
		return strings.ToLower(fields[0])
	}
	return ""
}

// ensureDB guarantees the per-user cache holds a usable indexed DB, pulling it
// from the rolling "latest" release when absent and refreshing it when the
// published sha256 differs from the cached file (the DB *is* the state).
//
// Degrade-don't-block: any network failure is non-fatal when a cached DB is
// already present — serve keeps working offline against what it has.
func ensureDB(ctx context.Context, allowUpdate bool) (string, error) {
	dbPath, err := bootstrap.DBPath()
	if err != nil {
		return "", err
	}
	have := fileExists(dbPath)

	if have && !allowUpdate {
		return dbPath, nil
	}

	want := remoteSHA(ctx, defaultDBSHAURL) // "" if unreachable
	if have {
		if want == "" {
			return dbPath, nil // offline: keep cache
		}
		if cur, err := bootstrap.SHA256File(dbPath); err == nil && cur == want {
			return dbPath, nil // already current
		}
		fmt.Fprintln(os.Stderr, "[3gpp-mcp] DB update available — pulling latest…")
	} else {
		fmt.Fprintln(os.Stderr, "[3gpp-mcp] no cached DB — pulling latest…")
	}

	if err := bootstrap.Fetch(ctx, bootstrap.Artifact{URL: defaultDBURL, SHA256: want, Dest: dbPath}); err != nil {
		if have { // degrade: prefer a stale-but-working DB over failing serve
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] DB update failed (%v) — using cached DB\n", err)
			return dbPath, nil
		}
		return "", fmt.Errorf("pull DB from %s: %w", defaultDBURL, err)
	}
	return dbPath, nil
}

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
	skipDB := fs.Bool("skip-db", false, "do not fetch the DB (models/ONNX Runtime only) — for image bakes that provide the DB separately")
	semantic := fs.Bool("semantic", false, "also fetch BGE-M3 + reranker models and ONNX Runtime (~5 GB)")
	noReranker := fs.Bool("no-reranker", false, "with --semantic, fetch only the BGE-M3 embedder + ONNX Runtime, NOT the optional reranker (smaller, fewer flaky fetches)")
	ortVer := fs.String("ort-version", bootstrap.DefaultORTVersion, "ONNX Runtime version")
	_ = fs.Parse(args)

	if *cache != "" {
		_ = os.Setenv("MCP3GPP_CACHE", *cache)
	}
	ctx := context.Background()

	if !*skipDB {
		dbPath, err := bootstrap.DBPath()
		if err != nil {
			return err
		}
		// No --db-url? Default to the rolling "latest" release, resolving its
		// published sha256 so the download is verified out of the box.
		url, sha := *dbURL, *dbSHA
		if url == "" {
			url = defaultDBURL
			if sha == "" {
				sha = remoteSHA(ctx, defaultDBSHAURL)
			}
		}
		fmt.Fprintf(os.Stderr, "[bootstrap] DB %s → %s\n", url, dbPath)
		if err := bootstrap.Fetch(ctx, bootstrap.Artifact{URL: url, SHA256: sha, Dest: dbPath}); err != nil {
			return err
		}
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
		arts := bootstrap.EmbedderArtifacts(models)
		if !*noReranker {
			arts = append(arts, bootstrap.RerankerArtifacts(models)...)
		}
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
	// EMBED_MODEL_DIR points the active embed model (default bge-m3) at the cached
	// copy, so serve finds it without the user exporting anything (registry seam).
	setIfPresent("EMBED_MODEL_DIR", models+"/bge-m3", "model.onnx")
	setIfPresent("BGE_RERANKER_DIR", models+"/bge-reranker-v2-m3", "model.onnx")
}

func setIfPresent(env, dir, sentinel string) {
	if os.Getenv(env) == "" && fileExists(dir+"/"+sentinel) {
		_ = os.Setenv(env, dir)
	}
}

// cachedDBPath is the per-user cache DB path (empty on resolution error).
func cachedDBPath() string { p, _ := bootstrap.DBPath(); return p }

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
