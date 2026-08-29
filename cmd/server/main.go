// Command server runs the 3gpp-mcp MCP server over stdio (CLAUDE.md §5).
//
// Usage:
//
//	server serve [--db data/3gpp.duckdb] [--release Rel-19]  # speak MCP on stdio
//	server bootstrap [--db-url URL] [--semantic]             # provision the cache
//	server version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/mcp"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

// servedDBPath is the resolved DuckDB file this process actually opened. It feeds
// the dashboard provenance block (db_path/size/mtime) so an operator can tell, by
// curl alone, WHICH data layer is being served — the missing signal behind the
// stale-data-layer incident ([[project_served_stale_data_layer]]).
var servedDBPath string

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
	case "bootstrap":
		if err := runBootstrap(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bootstrap:", err)
			os.Exit(1)
		}
	case "skill":
		// Print the embedded /3gpp skill (the same bytes the HTTP landing serves at
		// /skill/3gpp.md) so a stdio-only install can fetch it FROM THE BINARY —
		// always version-matched, never from the repository:
		//   docker run --rm <image> skill > ~/.claude/skills/3gpp/SKILL.md
		fmt.Print(skill3gpp)
	case "prefetch-extensions":
		// IMAGE-BUILD step: install fts+vss into ~/.duckdb so serve never needs
		// the network at startup (no-egress / read-only deployments keep BM25 +
		// HNSW instead of degrading to LIKE + exact-scan). Hard-fails the build
		// when an extension cannot be installed AND loaded.
		if err := prefetchExtensions(); err != nil {
			fmt.Fprintln(os.Stderr, "prefetch-extensions:", err)
			os.Exit(1)
		}
	case "check-data":
		// IMAGE-BUILD GUARD: assert the INHERITED data layer is fully indexed
		// (FTS present; frozen HNSW when vectors exist). Run as a RUN step in the
		// full image so a stale 3gpp-data digest FAILS the build instead of
		// silently shipping a LIKE/exact-scan server (the stale-data-layer
		// incident, [[project_served_stale_data_layer]]). cmd/validate stays the
		// full promotion gate; this is the shipped binary self-checking at build.
		if err := checkData(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "check-data:", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println(Version)
	default:
		usage()
		os.Exit(2)
	}
}

// loadVecManifest reads a vec-manifest ({"sub_bases":["…duckdb",…]}) and returns
// the sub-base DB paths, resolved relative to the manifest's directory.
func loadVecManifest(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m struct {
		SubBases []string `json:"sub_bases"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if len(m.SubBases) == 0 {
		return nil, fmt.Errorf("manifest lists no sub_bases")
	}
	dir := filepath.Dir(path)
	out := make([]string, 0, len(m.SubBases))
	for _, p := range m.SubBases {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		out = append(out, p)
	}
	return out, nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "data/3gpp.duckdb", "DuckDB snapshot path")
	etsiDB := fs.String("etsi-db", "", "optional ETSI corpus DuckDB (etsi.duckdb) served ALONGSIDE the 3GPP DB — kept SPLIT, not merged; get_spec/list_releases route 'ETSI …' ids here and list_specs unions it. Empty = 3GPP only.")
	release := fs.String("release", "", "baseline release every answer is scoped to (e.g. Rel-17); empty = latest")
	writable := fs.Bool("writable", false, "open writable (default: read-only — the corruption-safe serve posture)")
	noUpdate := fs.Bool("no-update", os.Getenv("MCP3GPP_NO_UPDATE") != "", "don't check the corpus package for a newer corpus at startup (air-gapped, or to pin what you have)")
	vecManifest := fs.String("vec-manifest", "", "Option B: JSON listing per-series vectorized sub-bases to ATTACH for scatter-gather vector search (empty = single-DB vectors)")
	vecGHCR := fs.String("vec-ghcr", "", "Option B: pull vector sub-bases from ghcr.io/<owner>/3gpp-vec:latest into the cache and serve them (empty = off)")
	httpAddr := fs.String("http", "", "serve MCP over Streamable HTTP on this addr (e.g. 127.0.0.1:8765) + a landing page at /; empty = stdio (the default, unchanged). A non-loopback bind exposes the corpus — gate it.")
	allowLexicalFallback := fs.Bool("allow-lexical-fallback", os.Getenv("MCP3GPP_ALLOW_LEXICAL_FALLBACK") != "",
		"serve lexical-only when the DB's embedding model does not match this binary's, instead of refusing to start")
	_ = fs.Parse(args)

	// Let the (onnx) embedder/reranker transparently use cache-bootstrapped
	// models when the user hasn't exported the paths. No-op on the lexical build.
	pointModelsAtCache()

	ctx := context.Background()

	// HTTP mode: bring /healthz up BEFORE the (possibly minutes-long) DB + vector
	// bootstrap, so a puller sees 503 "loading" during startup and 200 "ready" only
	// once the corpus is actually queryable (not connection-refused-then-instantly-up).
	var httpReady func(*mcpserver.MCPServer, *store.Store, *search.Engine, string)
	var httpErrc <-chan error
	if *httpAddr != "" {
		httpReady, httpErrc = startEarlyHTTP(*httpAddr)
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] /healthz live on %s — loading corpus (status=loading)…\n", *httpAddr)
	}

	// Resolve the DB autonomously: an explicit/local path wins (dev); otherwise
	// serve from the per-user cache, pulling it from the rolling 'latest' release
	// when absent and refreshing it (best-effort, sha256-gated) when stale. A
	// downloaded binary thus boots and provisions itself with no manual step.
	effDB, rerr := resolveDB(*dbPath)
	switch {
	case rerr != nil:
		var err error
		if effDB, err = ensureDB(ctx, true); err != nil {
			return err
		}
	case effDB == cachedDBPath() && !*noUpdate:
		if up, e := ensureDB(ctx, true); e == nil {
			effDB = up
		}
	}

	// Serve is a pure reader: open read-only so there is no WAL and the
	// unsupported "custom HNSW index + WAL replay" corruption path can't occur
	// (axis #6 §4). --writable is an escape hatch for ad-hoc maintenance.
	open := store.OpenReadOnly
	if *writable {
		open = store.Open
	}
	st, err := open(effDB)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	servedDBPath = effDB

	// Best-effort: load the persisted BM25 index (built at ingest). We LOAD,
	// never rebuild — rebuilding on a 700k-clause corpus would stall startup.
	if err := st.LoadFTS(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] FTS unavailable, lexical search uses LIKE: %v\n", err)
	}
	// LOAD (never build) the frozen HNSW index; on a missing/stale index this
	// degrades to an exact full-scan — correct, just slower (axis #6 §6).
	if err := st.LoadVSS(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] HNSW unavailable, vector search uses exact scan: %v\n", err)
	}
	// Best-effort: mark the sparse (BGE-M3 learned-lexical) arm available iff the
	// DB carries sparse postings. Absent → the engine simply doesn't offer it.
	if err := st.LoadSparse(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] sparse arm unavailable: %v\n", err)
	}
	// Coherence guard: if this binary's embedder produces a DIFFERENT model than
	// the one the DB's vectors/HNSW were built with, cosine scores would be
	// silently wrong. Disable vector search (lexical still serves) and say why.
	// (A lexical/disabled client embedder can't emit a query vector, so there is
	// nothing to guard — the engine never calls the vector path.)
	// A MISMATCH IS FATAL BY DEFAULT.
	//
	// Degrading silently to lexical is how this shipped broken for months: the DB
	// was full of valid vectors, the guard fired on every correctly-built corpus
	// because of an unrelated identity bug, and the only trace was one stderr line
	// nobody reads in a stdio MCP server. The user got plausible lexical answers to
	// semantic questions and had no way to know.
	//
	// Refusing to start is the honest behaviour: the operator asked for a vector
	// server and cannot have one. `--allow-lexical-fallback` (or
	// MCP3GPP_ALLOW_LEXICAL_FALLBACK) accepts the degraded mode explicitly, which is
	// the difference between a decision and an accident.
	if st.VSSAvailable() {
		if emb := embed.New(); emb.Enabled() {
			if dbModel := st.GetMeta(ctx, "embedding_model"); dbModel != emb.ModelID() {
				if !*allowLexicalFallback {
					return fmt.Errorf(
						"embedding model mismatch: the corpus was vectorised with %q but this binary embeds queries as %q.\n"+
							"  Cosine scores between the two are meaningless, so vector search cannot be served.\n"+
							"  Rebuild the corpus with this binary, fetch a matching snapshot, or pass\n"+
							"  --allow-lexical-fallback to serve lexical-only on purpose.",
						dbModel, emb.ModelID())
				}
				st.DisableVSS()
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] semantic disabled by --allow-lexical-fallback: DB embedding_model=%q, client=%q (mismatch)\n", dbModel, emb.ModelID())
			}
		}
	}
	// Loud guard: a DB that carries vectors but ships WITHOUT a frozen HNSW index —
	// or without an FTS index — means a STALE/unindexed data layer is being served
	// (the mcp image inherited an old 3gpp-data digest). The symptom is catastrophic
	// (LIKE full-scan p99, exact-scan recall), so scream it at startup with the baked
	// provenance rather than letting it hide behind a one-line "FTS unavailable".
	warnIfDegradedDataLayer(ctx, st)
	warnIfSparseMissing(ctx, st)

	// Option B: if a vec-manifest lists per-series sub-bases, ATTACH them and route
	// the vector arm through the scatter-gather. Best-effort: a bad manifest just
	// degrades to single-DB / lexical (degrade, never block).
	// Optionally pull the Option-B sub-bases from GHCR into the cache, then serve
	// them via the manifest path below (best-effort: a failed pull degrades).
	if *vecGHCR != "" {
		if dir, err := bootstrap.CacheDir(); err == nil {
			if mp, err := bootstrap.FetchVecBases(ctx, *vecGHCR, filepath.Join(dir, "vec")); err != nil {
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] vector sub-bases pull failed (%v) — single-DB/lexical\n", err)
			} else {
				*vecManifest = mp
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] pulled vector sub-bases from ghcr.io/%s/3gpp-vec\n", *vecGHCR)
			}
		}
	}

	var vecShards []string
	if *vecManifest != "" {
		emb := embed.New() // same selection as the engine
		switch paths, err := loadVecManifest(*vecManifest); {
		case err != nil:
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] vec-manifest ignored (%v) — single-DB vectors\n", err)
		case !emb.Enabled():
			// A lexical/disabled client can't embed a query, so sub-bases would
			// never be queried; don't attach them.
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] vec-manifest ignored: client embedder disabled (lexical)\n")
		default:
			if aliases, err := st.AttachShards(ctx, paths); err != nil {
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] sub-bases not attached (%v) — single-DB vectors\n", err)
			} else if ok, why := st.ShardsCoherent(ctx, aliases, emb.ModelID()); !ok {
				// Coherence guard for Option B: a sub-base built with a different
				// model than the client would yield silently-wrong cosine scores.
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] Option-B sub-bases ignored (single-DB/lexical vectors): model mismatch (%s)\n", why)
			} else {
				vecShards = aliases
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] Option B: %d vector sub-bases attached\n", len(aliases))
			}
		}
	}

	// Optional SPLIT ETSI corpus: a SECOND read-only store over etsi.duckdb, served
	// ALONGSIDE the 3GPP DB (never merged). The handlers route "ETSI …" ids to it and
	// union list_specs. Best-effort: a missing/bad etsi.duckdb degrades to 3GPP-only.
	// Default to the ETSI corpus sitting beside the resolved 3GPP one. That is
	// exactly where `bootstrap --etsi` writes it, and without this default the
	// 23 MB it downloads were ignored at serve time unless the user happened to
	// pass --etsi-db as well — a flag they had no reason to think was required.
	etsiPath, etsiWhy := *etsiDB, "--etsi-db"
	if etsiPath == "" {
		if beside := filepath.Join(filepath.Dir(effDB), "etsi.duckdb"); fileExists(beside) {
			etsiPath, etsiWhy = beside, "found beside the corpus"
		}
	}
	var etsiSt *store.Store
	if etsiPath != "" {
		if es, eerr := store.OpenReadOnly(etsiPath); eerr != nil {
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] ETSI corpus %s (%s) unavailable, serving 3GPP only: %v\n", etsiPath, etsiWhy, eerr)
		} else {
			_ = es.LoadFTS(ctx)
			_ = es.LoadVSS(ctx)
			_ = es.LoadSparse(ctx)
			etsiSt = es
			defer func() { _ = etsiSt.Close() }()
			fmt.Fprintf(os.Stderr, "[3gpp-mcp] ETSI corpus attached (split, not merged): %s\n", etsiPath)
		}
	}

	// Pass the ETSI store as a Reader. Build the interface explicitly so a nil
	// *store.Store does NOT become a non-nil typed-nil interface (Go gotcha) —
	// mcp's `etsi != nil` federation check must see a genuine nil when absent.
	var etsiReader store.Reader
	if etsiSt != nil {
		etsiReader = etsiSt
	}
	srv, eng := mcp.New(st, Version, *release, vecShards, etsiReader)
	scope := *release
	if scope == "" {
		scope = "latest"
	}
	logServeConfig(eng)
	// stdio (default) is byte-identical to the historical behaviour. --http mounts
	// the SAME *MCPServer on Streamable HTTP plus a copy-paste landing page; the
	// engine is transport-agnostic, so nothing about retrieval changes.
	if *httpAddr != "" {
		httpReady(srv, st, eng, scope) // flip /healthz → 200 ready and wire the live MCP + /spec + dashboard routes
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] READY: MCP over Streamable HTTP on %s (endpoint /mcp, landing /, db=%s, fts=%v, hnsw=%v, baseline=%s, status=ready)\n",
			*httpAddr, effDB, st.FTSAvailable(), st.VSSAvailable(), scope)
		return <-httpErrc
	}
	fmt.Fprintf(os.Stderr, "[3gpp-mcp] serving MCP on stdio (db=%s, fts=%v, hnsw=%v, baseline=%s)\n",
		effDB, st.FTSAvailable(), st.VSSAvailable(), scope)
	return mcpserver.ServeStdio(srv)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s <serve|bootstrap|skill|prefetch-extensions|version>\n", os.Args[0])
}

// logServeConfig prints ONE line summarising the retrieval/runtime knobs that
// matter on a CPU-only box, so the deployed configuration is visible in the logs
// (cores + GOMAXPROCS, ORT thread overrides, rerank window + always-on, query
// embed cache). Reads the same env the engine/embedder read at construction.
func logServeConfig(eng *search.Engine) {
	st := eng.State()
	fmt.Fprintf(os.Stderr,
		"[3gpp-mcp] config: cpus=%d gomaxprocs=%d ort_intra=%s ort_inter=%s rerank_window=%s rerank_all=%v search_budget=%s query_cache=%s embedder=%v reranker=%v\n",
		runtime.NumCPU(), runtime.GOMAXPROCS(0),
		envOrDash("ORT_INTRA_OP_THREADS"), envOrDash("ORT_INTER_OP_THREADS"),
		envOrElse("RERANK_WINDOW", "12(default)"), st.RerankOn,
		envOrElse("SEARCH_BUDGET", "20s(default)"),
		envOrElse("EMBED_QUERY_CACHE", "512(default)"), st.EmbedderEnabled, st.RerankerEnabled)
}

func envOrDash(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return "auto"
}

func envOrElse(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// warnIfDegradedDataLayer emits ONE conspicuous startup line when the served DB is
// indexed-incomplete: no FTS index (→ LIKE full-scan, catastrophic p99) or vectors
// present without a frozen HNSW (→ exact-scan, gutted recall). That state almost
// always means the mcp image inherited a STALE 3gpp-data layer (an indexed image
// can exist on the registry while an old one is served). We print the baked
// provenance so the operator can compare digests and rebuild/redeploy. Best-effort,
// never blocks serving (degrade, never block — CLAUDE.md §1).
func warnIfDegradedDataLayer(ctx context.Context, st *store.Store) {
	hasVectors := st.GetMeta(ctx, "embedding_model") != ""
	hnswState := st.GetMeta(ctx, "hnsw_state")
	ftsOK := st.FTSAvailable()
	hnswDegraded := hasVectors && hnswState != "frozen"
	if ftsOK && !hnswDegraded {
		return // fully indexed — nothing to warn about
	}
	created := os.Getenv("MCP3GPP_DATA_CREATED")
	corpus := os.Getenv("MCP3GPP_SOURCE_CORPUS")
	fmt.Fprintf(os.Stderr,
		"[3gpp-mcp] ⚠️  DEGRADED DATA LAYER — fts_index=%v hnsw_state=%q (vectors=%v). "+
			"This served DB lacks frozen indexes: BM25 falls back to LIKE full-scan (catastrophic p99) "+
			"and the vector arm to bounded exact-scan (reduced recall). Almost certainly a STALE inherited "+
			"3gpp-data layer. Provenance: data_created=%q source_corpus=%q db=%s. "+
			"Fix: rebuild mcp:full FROM an indexed 3gpp-data digest (validate --require-fts --require-hnsw) and restart.\n",
		ftsOK, hnswState, hasVectors, created, corpus, servedDBPath)
}

// warnIfSparseMissing makes the sparse gap VISIBLE at startup. When this build is
// sparse-capable (the active model declares a sparse head) but the served DB has no
// sparse postings — or carries a sparse layer built by a DIFFERENT sparse model —
// the sparse arm silently never serves. The fix is an ADDITIVE pass that never
// touches the (already-computed) dense vectors: `embed --sparse-only` on the fused
// DB, then re-bake. We scream it once with the exact command rather than letting it
// hide behind the dashboard's "Sparse: absent" pill. Best-effort; never blocks.
func warnIfSparseMissing(ctx context.Context, st *store.Store) {
	if !embed.SparseCapable() {
		return // dense-only build: nothing to expect
	}
	want := embed.SparseModelID()
	got := st.GetMeta(ctx, "sparse_model")
	switch {
	case !st.SparseAvailable():
		fmt.Fprintf(os.Stderr,
			"[3gpp-mcp] ⚠️  SPARSE MISSING — this build is sparse-capable (model %q) but the served DB has NO "+
				"sparse postings (clause_sparse empty). The learned-lexical arm will not serve. Fix (additive, does "+
				"NOT touch dense vectors): `embed --db <fused.duckdb> --sparse-only` then re-bake.\n", want)
	case want != "" && got != want:
		fmt.Fprintf(os.Stderr,
			"[3gpp-mcp] ⚠️  SPARSE STALE — DB sparse_model=%q but this build expects %q. Re-run "+
				"`embed --db <fused.duckdb> --sparse-only` (additive) and re-bake to refresh.\n", got, want)
	}
}

// prefetchExtensions installs+loads the serve-side DuckDB extensions (fts, vss)
// and prints where they landed, as build-log evidence the image is offline-ready.
func prefetchExtensions() error {
	ctx := context.Background()
	if err := store.PrefetchExtensions(ctx); err != nil {
		return err
	}
	paths, err := store.InstalledExtensionPaths(ctx)
	if err != nil {
		return err
	}
	for _, ext := range []string{"fts", "vss"} {
		p, ok := paths[ext]
		if !ok {
			return fmt.Errorf("%s reported not installed after prefetch", ext)
		}
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] extension %s installed at %s\n", ext, p)
	}
	return nil
}
