package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// WHERE THE CORPUS COMES FROM, AND WHY IT MOVED.
//
// It used to be the rolling `latest` GitHub Release asset, hardcoded here as the
// DEFAULT. That was wrong twice over, and the default is what made it serious —
// the out-of-the-box path was the non-compliant one:
//
//   - DATA_NOTICE.md forbids it. The DB holds verbatim 3GPP/ETSI clause text, and
//     the notice is explicit that no Release asset of this public repository may
//     carry a full-text DuckDB database.
//   - It no longer fits. GitHub caps an asset at 2 GB; the content-addressed
//     corpus is 12.36 GB (~7.9 GB compressed).
//
// The corpus therefore comes from the PRIVATE GHCR package that
// scripts/local/publish-corpus.sh already publishes. It needs a credential, by
// design — see internal/bootstrap/ghcr_corpus.go.
//
// `--db-url` survives as an explicit override for a mirror you host yourself.
// Nothing points at a github.com release asset any more, which is what
// TestBootstrapDefaultsAreNotAPublicReleaseAsset pins.
const (
	// envGHCROwner / envCorpusTag let a deployment repoint the corpus without a
	// rebuild (a fork, a staging tag) while keeping the default zero-config.
	envGHCROwner = "MCP3GPP_GHCR_OWNER"
	envCorpusTag = "MCP3GPP_CORPUS_TAG"
)

// corpusSource is the 3GPP corpus package this binary provisions from.
func corpusSource() bootstrap.CorpusSource {
	return bootstrap.Corpus3GPP(os.Getenv(envGHCROwner), os.Getenv(envCorpusTag))
}

// etsiSource is the ETSI Lawful-Interception corpus, served alongside and never
// merged (CLAUDE.md §13).
func etsiSource() bootstrap.CorpusSource {
	return bootstrap.CorpusETSI(os.Getenv(envGHCROwner), os.Getenv(envCorpusTag))
}

// bootstrapLog prefixes progress the same way the rest of the binary does.
func bootstrapLog(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[bootstrap] "+format+"\n", args...)
}

// credentialAdvice is the one message that turns "401" into something a user can
// act on. It names every accepted source, because the failure it explains is the
// first thing a new user hits.
const credentialAdvice = `the corpus package is PRIVATE — it carries verbatim 3GPP/ETSI specification text.

Provide a GitHub token with read:packages, by any of:
  - export GHCR_PAT=<token>          (or GITHUB_TOKEN, on a CI runner)
  - mcp-3gpp bootstrap --ghcr-token <token>
  - write it to .local/ghcr.pat      (when running from a checkout; gitignored)

Create one at https://github.com/settings/tokens/new — tick read:packages.
If you build the corpus yourself, skip all of this and point --db at it.`

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

	src := corpusSource()
	pat, origin, cerr := bootstrap.GHCRCredential("")
	if cerr != nil {
		if have { // degrade: a cached corpus serves fine without a credential
			return dbPath, nil
		}
		return "", fmt.Errorf("no cached corpus at %s and no credential to fetch one.\n\n%s", dbPath, credentialAdvice)
	}

	// One manifest GET decides whether a 7.9 GB transfer is needed at all.
	published, ierr := bootstrap.CorpusIdentity(ctx, src, pat)
	switch {
	case ierr != nil && have:
		// Offline, throttled, or a token that lost its scope: keep serving.
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] could not check %s for updates (%v) — using the cached corpus\n", src, ierr)
		return dbPath, nil
	case ierr != nil:
		return "", fmt.Errorf("no cached corpus and %s is unreachable: %w", src, ierr)
	}

	if have {
		if bootstrap.CachedCorpusIdentity(dbPath) == published {
			return dbPath, nil // already current
		}
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] a newer corpus is published on %s — pulling it (credential from %s)…\n", src, origin)
	} else {
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] no cached corpus — pulling %s (credential from %s). This is large; it resumes if interrupted.\n", src, origin)
	}

	if err := bootstrap.FetchCorpus(ctx, src, pat, dbPath, bootstrapLog); err != nil {
		if have { // degrade: prefer a stale-but-working corpus over failing serve
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] corpus update failed (%v) — using the cached corpus\n", err)
			return dbPath, nil
		}
		return "", fmt.Errorf("pull the corpus from %s: %w", src, err)
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
	dbURL := fs.String("db-url", "", "OVERRIDE: fetch the corpus from this URL (.duckdb or .duckdb.zst) instead of the GHCR package — for a mirror you host yourself")
	dbSHA := fs.String("db-sha256", "", "with --db-url, the expected SHA-256 of the decompressed DB (recommended)")
	etsiURL := fs.String("etsi-url", "", "with --db-url, fetch the ETSI corpus from this URL too (a 3GPP mirror URL says nothing about where the ETSI one lives)")
	etsiSHA := fs.String("etsi-sha256", "", "with --etsi-url, the expected SHA-256 of the decompressed ETSI DB (recommended)")
	ghcrToken := fs.String("ghcr-token", "", "GitHub token with read:packages for the private corpus package (default: $GHCR_PAT, $GITHUB_TOKEN, or .local/ghcr.pat)")
	force := fs.Bool("force", false, "re-fetch the corpus even when the cache already holds the published one")
	skipDB := fs.Bool("skip-db", false, "do not fetch the corpus (models/ONNX Runtime only) — for image bakes that provide it separately")
	withETSI := fs.Bool("etsi", false, "also fetch the ETSI Lawful-Interception corpus (23 MB), served alongside the 3GPP one")
	semantic := fs.Bool("semantic", false, "also fetch BGE-M3 + reranker models and ONNX Runtime (~5 GB)")
	noReranker := fs.Bool("no-reranker", false, "with --semantic, fetch only the BGE-M3 embedder + ONNX Runtime, NOT the optional reranker (smaller, fewer flaky fetches)")
	ortVer := fs.String("ort-version", bootstrap.DefaultORTVersion, "ONNX Runtime version")
	_ = fs.Parse(args)

	// The symmetric silence: --etsi-url is only consulted on the mirror path, so
	// passing it without --db-url would be dropped as quietly as --etsi once was.
	if *etsiURL != "" && *dbURL == "" {
		return fmt.Errorf("--etsi-url only applies with --db-url; on the default path the ETSI corpus comes from its own private GHCR package")
	}

	if *cache != "" {
		_ = os.Setenv("MCP3GPP_CACHE", *cache)
	}
	ctx := context.Background()

	if !*skipDB {
		dbPath, err := bootstrap.DBPath()
		if err != nil {
			return err
		}
		switch {
		case *dbURL != "":
			// Explicit mirror: a plain verified download, exactly as before.
			bootstrapLog("corpus %s → %s", *dbURL, dbPath)
			if err := bootstrap.Fetch(ctx, bootstrap.Artifact{URL: *dbURL, SHA256: *dbSHA, Dest: dbPath}); err != nil {
				return err
			}
			// --etsi used to be accepted here and silently dropped: the ETSI fetch
			// lived only in the default branch, so `bootstrap --db-url … --etsi`
			// exited 0 having created no etsi.duckdb, and serve then ran 3GPP-only
			// with nothing in the output to explain the missing half.
			if *withETSI {
				if *etsiURL == "" {
					return fmt.Errorf("--etsi with --db-url also needs --etsi-url: a mirror that carries your 3GPP corpus has to carry the ETSI one too, and the private GHCR package is consulted only on the default path")
				}
				etsiPath := filepath.Join(filepath.Dir(dbPath), "etsi.duckdb")
				bootstrapLog("corpus %s → %s", *etsiURL, etsiPath)
				if err := bootstrap.Fetch(ctx, bootstrap.Artifact{URL: *etsiURL, SHA256: *etsiSHA, Dest: etsiPath}); err != nil {
					return err
				}
			}
		default:
			if *force {
				// The identity recorded beside the cache is what marks it current,
				// so removing it is precisely what makes the next fetch transfer
				// again. Without this the check added to FetchCorpus would leave a
				// corrupt-but-current cache with no way back short of rm.
				_ = os.Remove(bootstrap.DigestPath(dbPath))
			}
			pat, origin, cerr := bootstrap.GHCRCredential(*ghcrToken)
			if cerr != nil {
				return fmt.Errorf("%s", credentialAdvice)
			}
			bootstrapLog("corpus %s → %s (credential from %s)", corpusSource(), dbPath, origin)
			if err := bootstrap.FetchCorpus(ctx, corpusSource(), pat, dbPath, bootstrapLog); err != nil {
				return err
			}
			if *withETSI {
				etsiPath := filepath.Join(filepath.Dir(dbPath), "etsi.duckdb")
				if *force {
					_ = os.Remove(bootstrap.DigestPath(etsiPath))
				}
				bootstrapLog("corpus %s → %s", etsiSource(), etsiPath)
				if err := bootstrap.FetchCorpus(ctx, etsiSource(), pat, etsiPath, bootstrapLog); err != nil {
					return err
				}
			}
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
	// EMBED_MODEL_DIR points the embedder at the cached copy of the model the
	// REGISTRY says is active — not at a hardcoded "bge-m3".
	//
	// The distinction is load-bearing, because the two halves of the embedder
	// resolve the model differently: the Go side reads the registry, while the
	// Rust cdylib (rust/embed-core, ort backend) reads EMBED_MODEL_DIR and falls
	// back to the literal relative path "data/models/bge-m3". So on a deployment
	// that ships the dual-head export INSTEAD of the dense-only one — which is
	// what the image does, because only the ACTIVE registry entry decides whether
	// the sparse arm exists at all — this used to find no bge-m3 directory, leave
	// EMBED_MODEL_DIR unset, and let the cdylib look for a relative path that does
	// not exist under the working directory. Semantic search would then fail at the
	// first query, in an image whose corpus is full of vectors.
	//
	// Only the LAST path element of the registry's dir is used, rejoined onto the
	// cache: the built-in registry names a repo-relative path ("data/models/…")
	// while a baked one names an absolute container path, and what is being
	// resolved here is "the cached copy", which lives under the cache either way.
	//
	// Falling back to "bge-m3" keeps every existing cache layout working: that is
	// where a bootstrap without a registry override puts the weights.
	active := "bge-m3"
	if d := strings.TrimSpace(embed.ActiveModel().Dir); d != "" {
		active = filepath.Base(d)
	}
	setIfPresent("EMBED_MODEL_DIR", filepath.Join(models, active), "model.onnx")
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
