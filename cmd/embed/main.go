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
	"io"
	"os"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		dbPath    = flag.String("db", "data/3gpp.duckdb", "existing DuckDB snapshot to embed in place")
		embFlr    = flag.String("embed-floor", "", "embed ONLY clauses at/above this release (e.g. Rel-19); empty = all. Lexical coverage is unaffected.")
		series    = flag.String("series", "", "embed ONLY this 2-digit series (e.g. 23); empty = all. For per-series Kaggle shards.")
		embRels   = flag.String("embed-releases", "", "embed ONLY clauses in this comma-separated SET of exact release labels (e.g. \"Rel-19,Rel-16,Rel-15\"); empty = all. For balanced per-RELEASE-lot Kaggle shards. AND-combined with --series/--embed-floor; selection-only (does NOT change EmbedIdentity).")
		limit     = flag.Int("limit", 0, "cap the work-list to N clauses (bounded session; resumes next run). 0 = no limit.")
		order     = flag.String("order", "recent", "work-list order: recent (newest release first) | chunk (chunk_id order)")
		resume    = flag.Bool("resume", false, "fast resume: embed ONLY never-embedded clauses (skip the Go hash re-check). Pair with --limit for bounded sessions.")
		ckptEvery = flag.Int("checkpoint-every", 0, "CHECKPOINT the DB every N embedded clauses for crash-resumable durability. 0 = only at end.")
		progEvery = flag.Int("progress-every", 256, "log a live progress line (count + cl/s) every N embedded clauses; 0 = silent until the end.")
		logFile   = flag.String("log-file", "", "also append progress + start/end markers to this file (tail -f friendly when embedding on a remote GPU)")
		countNull = flag.Bool("count-null-at-floor", false, "read-only: print null-at-floor (+series) and exit WITHOUT embedding (completeness oracle for the driver)")
		reqSem    = flag.Bool("require-semantic", false, "fail (exit 1) if the embedder is not enabled (also honours SEMANTIC_REQUIRED=1)")
		report    = flag.String("report", "text", "end-of-run summary: text | json")
		noHNSW    = flag.Bool("no-hnsw", false, "skip the HNSW build/freeze (e.g. when embedding a per-series shard that will be merged first)")
	)
	flag.Parse()

	// --count-null-at-floor is a read-only completeness probe: no embedder, no
	// writes (never drops the frozen HNSW). The driver calls it to skip series whose
	// sub-base is already complete.
	if *countNull {
		os.Exit(runCountNull(*dbPath, *embFlr, *series, *report))
	}

	releases := splitReleases(*embRels)

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
	rep, err := run(context.Background(), *dbPath, e, embedConfig{
		floor:           *embFlr,
		series:          *series,
		releases:        releases,
		limit:           *limit,
		order:           *order,
		resume:          *resume,
		checkpointEvery: *ckptEvery,
		progressEvery:   *progEvery,
		logFile:         *logFile,
		buildHNSW:       !*noHNSW,
	})
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

// embedConfig is the resolved cmd/embed invocation: which clauses to embed (floor,
// series, limit, order, resume) and the durability/finalisation knobs.
type embedConfig struct {
	floor           string   // --embed-floor (e.g. "Rel-19"); "" = all releases
	series          string   // --series 2-digit prefix; "" = all series
	releases        []string // --embed-releases exact-label set; nil/empty = all releases
	limit           int      // --limit work-list cap; 0 = no limit
	order           string   // --order: "recent" (default) | "chunk"
	resume          bool     // --resume: only never-embedded rows
	checkpointEvery int      // --checkpoint-every N; 0 = checkpoint only at end
	progressEvery   int      // --progress-every N; 0 = no live progress line
	logFile         string   // --log-file; "" = stderr only
	buildHNSW       bool     // !--no-hnsw
}

func run(ctx context.Context, dbPath string, e embed.Embedder, cfg embedConfig) (reportJSON, error) {
	rep := reportJSON{Model: e.ModelID()}

	db, err := store.Open(dbPath) // read-write; migrate() adds embedding_hash to an old DB.
	if err != nil {
		return rep, err
	}
	defer func() { _ = db.Close() }()

	floorOrd := 0
	if cfg.floor != "" {
		if o, ok := model.ReleaseOrdinal(cfg.floor); ok {
			floorOrd = o
		} else {
			fmt.Fprintf(os.Stderr, "embed: ignoring unparseable --embed-floor %q (embedding all releases)\n", cfg.floor)
		}
	}
	oldestFirst := cfg.order == "chunk"
	if cfg.order != "" && cfg.order != "chunk" && cfg.order != "recent" {
		fmt.Fprintf(os.Stderr, "embed: unknown --order %q (using recent-first)\n", cfg.order)
	}

	// Stream the work-list RECENT-RELEASE-FIRST, with floor/series/limit/resume
	// pushed into SQL so a 10M-clause DB never streams the whole table into a Go
	// slice. embed.Apply skips the ones whose hash is already current, so the actual
	// embedding cost is the delta only.
	rows, err := db.ClausesNeedingEmbedding(ctx, store.EmbedScan{
		FloorOrd:     floorOrd,
		SeriesPrefix: cfg.series,
		Releases:     cfg.releases,
		Limit:        cfg.limit,
		OldestFirst:  oldestFirst,
		ResumeOnly:   cfg.resume,
	})
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

	// Batched writes: one transaction per length-bucketed window (store.SetEmbeddingsBatch)
	// instead of one autocommit per clause — the write path that keeps a 10M embed flat.
	// --checkpoint-every additionally flushes the DB durably every N embeds, so a session
	// killed by the Kaggle 12h cap resumes from the last checkpoint, not the run start.
	// One wrapper does the batched write + optional durable checkpoint + a live
	// progress line (count + cl/s), so a long Kaggle embed shows forward motion in
	// the kernel log instead of going silent for hours. All knobs are independent.
	// Progress goes to stderr and, when --log-file is set, ALSO to that file so a
	// remote GPU run (Lightning/Kaggle) is observable with `tail -f` even when the
	// platform surfaces no live stdout. Markers bracket the run for log readers.
	progressW := io.Writer(os.Stderr)
	if cfg.logFile != "" {
		lf, err := os.OpenFile(cfg.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return rep, fmt.Errorf("open --log-file %s: %w", cfg.logFile, err)
		}
		defer func() { _ = lf.Close() }()
		progressW = io.MultiWriter(os.Stderr, lf)
		_, _ = fmt.Fprintf(progressW, "embed start: db=%s floor=%q series=%q candidates=%d model=%s\n",
			dbPath, cfg.floor, cfg.series, rep.Candidates, e.ModelID())
	}

	embStart := time.Now()
	done, sinceCkpt, sinceLog := 0, 0, 0
	total := rep.Candidates // work-list size (scanned); denominator for the progress %
	batchSet := func(ctx context.Context, ids []uint64, vecs [][]float32, hashes []string) error {
		if err := db.SetEmbeddingsBatch(ctx, ids, vecs, hashes); err != nil {
			return err
		}
		done += len(ids)
		if cfg.checkpointEvery > 0 {
			if sinceCkpt += len(ids); sinceCkpt >= cfg.checkpointEvery {
				sinceCkpt = 0
				if err := db.Checkpoint(ctx); err != nil {
					return err
				}
			}
		}
		if cfg.progressEvery > 0 {
			if sinceLog += len(ids); sinceLog >= cfg.progressEvery {
				sinceLog = 0
				el := time.Since(embStart).Seconds()
				rate := 0.0
				if el > 0 {
					rate = float64(done) / el
				}
				// done/total + % + a pseudo ETA for the remaining work-list, so a
				// remote log shows HOW FAR ALONG the run is, not just a running count.
				// total is the scanned work-list (rep.Candidates); with --resume that
				// is exactly the clauses left to embed, so done/total is the true ratio.
				pct, eta := 0.0, 0.0
				if total > 0 {
					pct = 100 * float64(done) / float64(total)
				}
				if rate > 0 && total > done {
					eta = float64(total-done) / rate
				}
				_, _ = fmt.Fprintf(progressW,
					"embed progress: %d/%d embedded (%.1f%%, %.1f cl/s, %.0fs elapsed, ETA %.0fs)\n",
					done, total, pct, rate, el, eta)
			}
		}
		return nil
	}

	embedded, err := embed.ApplyBatched(ctx, e, items, batchSet)
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
	// Stamp the embedder identity UNCONDITIONALLY — historically only
	// BuildAndFreezeHNSW wrote it, so every --no-hnsw artifact (the whole
	// Kaggle channel) shipped WITHOUT embedding_model: serve's ShardsCoherent
	// read an absent key and silently refused the sub-bases, and the image
	// fuse had no shard truth to copy. The meta states which model produced
	// the vectors; that is true with or without an index.
	if err := db.SetMeta("embedding_model", e.ModelID()); err != nil {
		return rep, err
	}

	if cfg.buildHNSW {
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
	// Floor-scoped completeness: a floored / series-scoped run leaves below-floor or
	// other-series clauses NULL on purpose, so the global count never hits 0. The CI
	// gate keys on NullAtFloor — at/above-floor (and in-series) clauses that should
	// have a vector but don't (a real failure). ⚠ With --limit the work-list is
	// intentionally truncated, so NullAtFloor will be > 0 mid-campaign; the hard
	// ==0 gate is only valid on an un-limited final run.
	if n, err := db.CountNullAtFloor(ctx, floorOrd, cfg.series); err == nil {
		rep.NullAtFloor = n
	}
	if cfg.logFile != "" {
		_, _ = fmt.Fprintf(progressW, "embed done: embedded=%d skipped=%d null_at_floor=%d hnsw=%v elapsed=%s\n",
			rep.Embedded, rep.Skipped, rep.NullAtFloor, rep.HNSW, time.Since(embStart).Round(time.Second))
	}
	return rep, nil
}

// runCountNull is the read-only completeness probe behind --count-null-at-floor:
// open the DB read-only (never touch the frozen HNSW) and report how many at/above-
// floor (+series) clauses still lack a vector. Returns a process exit code.
func runCountNull(dbPath, floor, series, report string) int {
	ctx := context.Background()
	floorOrd := 0
	if floor != "" {
		if o, ok := model.ReleaseOrdinal(floor); ok {
			floorOrd = o
		}
	}
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed: --count-null-at-floor: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	atFloor, err := db.CountNullAtFloor(ctx, floorOrd, series)
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed: count-null-at-floor: %v\n", err)
		return 1
	}
	global, _ := db.CountNullEmbeddings(ctx)
	if report == "json" {
		b, _ := json.MarshalIndent(map[string]any{
			"null_embeddings_at_floor": atFloor,
			"null_embeddings":          global,
			"floor":                    floor,
			"series":                   series,
		}, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("null_at_floor=%d null_after=%d floor=%q series=%q\n", atFloor, global, floor, series)
	}
	return 0
}

// splitReleases parses the comma-separated --embed-releases value into a trimmed,
// empty-dropped list of exact release labels. "" / whitespace yields nil (all
// releases), so the SQL predicate is skipped entirely. The values are matched
// verbatim against the clauses.release column (e.g. "Rel-19"), never re-derived
// from a recency ordinal — a lot is an explicit label set, not a range.
func splitReleases(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if r := strings.TrimSpace(part); r != "" {
			out = append(out, r)
		}
	}
	return out
}
