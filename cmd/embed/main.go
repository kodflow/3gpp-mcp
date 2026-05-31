// Command embed is the decoupled, micro-granular vectorisation step. It runs
// AFTER cmd/ingest, on an EXISTING DuckDB snapshot — no corpus download, no
// LibreOffice convert, no re-parse. It fills the clauses.embedding column only
// where it must:
//
//   - a clause never embedded (embedding_hash IS NULL/empty), or
//   - a clause whose text or the embedding model changed (stored hash != current).
//
// Everything else keeps its existing vector. So a repeat run over an unchanged DB
// embeds ZERO clauses and exits in seconds; after a delta ingest it embeds only
// the handful of clauses that actually changed. This is what makes "embed in a
// second step" cheap on rebuilds (BGE-M3 on CPU is ~1 clause/s, so re-embedding
// the whole corpus every time is infeasible — see .claude/plans).
//
//	embed --db data/3gpp.duckdb [--embed-floor Rel-19] [--require-semantic] [--report json]
//
// The real BGE-M3 backend is behind `-tags onnx` (+ a fetched model); the default
// build / EMBEDDER=local use a deterministic hash embedder so the path is testable
// without the model. Lexical builds never run this command.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		dbPath = flag.String("db", "data/3gpp.duckdb", "existing DuckDB snapshot to embed in place")
		embFlr = flag.String("embed-floor", "", "embed ONLY clauses at/above this release (e.g. Rel-19); empty = all. Lexical coverage is unaffected.")
		reqSem = flag.Bool("require-semantic", false, "fail (exit 1) if the embedder is not enabled (also honours SEMANTIC_REQUIRED=1)")
		report = flag.String("report", "text", "end-of-run summary: text | json")
		noHNSW = flag.Bool("no-hnsw", false, "skip the HNSW build/freeze (e.g. when embedding a per-series shard that will be merged first)")
	)
	flag.Parse()

	e := embed.New()
	if (*reqSem || os.Getenv("SEMANTIC_REQUIRED") == "1") && !e.Enabled() {
		fmt.Fprintln(os.Stderr, "semantic required but the embedder is disabled "+
			"(need a -tags onnx build + model, or EMBEDDER=local) — refusing to no-op an embed run")
		os.Exit(1)
	}
	if !e.Enabled() {
		fmt.Fprintln(os.Stderr, "embed: embedder disabled (lexical build) — nothing to do")
		return
	}

	if _, err := os.Stat(*dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "embed: --db %s: %v\n", *dbPath, err)
		os.Exit(1)
	}

	start := time.Now()
	rep, err := run(context.Background(), *dbPath, e, *embFlr, !*noHNSW)
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed failed: %v\n", err)
		os.Exit(1)
	}
	rep.Version = Version
	rep.Elapsed = time.Since(start).Round(time.Millisecond).String()

	if *report == "json" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Printf("embed done in %s (model=%s)\n", rep.Elapsed, rep.Model)
	fmt.Printf("  db=%s\n  candidates=%d embedded=%d skipped=%d null_after=%d null_at_floor=%d hnsw=%v\n",
		*dbPath, rep.Candidates, rep.Embedded, rep.Skipped, rep.NullAfter, rep.NullAtFloor, rep.HNSW)
}

// reportJSON is the machine summary (used by CI gates + the text printer).
type reportJSON struct {
	Model       string `json:"model"`
	Candidates  int    `json:"candidates"`               // clauses examined (after floor)
	Embedded    int    `json:"embedded_clauses"`         // actually (re)embedded this run
	Skipped     int    `json:"skipped_clauses"`          // hash already current → reused
	NullAfter   int    `json:"null_embeddings"`          // clauses still without a vector (GLOBAL — incl. below-floor)
	NullAtFloor int    `json:"null_embeddings_at_floor"` // at/above-floor clauses still NULL — the CI completeness gate
	HNSW        bool   `json:"hnsw"`
	Version     string `json:"version"`
	Elapsed     string `json:"elapsed"`
}

func run(ctx context.Context, dbPath string, e embed.Embedder, embedFloor string, buildHNSW bool) (reportJSON, error) {
	rep := reportJSON{Model: e.ModelID()}

	db, err := store.Open(dbPath) // read-write; migrate() adds embedding_hash to an old DB.
	if err != nil {
		return rep, err
	}
	defer func() { _ = db.Close() }()

	floorOrd := 0
	if embedFloor != "" {
		if o, ok := model.ReleaseOrdinal(embedFloor); ok {
			floorOrd = o
		} else {
			fmt.Fprintf(os.Stderr, "embed: ignoring unparseable --embed-floor %q (embedding all releases)\n", embedFloor)
		}
	}

	// Stream every clause once; build the work-list in memory (chunk_id + text +
	// stored hash). embed.Apply skips the ones whose hash is already current, so
	// the actual embedding cost is the delta only.
	rows, err := db.ClausesNeedingEmbedding(ctx)
	if err != nil {
		return rep, fmt.Errorf("scan clauses: %w", err)
	}
	var items []embed.Item
	for rows.Next() {
		var (
			id            uint64
			heading, text string
			release       string
			storedHash    string
		)
		if err := rows.Scan(&id, &heading, &text, &release, &storedHash); err != nil {
			_ = rows.Close()
			return rep, err
		}
		if floorOrd > 0 {
			if o, ok := model.ReleaseOrdinal(release); !ok || o < floorOrd {
				continue // below the vector floor: lexical-only
			}
		}
		items = append(items, embed.Item{ChunkID: id, Heading: heading, Text: text, StoredHash: storedHash})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return rep, err
	}
	_ = rows.Close()
	rep.Candidates = len(items)

	// Drop any existing HNSW index first: DuckDB refuses to UPDATE the embedding
	// column while the index references the table. BuildAndFreezeHNSW recreates it
	// at the end. No-op on a DB that was never embedded.
	if err := db.PrepareForEmbedUpdate(ctx); err != nil {
		return rep, err
	}

	embedded, err := embed.Apply(ctx, e, items, db.SetEmbeddingWithHash)
	if err != nil {
		return rep, fmt.Errorf("embed: %w", err)
	}
	rep.Embedded = embedded
	rep.Skipped = rep.Candidates - embedded

	// Stamp the pipeline version for the (now semantic) DB so a delta merge treats
	// it consistently, mirroring the inline ingest path.
	if err := db.SetMeta("pipeline_version", model.PipelineVersion(e.ModelID())); err != nil {
		return rep, err
	}

	if buildHNSW {
		// BuildAndFreezeHNSW also stamps embedding_model/dim/count + hnsw_state.
		if err := db.BuildAndFreezeHNSW(ctx, e.ModelID()); err != nil {
			fmt.Fprintf(os.Stderr, "embed: HNSW build-then-freeze failed (vector search degrades to exact scan): %v\n", err)
		} else {
			rep.HNSW = true
		}
	}

	if n, err := db.CountNullEmbeddings(ctx); err == nil {
		rep.NullAfter = n
	}
	// Floor-scoped completeness: a floored run leaves below-floor clauses NULL on
	// purpose, so the global count never hits 0. The CI gate keys on NullAtFloor —
	// at/above-floor clauses that should have a vector but don't (a real failure).
	if byRel, err := db.NullEmbeddingsByRelease(ctx); err == nil {
		for rel, n := range byRel {
			if floorOrd > 0 {
				if o, ok := model.ReleaseOrdinal(rel); !ok || o < floorOrd {
					continue // below floor: NULL is expected, not a failure
				}
			}
			rep.NullAtFloor += n
		}
	}
	return rep, nil
}
