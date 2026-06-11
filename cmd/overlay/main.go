// Command overlay writes the embedding vectors from one or more vector shards onto a
// FULL lexical base DB, keyed by the clause's NATURAL IDENTITY
// (spec_id, release, clause_path, text) — the same proven match the Kaggle kernel's
// carry-over uses, and deliberately NOT chunk_id: a lexical republish renumbers
// chunk_ids for changed series, so in the window before the embed catch-up a stale
// sub-base overlaid by chunk_id could attach vectors to the WRONG clauses. With
// identity matching a stale sub-base simply does not match the changed clauses (they
// stay NULL → lexical until the catch-up re-embeds them) — self-correcting, never
// mis-keyed. The shards are CLAUSES-ONLY (the embed kernel slices only the clauses
// table), so they carry vectors but NOT the catalogue; the lexical base carries the
// whole catalogue + the same clauses WITHOUT vectors.
//
//	overlay --base lex.duckdb --vec s21.duckdb --vec s23.duckdb …
//
// Result: a full DB (catalogue + clauses + the shards' vectors), with the shards'
// embedding_model stamped into schema_meta (coherence-checked across shards) so the
// serve-time model guard sees the fused DB exactly like a published sub-base. FTS
// from the base is kept; HNSW is NOT built here (RAM-hungry — freeze it later). The
// base is modified in place, so pass a COPY if you want the lexical-only original.
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
	flag.Var(&vecs, "vec", "a vector shard (clauses+embedding) to overlay onto the base by chunk_id; repeat for each lot")
	catFrom := flag.String("catalogue-from", "", "copy the catalogue tables (specs/spec_versions/acronyms/changes/evolutions/api_*/li_*/releases/asn1_types) from this DB into the base — use when the base is a CLAUSES-ONLY lots-merge (the lots carry clauses+vectors but no catalogue). chunk_id-independent.")
	embedModel := flag.String("embedding-model", "", "canonical EmbedIdentity to stamp into the base meta (see cmd/embedid). Needed for shards that predate the unconditional embedding_model stamp (--no-hnsw kernel outputs carried none); when a shard DOES carry the meta it must agree with this value (fail-closed).")
	flag.Parse()
	if *base == "" || (len(vecs) == 0 && *catFrom == "") {
		fmt.Fprintln(os.Stderr, "usage: overlay --base BASE.duckdb [--vec SHARD.duckdb ...] [--catalogue-from LEX.duckdb] [--embedding-model ID]")
		os.Exit(2)
	}
	if err := run(context.Background(), *base, vecs, *catFrom, *embedModel); err != nil {
		fmt.Fprintln(os.Stderr, "overlay:", err)
		os.Exit(1)
	}
}

// catalogueTables are every table EXCEPT clauses — they describe specs/releases/terms
// and join to clauses by (spec_id, release, clause_path) at QUERY time, never by the
// synthetic chunk_id, so they can be copied wholesale regardless of chunk_id values.
var catalogueTables = []string{
	"specs", "spec_versions", "acronyms", "evolutions", "releases", "changes",
	"api_operations", "api_schemas", "li_events", "li_event_fields", "li_nf_clauses", "asn1_types",
}

func run(ctx context.Context, base string, vecs []string, catFrom, embedModelFlag string) error {
	if _, err := os.Stat(base); err != nil {
		return fmt.Errorf("base %s: %w", base, err)
	}
	db, err := store.Open(base) // writable
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sqldb := db.DB()

	// Copy the catalogue from a full lexical base into a clauses-only base. The base's
	// catalogue tables are empty (a lots-merge has only clauses); fill them from src.
	if catFrom != "" {
		if _, err := os.Stat(catFrom); err != nil {
			return fmt.Errorf("catalogue-from %s: %w", catFrom, err)
		}
		if _, err := sqldb.ExecContext(ctx, "ATTACH '"+strings.ReplaceAll(catFrom, "'", "''")+"' AS cat (READ_ONLY)"); err != nil {
			return fmt.Errorf("attach catalogue %s: %w", catFrom, err)
		}
		for _, t := range catalogueTables {
			res, err := sqldb.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s SELECT * FROM cat.%s", t, t))
			if err != nil {
				// A table may be absent in either side (older schema) — skip, don't abort.
				fmt.Fprintf(os.Stderr, "[overlay] catalogue %s: skipped (%v)\n", t, err)
				continue
			}
			n, _ := res.RowsAffected()
			fmt.Fprintf(os.Stderr, "[overlay] catalogue %s: +%d rows\n", t, n)
		}
		if _, err := sqldb.ExecContext(ctx, "DETACH cat"); err != nil {
			return fmt.Errorf("detach catalogue: %w", err)
		}
	}

	var before int64
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE embedding IS NOT NULL`).Scan(&before)
	fmt.Fprintf(os.Stderr, "[overlay] base %s: %d clauses already vectorised\n", base, before)

	// embedModel collects the shards' embedding_model meta, seeded by the explicit
	// --embedding-model flag (the only truth for shards that predate the
	// unconditional stamp). All sources feeding ONE fused DB must agree (mixing
	// models/precisions in one store is forbidden — the EmbedIdentity invariant);
	// the agreed value is stamped into the base so the serve-time coherence guard
	// sees the fused DB like a published sub-base.
	embedModel := embedModelFlag
	for i, v := range vecs {
		if _, err := os.Stat(v); err != nil {
			return fmt.Errorf("vec %s: %w", v, err)
		}
		alias := fmt.Sprintf("v%d", i)
		// ATTACH takes no bind params; v is an operator-provided path.
		if _, err := sqldb.ExecContext(ctx, "ATTACH '"+strings.ReplaceAll(v, "'", "''")+"' AS "+alias+" (READ_ONLY)"); err != nil {
			return fmt.Errorf("attach %s: %w", v, err)
		}
		var m string
		_ = sqldb.QueryRowContext(ctx,
			"SELECT value FROM "+alias+".schema_meta WHERE key = 'embedding_model'").Scan(&m)
		switch {
		case m == "":
			fmt.Fprintf(os.Stderr, "[overlay] %s: no embedding_model meta (older shard?)\n", v)
		case embedModel == "":
			embedModel = m
		case m != embedModel:
			return fmt.Errorf("shard %s embedding_model=%q != %q (from --embedding-model or earlier shards) — refusing to fuse mixed models into one DB", v, m, embedModel)
		}
		// Overlay ONLY where the shard actually has a vector, matched by the clause's
		// natural identity + exact text (NOT chunk_id — unstable across republishes).
		res, err := sqldb.ExecContext(ctx, fmt.Sprintf(
			`UPDATE clauses SET embedding = s.embedding, embedding_hash = s.embedding_hash
			 FROM %s.clauses AS s
			 WHERE clauses.spec_id = s.spec_id AND clauses.release = s.release
			   AND clauses.clause_path = s.clause_path AND clauses.text = s.text
			   AND s.embedding IS NOT NULL`, alias))
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
	if embedModel != "" {
		if err := db.SetMeta("embedding_model", embedModel); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[overlay] embedding_model stamped: %s\n", embedModel)
	}
	var after int64
	_ = sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE embedding IS NOT NULL`).Scan(&after)
	fmt.Fprintf(os.Stderr, "[overlay] done: %d clauses vectorised (+%d). HNSW NOT built (freeze later).\n", after, after-before)
	return nil
}
