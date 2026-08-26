// Command migrate-paragraphs converts a corpus to the content-addressed storage
// of ADR 0004: each distinct paragraph stored once, each distinct (heading,
// paragraph-sequence) body stored once, and one occurrence row per real
// (spec, release, version, clause).
//
//	migrate-paragraphs --db data/3gpp.duckdb            # build + verify, additive
//	migrate-paragraphs --db data/3gpp.duckdb --verify    # verify only, no writes
//	migrate-paragraphs --db data/3gpp.duckdb --drop-clauses
//
// It is ADDITIVE: the new tables are built alongside `clauses`, which is what
// makes the read side migratable one call at a time instead of in one jump.
// --drop-clauses is the separate, explicit step that actually reclaims the
// space, and it REFUSES to run unless verification passes first.
//
// Verification is not a formality. Two properties have to hold or the corpus is
// silently lossy:
//
//   - splitting a body on "\n\n" and re-joining must reproduce it byte-for-byte,
//     which requires keeping the EMPTY parts a run of blank lines produces;
//   - every occurrence must resolve back to exactly the text it had.
//
// Both are asserted over the whole corpus, not sampled.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"
)

const sep = "chr(10)||chr(10)"

func main() {
	db := flag.String("db", "data/3gpp.duckdb", "corpus to migrate (in place, additive)")
	verifyOnly := flag.Bool("verify", false, "check an already-migrated corpus and exit")
	dropClauses := flag.Bool("drop-clauses", false, "after a passing verification, drop the now-redundant `clauses` table")
	flag.Parse()

	h, err := sql.Open("duckdb", *db)
	if err != nil {
		die("open: %v", err)
	}
	defer func() { _ = h.Close() }()

	if !*verifyOnly {
		if err := build(h); err != nil {
			die("build: %v", err)
		}
	}
	if err := verify(h); err != nil {
		die("VERIFICATION FAILED — the corpus is NOT safe to shrink: %v", err)
	}
	if *dropClauses {
		if err := drop(h); err != nil {
			die("drop: %v", err)
		}
	}
	report(h)
}

func build(h *sql.DB) error {
	steps := []struct{ label, sql string }{
		// Staging carries the body text: it is the join key for the occurrences
		// and the reference the verification compares against. It does NOT
		// survive — reconstructing a body from its paragraphs costs 0.035 ms
		// once seq_body exists, so materialising 1.53 GB of it would be paying
		// storage to avoid a cost that is not there.
		{"staging", `CREATE OR REPLACE TABLE _mig_bodies AS
			SELECT row_number() OVER ()::INTEGER AS body_id, heading, body,
			       arg_max(emb, has_emb) AS embedding, any_value(ehash) AS embedding_hash
			FROM (
				SELECT heading, text AS body, embedding AS emb,
				       (embedding IS NOT NULL)::INTEGER AS has_emb, embedding_hash AS ehash
				FROM clauses
			) GROUP BY heading, body`},

		{"paragraphs", `CREATE OR REPLACE TABLE paragraphs AS
			SELECT row_number() OVER ()::INTEGER AS para_id, part
			FROM (SELECT DISTINCT unnest(string_split(body, ` + sep + `)) AS part FROM _mig_bodies)`},

		{"body_seq", `CREATE OR REPLACE TABLE body_seq AS
			WITH x AS (
				SELECT body_id,
				       unnest(list_transform(string_split(body, ` + sep + `), (s, i) -> {'o': i, 'p': s})) AS e
				FROM _mig_bodies
			)
			SELECT x.body_id, x.e['o']::SMALLINT AS ord, p.para_id
			FROM x JOIN paragraphs p ON p.part = x.e['p']`},

		{"clause_occ", `CREATE OR REPLACE TABLE clause_occ AS
			SELECT c.chunk_id, c.spec_id, c.release, c.version, c.clause_path, c.is_normative, b.body_id
			FROM clauses c JOIN _mig_bodies b
			  ON b.heading IS NOT DISTINCT FROM c.heading AND b.body = c.text`},

		{"bodies", `CREATE OR REPLACE TABLE bodies AS
			SELECT body_id, heading, embedding, embedding_hash FROM _mig_bodies`},

		{"drop staging", `DROP TABLE _mig_bodies`},

		// Declared in schema.sql, and not optional: without seq_body the
		// reconstruction join is a full scan of 8.4 M rows and a thousand
		// bodies take 5.8 s instead of 35 ms.
		{"indexes", `CREATE INDEX IF NOT EXISTS seq_body ON body_seq (body_id);
			CREATE INDEX IF NOT EXISTS seq_para ON body_seq (para_id);
			CREATE INDEX IF NOT EXISTS occ_body ON clause_occ (body_id);
			CREATE INDEX IF NOT EXISTS occ_spec ON clause_occ (spec_id);
			CREATE INDEX IF NOT EXISTS occ_path ON clause_occ (spec_id, clause_path)`},
	}
	for _, s := range steps {
		start := time.Now()
		if _, err := h.Exec(s.sql); err != nil {
			return fmt.Errorf("%s: %w", s.label, err)
		}
		fmt.Fprintf(os.Stderr, "  %-12s in %s\n", s.label, time.Since(start).Round(time.Second))
	}
	return nil
}

// verify asserts, over the WHOLE corpus, that every occurrence still resolves to
// exactly the text it had. Comparing the rebuild against `clauses` rather than
// against a copy kept inside the new tables is the point: a check that reads its
// own output would pass on a corpus that lost the original.
//
// The join is on chunk_id and nothing else. (spec_id, release, version,
// clause_path) looks like a key and is not one: 2 752 688 rows share 2 579 376
// of them, and TS 51.010-1 v7.12.0 alone carries 16 509 rows with an empty
// clause_path. Joining on it compared 550 568 438 pairs and declared the corpus
// corrupt when nothing was wrong with it.
func verify(h *sql.DB) error {
	var occ, exact int64
	err := h.QueryRow(`
		WITH rebuilt AS (
			SELECT s.body_id, string_agg(p.part, `+sep+` ORDER BY s.ord) AS body
			FROM body_seq s JOIN paragraphs p USING (para_id) GROUP BY s.body_id
		)
		SELECT count(*),
		       count(*) FILTER (WHERE c.text IS NOT DISTINCT FROM r.body
		                          AND c.heading IS NOT DISTINCT FROM b.heading)
		FROM clauses c
		JOIN clause_occ o ON o.chunk_id = c.chunk_id
		JOIN bodies b USING (body_id)
		JOIN rebuilt r USING (body_id)`).Scan(&occ, &exact)
	if err != nil {
		return fmt.Errorf("reconstruction query: %w", err)
	}
	if occ == 0 {
		return fmt.Errorf("no occurrences matched `clauses` — nothing was verified")
	}
	if exact != occ {
		return fmt.Errorf("%d of %d occurrences do not rebuild byte-for-byte", occ-exact, occ)
	}
	var clauses int64
	if err := h.QueryRow(`SELECT count(*) FROM clauses`).Scan(&clauses); err != nil {
		return fmt.Errorf("counting clauses: %w", err)
	}
	if occ != clauses {
		return fmt.Errorf("%d clauses but only %d verified occurrences — %d were lost", clauses, occ, clauses-occ)
	}
	fmt.Fprintf(os.Stderr, "  verified        %d/%d occurrences rebuild byte-for-byte\n", exact, clauses)
	return nil
}

func drop(h *sql.DB) error {
	fmt.Fprintln(os.Stderr, "  dropping `clauses` (verification passed)")
	if _, err := h.Exec(`DROP TABLE IF EXISTS clauses`); err != nil {
		return err
	}
	_, err := h.Exec(`CHECKPOINT`)
	return err
}

func report(h *sql.DB) {
	var para, bod, seq, occ int64
	_ = h.QueryRow(`SELECT (SELECT count(*) FROM paragraphs), (SELECT count(*) FROM bodies),
	                       (SELECT count(*) FROM body_seq), (SELECT count(*) FROM clause_occ)`).
		Scan(&para, &bod, &seq, &occ)
	fmt.Printf("paragraphs=%d bodies=%d body_seq=%d clause_occ=%d\n", para, bod, seq, occ)
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "migrate-paragraphs: "+f+"\n", a...)
	os.Exit(1)
}
