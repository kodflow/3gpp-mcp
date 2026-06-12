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

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/mcp"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

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
		//   docker run --rm <image> skill > ~/.claude/commands/3gpp.md
		fmt.Print(skill3gpp)
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
	release := fs.String("release", "", "baseline release every answer is scoped to (e.g. Rel-17); empty = latest")
	writable := fs.Bool("writable", false, "open writable (default: read-only — the corruption-safe serve posture)")
	noUpdate := fs.Bool("no-update", os.Getenv("MCP3GPP_NO_UPDATE") != "", "don't pull/refresh the DB from the rolling 'latest' release at startup")
	vecManifest := fs.String("vec-manifest", "", "Option B: JSON listing per-series vectorized sub-bases to ATTACH for scatter-gather vector search (empty = single-DB vectors)")
	vecGHCR := fs.String("vec-ghcr", "", "Option B: pull vector sub-bases from ghcr.io/<owner>/3gpp-vec:latest into the cache and serve them (empty = off)")
	httpAddr := fs.String("http", "", "serve MCP over Streamable HTTP on this addr (e.g. 127.0.0.1:8765) + a landing page at /; empty = stdio (the default, unchanged). A non-loopback bind exposes the corpus — gate it.")
	_ = fs.Parse(args)

	// Let the (onnx) embedder/reranker transparently use cache-bootstrapped
	// models when the user hasn't exported the paths. No-op on the lexical build.
	pointModelsAtCache()

	ctx := context.Background()

	// HTTP mode: bring /healthz up BEFORE the (possibly minutes-long) DB + vector
	// bootstrap, so a puller sees 503 "loading" during startup and 200 "ready" only
	// once the corpus is actually queryable (not connection-refused-then-instantly-up).
	var httpReady func(*mcpserver.MCPServer, *store.Store, search.Caps, string)
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
	// Coherence guard: if this binary's embedder produces a DIFFERENT model than
	// the one the DB's vectors/HNSW were built with, cosine scores would be
	// silently wrong. Disable vector search (lexical still serves) and say why.
	// (A lexical/disabled client embedder can't emit a query vector, so there is
	// nothing to guard — the engine never calls the vector path.)
	if st.VSSAvailable() {
		if emb := embed.New(); emb.Enabled() {
			if dbModel := st.GetMeta(ctx, "embedding_model"); dbModel != emb.ModelID() {
				st.DisableVSS()
				fmt.Fprintf(os.Stderr, "[3gpp-mcp] semantic disabled: DB embedding_model=%q, client=%q (mismatch)\n", dbModel, emb.ModelID())
			}
		}
	}

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

	srv, caps := mcp.New(st, Version, *release, vecShards)
	scope := *release
	if scope == "" {
		scope = "latest"
	}
	// stdio (default) is byte-identical to the historical behaviour. --http mounts
	// the SAME *MCPServer on Streamable HTTP plus a copy-paste landing page; the
	// engine is transport-agnostic, so nothing about retrieval changes.
	if *httpAddr != "" {
		httpReady(srv, st, caps, scope) // flip /healthz → 200 ready and wire the live MCP + /spec + dashboard routes
		fmt.Fprintf(os.Stderr, "[3gpp-mcp] READY: MCP over Streamable HTTP on %s (endpoint /mcp, landing /, db=%s, fts=%v, hnsw=%v, baseline=%s, status=ready)\n",
			*httpAddr, effDB, st.FTSAvailable(), st.VSSAvailable(), scope)
		return <-httpErrc
	}
	fmt.Fprintf(os.Stderr, "[3gpp-mcp] serving MCP on stdio (db=%s, fts=%v, hnsw=%v, baseline=%s)\n",
		effDB, st.FTSAvailable(), st.VSSAvailable(), scope)
	return mcpserver.ServeStdio(srv)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s <serve|bootstrap|skill|version>\n", os.Args[0])
}
