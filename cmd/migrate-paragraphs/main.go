// Command migrate-paragraphs converts a corpus to the content-addressed storage
// of ADR 0004: each distinct paragraph stored once, each distinct (heading,
// paragraph-sequence) body stored once, and one occurrence row per real
// (spec, release, version, clause).
//
//	migrate-paragraphs --db data/3gpp.duckdb            # build + verify, additive
//	migrate-paragraphs --db data/3gpp.duckdb --verify    # verify only, no writes
//	migrate-paragraphs --db data/3gpp.duckdb --drop-clauses
//	migrate-paragraphs --db data/3gpp.duckdb --restore     # the inverse of --drop-clauses
//	migrate-paragraphs --db data/3gpp.duckdb --repair-view # recover an INTERRUPTED --restore
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
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

const sep = "chr(10)||chr(10)"

func main() {
	db := flag.String("db", "data/3gpp.duckdb", "corpus to migrate (in place, additive)")
	verifyOnly := flag.Bool("verify", false, "check an already-migrated corpus and exit")
	dropClauses := flag.Bool("drop-clauses", false, "after a passing verification, drop the now-redundant `clauses` table")
	restoreFlag := flag.Bool("restore", false, "put `clauses` back as a real table and remove the content-addressed tables (the inverse of --drop-clauses)")
	repairFlag := flag.Bool("repair-view", false, "recover an INTERRUPTED --restore: drop the empty `clauses` shell it left behind and put the view back")
	flag.Parse()

	h, err := sql.Open("duckdb", *db)
	if err != nil {
		die("open: %v", err)
	}
	defer func() { _ = h.Close() }()

	// A corpus carrying a frozen HNSW cannot be MODIFIED — nor even
	// CHECKPOINTed — unless the extension providing that index type is loaded:
	//
	//   FATAL Error: Failed to create checkpoint because of error: Cannot bind
	//   index 'bodies', unknown index type 'HNSW'. You need to load the
	//   extension that provides this index type before table 'bodies' can be
	//   modified.
	//
	// Invisible on a first pass, when no index exists yet, and fatal on every
	// one after — which is to say, precisely in the incremental case. This is
	// the same load Store::open_rw does, and for the same reason. Best-effort:
	// a build without VSS has no such index to bind.
	_, _ = h.Exec(`INSTALL vss`)
	_, _ = h.Exec(`LOAD vss`)
	_, _ = h.Exec(`SET hnsw_enable_experimental_persistence = true`)

	if *repairFlag {
		// Narrow by design, and the whole run: there is nothing to verify or report
		// against a corpus whose `clauses` is the empty shell of a killed restore.
		if err := repairView(h); err != nil {
			die("repair-view: %v", err)
		}
		return
	}
	if *restoreFlag {
		// Restore replaces the tables the other paths read, so it is the whole
		// run: verifying afterwards would have nothing left to verify against,
		// and report would count tables that no longer exist.
		if err := restore(h); err != nil {
			die("restore: %v", err)
		}
		return
	}
	if !*verifyOnly {
		// An already-converted corpus is a SUCCESS with no work, not a failure: the
		// pipeline passes --drop-clauses unconditionally and must be able to re-run
		// `paragraphs` on a corpus it already converted. See alreadyConverted.
		switch err := build(h); {
		case errors.Is(err, errAlreadyConverted):
			report(h)
			return
		case err != nil:
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
	if done, err := alreadyConverted(h); err != nil {
		return err
	} else if done {
		return errAlreadyConverted
	}
	if err := refuseToShrink(h); err != nil {
		return err
	}
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
	// A VIEW takes its place, with the same columns. Every caller that reads
	// metadata off `clauses` — validate's counts, anchorcheck's (spec, release,
	// version) sweep, split, the sparse join — keeps working untouched, because
	// DuckDB PRUNES the text column when it is not selected: measured on this
	// corpus, count(*) through the view is 0.62 s and count(DISTINCT release)
	// 0.44 s. The rebuild is only paid by a query that actually asks for text,
	// and those paths were converted in Go to the bounded two-step read.
	//
	// `CREATE TABLE IF NOT EXISTS clauses` is a no-op against an existing view of
	// that name, so applying the schema to a migrated corpus does not resurrect
	// the table. The three `CREATE INDEX ... ON clauses` in the same file are NOT
	// a no-op — DuckDB refuses to index a view, and schema application is
	// all-or-nothing — so they are bracketed by markers and both readers of
	// schema.sql strip them here. See store.SchemaForView.
	if err := createView(h); err != nil {
		return err
	}
	return restamp(h)
}

// createView installs the compatibility view. ONE definition, shared by the conversion
// and by --repair-view, so the two can never install subtly different views.
func createView(h *sql.DB) error {
	if _, err := h.Exec(`CREATE OR REPLACE VIEW clauses AS
		SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path, b.heading,
		       (SELECT string_agg(p.part, ` + sep + ` ORDER BY s.ord)
		          FROM body_seq s JOIN paragraphs p USING (para_id)
		         WHERE s.body_id = o.body_id) AS text,
		       o.is_normative, b.embedding, b.embedding_hash
		FROM clause_occ o JOIN bodies b USING (body_id)`); err != nil {
		return fmt.Errorf("compatibility view: %w", err)
	}
	return nil
}

// restamp RE-STATES WHAT THE CONVERSION JUST CHANGED.
//
// schema_meta.embedding_count is the corpus telling the server how many
// vectors it holds, and the server refuses to use the index if the number
// disagrees with reality — the corruption gate in store.LoadVSS. The
// conversion collapses 2 752 688 references onto the 821 146 distinct vectors
// they point at, so the old number stops being true the moment `clauses` goes.
//
// Left stale, the gate fires on a perfectly good corpus: "embedding count
// drift (meta=2207218 have=821146)", vector search silently degrades to an
// exact scan over every vector, and nothing anywhere reports an error. Correct
// answers, O(N), no signal. Exactly what these markers exist to prevent.
//
// hnsw_state goes with it. Whatever index existed was on a table that no
// longer exists; unless one is already on `bodies`, the corpus must not claim
// to be frozen. The `index` step runs after this one and re-stamps both.
func restamp(h *sql.DB) error {
	var vecs int
	if err := h.QueryRow(`SELECT count(*) FROM bodies WHERE embedding IS NOT NULL`).Scan(&vecs); err != nil {
		return fmt.Errorf("counting the vectors the corpus now holds: %w", err)
	}
	if _, err := h.Exec(`INSERT INTO schema_meta (key, value) VALUES ('embedding_count', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, fmt.Sprintf("%d", vecs)); err != nil {
		return fmt.Errorf("stamping embedding_count: %w", err)
	}
	var indexed int
	_ = h.QueryRow(`SELECT count(*) FROM duckdb_indexes() WHERE index_name = 'bodies_hnsw'`).Scan(&indexed)
	if indexed == 0 {
		if _, err := h.Exec(`INSERT INTO schema_meta (key, value) VALUES ('hnsw_state', 'building')
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`); err != nil {
			return fmt.Errorf("clearing hnsw_state: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  %d vector(s) on `bodies`, no index yet — hnsw_state=building until `index` runs\n", vecs)
	} else {
		fmt.Fprintf(os.Stderr, "  %d vector(s) on `bodies`, bodies_hnsw present\n", vecs)
	}

	fmt.Fprintln(os.Stderr, "  `clauses` is now a view over the occurrences")
	_, err := h.Exec(`CHECKPOINT`)
	return err
}

// repairView recovers the one state a killed `--restore` leaves behind.
//
// `--restore` materialises `clauses` from the content-addressed tables. Interrupt it —
// a full disk, a Ctrl-C, a stopped background job — and the table is left in place but
// EMPTY, shadowing the compatibility view. The corpus then serves nothing at all while
// every byte of it is still there: clause_occ, bodies, body_seq and paragraphs are
// untouched, the vectors are untouched, bodies_hnsw is untouched.
//
// Neither existing path recovers it. `--drop-clauses` rebuilds the content-addressed
// tables from `clauses` first, and refuseToShrink correctly stops 0 rows from replacing
// millions of occurrences. `--restore` would redo the materialisation that filled the
// disk in the first place. This happened on 2026-08-29 and had to be repaired by hand,
// which is a bad thing to have to do to a 12 GB corpus with no backup.
//
// It is deliberately NARROW because it drops a table: it refuses unless the corpus
// carries exactly the signature of an interrupted restore.
func repairView(h *sql.DB) error {
	var kind string
	if err := h.QueryRow(
		`SELECT COALESCE(max(table_type),'') FROM information_schema.tables WHERE table_name='clauses'`,
	).Scan(&kind); err != nil {
		return fmt.Errorf("inspecting the shape of `clauses`: %w", err)
	}
	if strings.EqualFold(kind, "VIEW") {
		fmt.Fprintln(os.Stderr, "  `clauses` is already a view — nothing to repair")
		return nil
	}
	if kind == "" {
		return fmt.Errorf("there is no `clauses` at all; this is not an interrupted restore")
	}

	// Order matters: a corpus that was never converted has no clause_occ to count, and
	// "table does not exist" is a far worse thing to tell someone than the real reason.
	var rows int64
	if err := h.QueryRow(`SELECT count(*) FROM clauses`).Scan(&rows); err != nil {
		return fmt.Errorf("counting `clauses`: %w", err)
	}
	if rows != 0 {
		return fmt.Errorf(
			"`clauses` holds %d rows, so this is a real table with data in it, not the empty\n"+
				"shell an interrupted restore leaves. Dropping it here could destroy a corpus that\n"+
				"was never converted. Use --drop-clauses, which verifies before it drops", rows)
	}

	var occ int64
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil {
		return fmt.Errorf("counting occurrences: %w", err)
	}
	if occ == 0 {
		return fmt.Errorf(
			"`clauses` is empty AND clause_occ holds nothing: there is no corpus underneath to\n" +
				"serve through a view. Installing one would produce an empty corpus that looks healthy")
	}

	fmt.Fprintf(os.Stderr, "  interrupted restore: `clauses` is an empty table over %d occurrence(s)\n", occ)
	if _, err := h.Exec(`DROP TABLE clauses`); err != nil {
		return fmt.Errorf("dropping the empty table: %w", err)
	}
	if err := createView(h); err != nil {
		return err
	}
	return restamp(h)
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

// errAlreadyConverted ends the run as a success: there is nothing to convert.
var errAlreadyConverted = errors.New("already converted")

// alreadyConverted reports whether `clauses` is the compatibility VIEW, which is the
// one input the rebuild must never run against.
//
// THE REBUILD IS NOT IDEMPOTENT, AND ITS COMMENTS USED TO SAY IT WAS.
//
// The steps run in order: staging reads `clauses`, then `paragraphs` and `body_seq` are
// REPLACED with fresh row_number() ids, and only then is `clause_occ` re-derived — also
// `FROM clauses`. When `clauses` is a real table that ordering is harmless, because the
// table owes nothing to the tables being replaced. When it is the VIEW, it is defined as
// clause_occ ⋈ bodies ⋈ body_seq ⋈ paragraphs, so by the time `clause_occ` is rebuilt the
// view is joining freshly renumbered paragraphs against the OLD body_ids still in
// clause_occ. It resolves almost nothing.
//
// Measured on 2026-08-29 against the real corpus: `clause_occ` went from 2 752 688 rows
// to 140 047, and `verify` then passed — 140047/140047 rebuild byte-for-byte — because it
// compares the rebuild against the same broken view. Every count agreed with every other
// count about a corpus that had lost 95% of its occurrences. Only `drop` failed, and only
// because `DROP TABLE clauses` cannot drop a view.
//
// refuseToShrink cannot catch this: it runs BEFORE the rebuild, when `clauses` (the view)
// and `clause_occ` still report the same 2 752 688 rows.
//
// So the case is refused at the entrance instead. It costs nothing to detect and it is
// exactly the case the pipeline hits every time it re-runs `paragraphs` on a corpus that
// is already content-addressed — which internal/goal's step comment claimed was "a no-op
// re-derivation". It is now genuinely a no-op, rather than being described as one.
func alreadyConverted(h *sql.DB) (bool, error) {
	var kind string
	if err := h.QueryRow(
		`SELECT COALESCE(max(table_type),'') FROM information_schema.tables WHERE table_name='clauses'`,
	).Scan(&kind); err != nil {
		return false, fmt.Errorf("inspecting the shape of `clauses`: %w", err)
	}
	if !strings.EqualFold(kind, "VIEW") {
		return false, nil
	}
	// A view over nothing is not a converted corpus; let the normal paths report it.
	var occ int64
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil {
		return false, fmt.Errorf("`clauses` is a view but clause_occ is unreadable: %w", err)
	}
	if occ == 0 {
		return false, nil
	}
	fmt.Fprintf(os.Stderr,
		"  `clauses` is already the compatibility view over %d occurrence(s) — the corpus is\n"+
			"  content-addressed and there is nothing to convert. Rebuilding from the view would\n"+
			"  renumber paragraphs/body_seq under clause_occ's feet and destroy it.\n", occ)
	return true, nil
}

// refuseToShrink stops the one way this conversion can destroy a corpus.
//
// The build derives the tables from whatever `clauses` currently holds, with
// CREATE OR REPLACE. That is right the first time and on a full rebuild. It is
// catastrophic against a PARTIAL one. `merge --base` compact-copies the base
// table by table, so a converted corpus's `clauses` VIEW is left behind and
// schema.sql recreates it empty; the fold then fills it with the changed buckets
// alone. Re-deriving from that would replace 2 752 688 occurrences with the
// handful the delta carried — a corpus silently reduced to its last increment,
// with every gate still green because the numbers agree with each other.
//
// The pipeline no longer produces that input: `merge` restores `clauses` as a
// real table before folding (see restore), so a re-derivation always runs
// against a whole corpus. This stays anyway. It costs one count, it is the only
// thing standing between a mis-sequenced run and a corpus that agrees with
// itself about being empty, and the day it fires is the day nobody expects it.
func refuseToShrink(h *sql.DB) error {
	var occ int64
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil {
		return nil // no occurrences yet: this is the first conversion, nothing to lose
	}
	if occ == 0 {
		return nil
	}
	var clauses int64
	if err := h.QueryRow(`SELECT count(*) FROM clauses`).Scan(&clauses); err != nil {
		return fmt.Errorf("the corpus carries %d occurrences and no readable `clauses` to rebuild them from: %w", occ, err)
	}
	if clauses < occ {
		return fmt.Errorf(
			"REFUSING to rebuild: `clauses` holds %d rows but the corpus already carries %d occurrences.\n"+
				"Re-deriving would replace the corpus with that smaller set. This is what a DELTA merge looks like,\n"+
				"and the conversion is not incremental yet — rebuild from a full corpus, or extend it to merge",
			clauses, occ)
	}
	return nil
}

// restore is the exact inverse of drop: it materialises `clauses` back into a
// real table and removes the content-addressed tables it came from.
//
// It exists because of a constraint the conversion cannot argue with. The Rust
// write side folds a delta with `merge --base`, which starts by COMPACT-COPYING
// the base — table by table, from `duckdb_tables()`. A view is not a table, so
// the copy silently leaves `clauses` behind, and schema.sql then recreates it
// EMPTY in the destination. `merge` folds the changed buckets into that empty
// table and the result is a corpus whose `clauses` holds the increment while
// `clause_occ` still holds all 2 752 688 occurrences — the precise input
// refuseToShrink refuses, arrived at by a route nobody would predict.
//
// Three further things break in the same state, all quietly:
//
//   - max_chunk_id() reads `clauses`, so the offset applied to a folded shard is
//     0 and its chunk_ids collide with the occurrences already in the corpus;
//   - changed_buckets() compares the shard against an empty `clauses`, so every
//     bucket looks changed and the delta stops being a delta;
//   - stash_bucket_vectors() carries embeddings across a bucket replacement by
//     reading them from `clauses`, and finds none.
//
// Teaching the write side about paragraphs, bodies and occurrences would fix all
// four. It would also put the storage layout of ADR 0004 into the one component
// ADR 0001 arranged for it not to be in, and each of those four is a place where
// getting it subtly wrong produces a corpus that passes every gate. Restoring
// the shape the write side has always seen, and converting again afterwards,
// costs one grouped reconstruction — 1 m 47 for 2.87 GB on this corpus, measured
// — and needs no write-side change beyond one: Store::open_rw had to learn not
// to apply `CREATE INDEX ... ON clauses` when that name is a view. That is the
// narrow rule "you cannot index a view", not knowledge of this storage layout.
func restore(h *sql.DB) error {
	var occ int64
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil || occ == 0 {
		fmt.Fprintln(os.Stderr, "  nothing to restore — this corpus is not content-addressed")
		return nil
	}
	var isView bool
	if err := h.QueryRow(`SELECT count(*) > 0 FROM duckdb_views()
		WHERE database_name = current_database() AND schema_name = 'main' AND view_name = 'clauses'`).
		Scan(&isView); err != nil {
		return fmt.Errorf("looking for the clauses view: %w", err)
	}
	if !isView {
		// `clauses` is a real table and the occurrences are also there. Both
		// shapes present means the last conversion was additive and never
		// dropped anything, so there is nothing to give back.
		fmt.Fprintln(os.Stderr, "  `clauses` is already a table — leaving it alone")
		return nil
	}

	steps := []struct{ label, sql string }{
		// ONE grouped reconstruction, not the view. The view rebuilds a body with
		// a correlated scalar subquery, which is right for the bounded reads it
		// was written for (a search window, one clause) and quadratic here: 5.34 s
		// for 1 191 clauses is 3+ hours for the corpus. Grouping body_seq once and
		// joining costs 1 m 47 for all of it.
		{"reconstruct", `CREATE OR REPLACE TABLE _mig_restore AS
			WITH r AS (
				SELECT s.body_id, string_agg(p.part, ` + sep + ` ORDER BY s.ord) AS body
				FROM body_seq s JOIN paragraphs p USING (para_id) GROUP BY s.body_id
			)
			SELECT o.chunk_id, o.spec_id, o.release, o.version, o.clause_path,
			       b.heading, r.body AS text, o.is_normative, b.embedding, b.embedding_hash
			FROM clause_occ o JOIN bodies b USING (body_id) JOIN r USING (body_id)`},
	}
	for _, s := range steps {
		start := time.Now()
		if _, err := h.Exec(s.sql); err != nil {
			return fmt.Errorf("%s: %w", s.label, err)
		}
		fmt.Fprintf(os.Stderr, "  %-12s in %s\n", s.label, time.Since(start).Round(time.Second))
	}

	// Assert BEFORE anything is dropped, because after the drop there is nothing
	// left to compare against and a wrong answer becomes the corpus.
	//
	// This is not the byte-for-byte proof `verify` runs — it cannot be, since the
	// only reference for the text is the tables being read. It is the check that
	// the join behaved: one row per occurrence, no fanout, no row lost to a NULL
	// on the way through, every chunk_id still distinct.
	var rows, distinct, nulls int64
	if err := h.QueryRow(`SELECT count(*), count(DISTINCT chunk_id),
	                             count(*) FILTER (WHERE text IS NULL) FROM _mig_restore`).
		Scan(&rows, &distinct, &nulls); err != nil {
		return fmt.Errorf("checking the reconstruction: %w", err)
	}
	switch {
	case rows != occ:
		return fmt.Errorf("reconstructed %d rows from %d occurrences — refusing to drop the originals", rows, occ)
	case distinct != rows:
		return fmt.Errorf("%d rows carry only %d distinct chunk_ids — the join fanned out", rows, distinct)
	case nulls != 0:
		return fmt.Errorf("%d reconstructed clauses have no text", nulls)
	}
	fmt.Fprintf(os.Stderr, "  reconstructed   %d clauses, %d distinct chunk_ids, no empties\n", rows, distinct)

	// The view has to go before schema.sql runs, or `CREATE TABLE IF NOT EXISTS
	// clauses` sees a relation of that name and does nothing — the same no-op
	// that lets the schema be applied to a converted corpus safely, working
	// against us here.
	if _, err := h.Exec(`DROP VIEW IF EXISTS clauses; DROP VIEW IF EXISTS clauses_probe`); err != nil {
		return fmt.Errorf("dropping the compatibility view: %w", err)
	}
	if _, err := h.Exec(store.SchemaSQL()); err != nil {
		return fmt.Errorf("recreating the declared schema: %w", err)
	}
	// Column ORDER, not just column names: rust/store's compact copy is
	// `INSERT INTO dst SELECT * FROM src`, so a table whose columns are in a
	// different order than schema.sql declares would be copied into the wrong
	// ones. Naming them here and letting DuckDB match by name is what makes the
	// order come from schema.sql rather than from the SELECT above.
	if _, err := h.Exec(`INSERT INTO clauses
		(chunk_id, spec_id, release, version, clause_path, heading, text, is_normative, embedding, embedding_hash)
		SELECT chunk_id, spec_id, release, version, clause_path, heading, text, is_normative, embedding, embedding_hash
		FROM _mig_restore`); err != nil {
		return fmt.Errorf("filling the restored table: %w", err)
	}
	for _, t := range []string{"_mig_restore", "clause_occ", "body_seq", "bodies", "paragraphs"} {
		if _, err := h.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
			return fmt.Errorf("dropping %s: %w", t, err)
		}
	}
	fmt.Fprintf(os.Stderr, "  `clauses` is a table again (%d rows); the corpus is in write-side shape\n", rows)
	_, err := h.Exec(`CHECKPOINT`)
	return err
}
