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
	"flag"
	"fmt"
	"os"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/mcp"
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
	case "version", "-v", "--version":
		fmt.Println(Version)
	default:
		usage()
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "data/3gpp.duckdb", "DuckDB snapshot path")
	release := fs.String("release", "", "baseline release every answer is scoped to (e.g. Rel-17); empty = latest")
	writable := fs.Bool("writable", false, "open writable (default: read-only — the corruption-safe serve posture)")
	noUpdate := fs.Bool("no-update", os.Getenv("MCP3GPP_NO_UPDATE") != "", "don't pull/refresh the DB from the rolling 'latest' release at startup")
	_ = fs.Parse(args)

	// Let the (onnx) embedder/reranker transparently use cache-bootstrapped
	// models when the user hasn't exported the paths. No-op on the lexical build.
	pointModelsAtCache()

	ctx := context.Background()

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

	srv := mcp.New(st, Version, *release)
	scope := *release
	if scope == "" {
		scope = "latest"
	}
	fmt.Fprintf(os.Stderr, "[3gpp-mcp] serving MCP on stdio (db=%s, fts=%v, hnsw=%v, baseline=%s)\n",
		effDB, st.FTSAvailable(), st.VSSAvailable(), scope)
	return mcpserver.ServeStdio(srv)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s <serve|bootstrap|version>\n", os.Args[0])
}
