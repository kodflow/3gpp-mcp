// Command overlay writes the embedding vectors from one or more vector shards onto a
// FULL lexical base DB, keyed by chunk_id. It is the correct way to assemble a single
// serveable DB from the Kaggle lots: the lot outputs are CLAUSES-ONLY (the embed kernel
// slices only the clauses table), so they carry vectors but NOT the catalogue
// (specs/spec_versions/acronyms/changes/api/li). The lexical `latest` base carries the
// whole catalogue + the same clauses (by chunk_id) WITHOUT vectors. overlay UPDATEs the
// base's clauses.embedding / embedding_hash from each shard where the shard has a vector.
//
//	overlay --base lex.duckdb --vec lotA.duckdb --vec lotB.duckdb
//
// Result: a full DB (catalogue + clauses + the shards' vectors). FTS from the base is
// kept; HNSW is NOT built here (RAM-hungry — freeze it later). The base is modified
// in place, so pass a COPY if you want to keep the lexical-only original.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	base := flag.String("base", "", "full lexical base DB to overlay vectors onto (modified IN PLACE — pass a copy)")
	var vecs multiFlag
	flag.Var(&vecs, "vec", "a vector shard (clauses+embedding) to overlay; repeat for each lot")
	flag.Parse()
	if *base == "" || len(vecs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: overlay --base BASE.duckdb --vec SHARD1.duckdb [--vec SHARD2.duckdb ...]")
		os.Exit(2)
	}
	if err := run(context.Background(), *base, vecs); err != nil {
		fmt.Fprintln(os.Stderr, "overlay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, base string, vecs []string) error {
	if _, err := os.Stat(base); err != nil {
		return fmt.Errorf("base %s: %w", base, err)
	}
	db, err := store.Open(base) // writable
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sqldb := db.DB()

	var before int64
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE embedding IS NOT NULL`).Scan(&before)
	fmt.Fprintf(os.Stderr, "[overlay] base %s: %d clauses already vectorised\n", base, before)

	for i, v := range vecs {
		if _, err := os.Stat(v); err != nil {
			return fmt.Errorf("vec %s: %w", v, err)
		}
		alias := fmt.Sprintf("v%d", i)
		// ATTACH takes no bind params; v is an operator-provided path.
		if _, err := sqldb.ExecContext(ctx, "ATTACH '"+strings.ReplaceAll(v, "'", "''")+"' AS "+alias+" (READ_ONLY)"); err != nil {
			return fmt.Errorf("attach %s: %w", v, err)
		}
		// Overlay ONLY where the shard actually has a vector, matched by chunk_id.
		res, err := sqldb.ExecContext(ctx, fmt.Sprintf(
			`UPDATE clauses SET embedding = s.embedding, embedding_hash = s.embedding_hash
			 FROM %s.clauses AS s
			 WHERE clauses.chunk_id = s.chunk_id AND s.embedding IS NOT NULL`, alias))
		if err != nil {
			return fmt.Errorf("overlay %s: %w", v, err)
		}
		n, _ := res.RowsAffected()
		fmt.Fprintf(os.Stderr, "[overlay] %s: %d vectors written\n", v, n)
		if _, err := sqldb.ExecContext(ctx, "DETACH "+alias); err != nil {
			return fmt.Errorf("detach %s: %w", alias, err)
		}
	}

	if _, err := sqldb.ExecContext(ctx, `CHECKPOINT`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	// Stamp the pipeline version so a downstream consumer treats it consistently.
	if err := db.SetMeta("pipeline_version", "overlay"); err != nil {
		return err
	}
	var after int64
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE embedding IS NOT NULL`).Scan(&after)
	fmt.Fprintf(os.Stderr, "[overlay] done: %d clauses vectorised (+%d). HNSW NOT built (freeze later).\n", after, after-before)
	return nil
}
