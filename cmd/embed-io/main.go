// Command embed-io is the Go<->Rust bridge for the decoupled embedder
// (rust/embedder). The expensive embedding COMPUTE runs in Rust (fastembed /
// ONNX Runtime); all DuckDB I/O stays here in Go, which owns the exact DuckDB
// version that wrote the corpus. This removes any DuckDB storage-format
// compatibility risk between the two runtimes and keeps the Rust binary tiny.
//
// Two modes, one JSONL contract shared with rust/embedder:
//
//	embed-io --db D --export-worklist work.jsonl [scan flags]
//	  -> open D read-only, stream the SAME work-list cmd/embed would embed
//	     (floor/series/releases/limit/resume/order), write one line per clause:
//	     {"chunk_id":U64,"heading":S,"text":S}
//
//	embed-io --db D --import-vectors vecs.jsonl --embed-identity <id> [--build-hnsw]
//	  -> read {"chunk_id":U64,"hash":S,"vec":[f32;1024]} lines produced by the Rust
//	     embedder, write them into clauses.embedding (+ embedding_hash) in batches,
//	     stamp embedding_model meta, and optionally (re)build+freeze the HNSW index.
//
// The Rust side writes the SAME embedding_hash as Go's embed.ClauseHash (it is given
// the identity printed by cmd/embedid), so a later Go re-embed gate is a no-op.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// denseDim is the BGE-M3 dense dimensionality (clauses.embedding FLOAT[1024]).
const denseDim = 1024

func main() {
	var (
		dbPath   = flag.String("db", "data/3gpp.duckdb", "DuckDB snapshot")
		exportWL = flag.String("export-worklist", "", "write the embedding work-list as JSONL to this path, then exit")
		importV  = flag.String("import-vectors", "", "read a JSONL vector file (from rust/embedder) and write it into the DB, then exit")
		embedID  = flag.String("embed-identity", "", "the EmbedIdentity stamped into embedding_model meta on import (use `go run ./cmd/embedid`)")
		buildHN  = flag.Bool("build-hnsw", false, "after import, (re)build and freeze the HNSW index")
		// work-list selection (mirrors cmd/embed so export parity is exact)
		floor   = flag.String("embed-floor", "", "export ONLY clauses at/above this release (e.g. Rel-19); empty = all")
		series  = flag.String("series", "", "export ONLY this 2-digit series (e.g. 23); empty = all")
		embRels = flag.String("embed-releases", "", "export ONLY clauses in this comma-separated exact release set; empty = all")
		limit   = flag.Int("limit", 0, "cap the work-list to N clauses (0 = no cap)")
		order   = flag.String("order", "recent", "work-list order: recent (newest first) | chunk")
		resume  = flag.Bool("resume", true, "export ONLY never-embedded clauses (embedding IS NULL)")
		batch   = flag.Int("batch", 512, "rows per write transaction on import")
	)
	flag.Parse()

	switch {
	case *exportWL != "":
		if err := runExport(*dbPath, *exportWL, scanOpts{floor: *floor, series: *series, releases: splitCSV(*embRels), limit: *limit, order: *order, resume: *resume}); err != nil {
			fmt.Fprintf(os.Stderr, "export-worklist: %v\n", err)
			os.Exit(1)
		}
	case *importV != "":
		if err := runImport(*dbPath, *importV, *embedID, *batch, *buildHN); err != nil {
			fmt.Fprintf(os.Stderr, "import-vectors: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "embed-io: pass --export-worklist <file> or --import-vectors <file>")
		os.Exit(2)
	}
}

type scanOpts struct {
	floor, series, order string
	releases             []string
	limit                int
	resume               bool
}

// wlRecord is one work-list line (Rust input).
type wlRecord struct {
	ChunkID uint64 `json:"chunk_id"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

// vecRecord is one vector line (Rust output).
type vecRecord struct {
	ChunkID uint64    `json:"chunk_id"`
	Hash    string    `json:"hash"`
	Vec     []float32 `json:"vec"`
}

// runExport streams the work-list (read-only) to JSONL, using the SAME EmbedScan as
// cmd/embed so the Rust embedder sees exactly the clauses Go would have embedded.
func runExport(dbPath, out string, o scanOpts) error {
	floorOrd := 0
	if o.floor != "" {
		if v, ok := model.ReleaseOrdinal(o.floor); ok {
			floorOrd = v
		} else {
			fmt.Fprintf(os.Stderr, "export-worklist: ignoring unparseable --embed-floor %q\n", o.floor)
		}
	}
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	rows, err := db.ClausesNeedingEmbedding(ctx, store.EmbedScan{
		FloorOrd:     floorOrd,
		SeriesPrefix: o.series,
		Releases:     o.releases,
		Limit:        o.limit,
		OldestFirst:  o.order == "chunk",
		ResumeOnly:   o.resume,
	})
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	defer func() { _ = rows.Close() }()

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()
	enc := json.NewEncoder(w)

	n := 0
	for rows.Next() {
		var (
			id            uint64
			heading, text string
			release       string
			storedHash    string
		)
		if err := rows.Scan(&id, &heading, &text, &release, &storedHash); err != nil {
			return err
		}
		if err := enc.Encode(wlRecord{ChunkID: id, Heading: heading, Text: text}); err != nil {
			return err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "export-worklist: wrote %d clause(s) to %s\n", n, out)
	return nil
}

// runImport reads the Rust vector JSONL and writes it into the DB in batched
// transactions, then stamps embedding_model meta and optionally rebuilds the HNSW.
func runImport(dbPath, in, embedID string, batch int, buildHNSW bool) error {
	db, err := store.Open(dbPath) // read-write; migrate() ensures embedding_hash exists
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// DuckDB refuses to UPDATE the embedding column while an HNSW index references
	// the table; drop it first (no-op if absent), rebuild at the end.
	if err := db.PrepareForEmbedUpdate(ctx); err != nil {
		return err
	}

	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26) // a 1024-float line is large; allow up to 64 MiB
	var (
		ids    []uint64
		vecs   [][]float32
		hashes []string
		total  int
	)
	flush := func() error {
		if len(ids) == 0 {
			return nil
		}
		if err := db.SetEmbeddingsBatch(ctx, ids, vecs, hashes); err != nil {
			return err
		}
		total += len(ids)
		ids, vecs, hashes = ids[:0], vecs[:0], hashes[:0]
		return nil
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r vecRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return fmt.Errorf("parse vector line: %w", err)
		}
		if len(r.Vec) != denseDim {
			return fmt.Errorf("clause %d: vec dim %d, want %d", r.ChunkID, len(r.Vec), denseDim)
		}
		ids = append(ids, r.ChunkID)
		vecs = append(vecs, r.Vec)
		hashes = append(hashes, r.Hash)
		if len(ids) >= batch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	if embedID != "" {
		if err := db.SetMeta("embedding_model", embedID); err != nil {
			return fmt.Errorf("stamp embedding_model: %w", err)
		}
	}
	// Gate the (full, O(N)) HNSW build on completeness. In the resumable Kaggle loop
	// --build-hnsw is passed on EVERY pass, but a PARTIAL pass would pay a full index
	// rebuild that the next pass — and cmd/merge's authoritative global rebuild —
	// just discard. Build + freeze only when no embeddable clause is left NULL (the
	// final pass), matching the kernel's complete=1 (null_after==0) condition. A
	// partial DB stays index-free and serve degrades to an exact scan (still correct).
	built := false
	if buildHNSW {
		n, err := db.CountNullEmbeddings(ctx)
		if err != nil {
			return fmt.Errorf("count null embeddings: %w", err)
		}
		if n > 0 {
			fmt.Fprintf(os.Stderr, "import-vectors: %d embeddable clause(s) still NULL — skipping HNSW build (not the final pass)\n", n)
		} else {
			if err := db.BuildAndFreezeHNSW(ctx, embedID); err != nil {
				return fmt.Errorf("build hnsw: %w", err)
			}
			built = true
		}
	}
	fmt.Fprintf(os.Stderr, "import-vectors: wrote %d vector(s) (hnsw=%v)\n", total, built)
	return nil
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if t := trim(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if t := trim(cur); t != "" {
		out = append(out, t)
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
