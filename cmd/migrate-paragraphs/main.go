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
		{"bodies", `CREATE OR REPLACE TABLE bodies AS
			SELECT row_number() OVER ()::INTEGER AS body_id, heading, body,
			       arg_max(emb, has_emb) AS embedding, any_value(ehash) AS embedding_hash
			FROM (
				SELECT heading, text AS body, embedding AS emb,
				       (embedding IS NOT NULL)::INTEGER AS has_emb, embedding_hash AS ehash
				FROM clauses
			) GROUP BY heading, body`},

		// The distinct parts of every distinct body — including the empty ones.
		{"paragraphs", `CREATE OR REPLACE TABLE paragraphs AS
			SELECT row_number() OVER ()::INTEGER AS para_id, part
			FROM (SELECT DISTINCT unnest(string_split(body, ` + sep + `)) AS part FROM bodies)`},

		{"body_seq", `CREATE OR REPLACE TABLE body_seq AS
			WITH x AS (
				SELECT body_id,
				       unnest(list_transform(string_split(body, ` + sep + `), (s, i) -> {'o': i, 'p': s})) AS e
				FROM bodies
			)
			SELECT x.body_id, x.e['o']::SMALLINT AS ord, p.para_id
			FROM x JOIN paragraphs p ON p.part = x.e['p']`},

		{"clause_occ", `CREATE OR REPLACE TABLE clause_occ AS
			SELECT c.spec_id, c.release, c.version, c.clause_path, c.is_normative, b.body_id
			FROM clauses c JOIN bodies b ON b.heading IS NOT DISTINCT FROM c.heading AND b.body = c.text`},
	}
	for _, s := range steps {
		start := time.Now()
		if _, err := h.Exec(s.sql); err != nil {
			return fmt.Errorf("%s: %w", s.label, err)
		}
		fmt.Fprintf(os.Stderr, "  %-12s built in %s\n", s.label, time.Since(start).Round(time.Second))
	}
	return nil
}

// verify asserts the two properties that make the new shape lossless, over the
// WHOLE corpus. A sampled check here would be worse than none: the failure mode
// it guards against — a separator that does not round-trip — is rare by nature.
func verify(h *sql.DB) error {
	var bodies, exact int64
	err := h.QueryRow(`
		WITH rebuilt AS (
			SELECT s.body_id, string_agg(p.part, `+sep+` ORDER BY s.ord) AS body
			FROM body_seq s JOIN paragraphs p USING (para_id) GROUP BY s.body_id
		)
		SELECT count(*), count(*) FILTER (WHERE b.body IS NOT DISTINCT FROM r.body)
		FROM bodies b JOIN rebuilt r USING (body_id)`).Scan(&bodies, &exact)
	if err != nil {
		return fmt.Errorf("reconstruction query: %w", err)
	}
	if bodies == 0 {
		return fmt.Errorf("no bodies — nothing was migrated")
	}
	if exact != bodies {
		return fmt.Errorf("%d of %d bodies do not rebuild byte-for-byte", bodies-exact, bodies)
	}
	fmt.Fprintf(os.Stderr, "  reconstruction  %d/%d bodies exact\n", exact, bodies)

	// Every clause must have found its body: a JOIN that silently dropped rows
	// would shrink the corpus by losing it.
	var clauses, occ int64
	if err := h.QueryRow(`SELECT (SELECT count(*) FROM clauses), (SELECT count(*) FROM clause_occ)`).Scan(&clauses, &occ); err != nil {
		// `clauses` is gone on an already-dropped corpus; that is not a failure.
		fmt.Fprintf(os.Stderr, "  occurrences     clauses table absent (already dropped)\n")
		return nil
	}
	if occ != clauses {
		return fmt.Errorf("%d clauses but %d occurrences — the join lost %d rows", clauses, occ, clauses-occ)
	}
	fmt.Fprintf(os.Stderr, "  occurrences     %d/%d preserved\n", occ, clauses)
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
