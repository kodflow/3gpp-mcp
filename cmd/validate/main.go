// Command validate is the single promotion gate for the corpus pipeline: it asserts
// that a DuckDB snapshot is complete, coherent, and SAFE to publish before `latest`
// is ever moved to it (plan §5). Every publication path — manual collect, CI collect,
// CI corpus, CI image — runs it; the image never consumes an unvalidated DB.
//
//	validate --db data/3gpp.duckdb --pending-zero --require-fts --require-hnsw \
//	         --expected-releases "Rel-99,Rel-4,...,Rel-20" --min-clauses 2000000 \
//	         --embedding-dim 1024 --expected-embed-identity <digest> \
//	         --zst data/3gpp.duckdb.zst --sha data/3gpp.duckdb.sha256 \
//	         --repo-visibility public --forbid-fulltext-artifacts
//
// Any failed check is collected and reported; the process exits non-zero if ANY
// invariant is violated (fail-closed). The anti-leak guard inspects DB CONTENT
// (clauses.text), never a filename (plan §5.2): a public channel must never carry
// verbatim 3GPP text.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

var Version = "dev"

func main() {
	var (
		dbPath        = flag.String("db", "data/3gpp.duckdb", "DuckDB snapshot to validate")
		pendingZero   = flag.Bool("pending-zero", false, "fail unless EVERY clause has an embedding (null_embeddings==0)")
		requireFTS    = flag.Bool("require-fts", false, "fail if the BM25 FTS index is absent")
		requireHNSW   = flag.Bool("require-hnsw", false, "fail if the DB is vectorized but its HNSW index is not frozen")
		requireSparse = flag.Bool("require-sparse", false, "fail unless the sparse (clause_sparse) postings are populated (sparse-enabled bakes only)")
		requireETSI   = flag.String("require-etsi", "", "path to etsi.duckdb; fail unless it holds clauses, carries vectors, and was embedded with the SAME identity as --db (an ETSI half at a stale identity is served lexically, silently)")
		requireEmbed  = flag.Bool("require-embed-complete", false, "fail unless NO clause at/above --embed-floor still lacks a vector (dense convergence; floor-aware, unlike --pending-zero)")
		embedFloor    = flag.String("embed-floor", "", "release floor for --require-embed-complete (e.g. Rel-99); empty = all releases. Below-floor/legacy clauses are intentionally NULL and never counted.")
		embeddingDim  = flag.Int("embedding-dim", 0, "if >0, fail unless schema_meta embedding_dim == this")
		minClauses    = flag.Int("min-clauses", 0, "if >0, fail unless clause count >= this (anti-shrink guard)")
		expRels       = flag.String("expected-releases", "", "comma-separated set; fail unless the DB's releases == this set exactly")
		expIdentity   = flag.String("expected-embed-identity", "", "fail unless schema_meta embedding_model == this (EmbedIdentity)")
		zstPath       = flag.String("zst", "", "compressed artifact whose sha256 is checked against --sha")
		shaPath       = flag.String("sha", "", "sha256 sidecar for --zst (format: '<hex>  <name>')")
		repoVis       = flag.String("repo-visibility", "", "public|private — drives the anti-leak guard")
		forbidFull    = flag.Bool("forbid-fulltext-artifacts", false, "with --repo-visibility public: fail if the DB carries verbatim clause text (anti-leak)")
		maxEmptyMeta  = flag.Int("max-empty-meta", -1, "if >=0, fail unless the count of clause-bearing specs missing catalog title/WG is <= this (catalog coverage guard)")
		report        = flag.String("report", "text", "text | json")
	)
	flag.Parse()

	res := runChecks(context.Background(), checkCfg{
		db: *dbPath, pendingZero: *pendingZero, requireFTS: *requireFTS, requireHNSW: *requireHNSW, requireSparse: *requireSparse,
		requireETSI: *requireETSI,
		requireEmbedComplete: *requireEmbed, embedFloor: *embedFloor,
		embeddingDim: *embeddingDim, minClauses: *minClauses, expectedReleases: splitCSV(*expRels),
		expectedIdentity: *expIdentity, zst: *zstPath, sha: *shaPath,
		repoVisibility: *repoVis, forbidFulltext: *forbidFull,
		emptyMetaGuard: *maxEmptyMeta >= 0, maxEmptyMeta: *maxEmptyMeta,
	})

	if *report == "json" {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	} else {
		for _, c := range res.Checks {
			mark := "ok"
			if !c.Pass {
				mark = "FAIL"
			}
			fmt.Printf("[%-4s] %s: %s\n", mark, c.Name, c.Detail)
		}
	}
	if !res.OK {
		os.Exit(1)
	}
}

type checkCfg struct {
	db                                         string
	pendingZero, requireFTS, requireHNSW       bool
	requireSparse                              bool
	requireETSI                                string
	requireEmbedComplete                       bool
	embedFloor                                 string
	embeddingDim, minClauses                   int
	expectedReleases                           []string
	expectedIdentity, zst, sha, repoVisibility string
	forbidFulltext                             bool
	// emptyMetaGuard gates the catalog-coverage check; maxEmptyMeta is the
	// threshold. A separate bool (not a sentinel) because 0 is a MEANINGFUL
	// strict threshold here, so it cannot double as "disabled".
	emptyMetaGuard bool
	maxEmptyMeta   int
}

type check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

type result struct {
	OK     bool    `json:"ok"`
	Checks []check `json:"checks"`
}

func (r *result) add(name string, pass bool, format string, a ...any) {
	r.Checks = append(r.Checks, check{Name: name, Pass: pass, Detail: fmt.Sprintf(format, a...)})
	if !pass {
		r.OK = false
	}
}

func runChecks(ctx context.Context, cfg checkCfg) result {
	res := result{OK: true}

	// Checksum is file-level and independent of opening the DB — do it first.
	if cfg.zst != "" && cfg.sha != "" {
		if got, want, err := checksum(cfg.zst, cfg.sha); err != nil {
			res.add("checksum", false, "%v", err)
		} else {
			res.add("checksum", got == want, "zst=%s sha256=%s (sidecar=%s)", cfg.zst, short(got), short(want))
		}
	}

	db, err := store.OpenReadOnly(cfg.db)
	if err != nil {
		res.add("db-openable", false, "open %s: %v", cfg.db, err)
		return res // nothing else is checkable
	}
	defer func() { _ = db.Close() }()
	res.add("db-openable", true, "%s", cfg.db)
	sqldb := db.DB()

	// clause count + min-clauses
	var clauses int
	if err := sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&clauses); err != nil {
		res.add("clauses", false, "count clauses: %v", err)
	} else if cfg.minClauses > 0 {
		res.add("min-clauses", clauses >= cfg.minClauses, "clauses=%d (min=%d)", clauses, cfg.minClauses)
	} else {
		res.add("clauses", clauses > 0, "clauses=%d", clauses)
	}

	// expected-releases (exact set)
	if len(cfg.expectedReleases) > 0 {
		got, err := distinctReleases(ctx, sqldb)
		if err != nil {
			res.add("expected-releases", false, "%v", err)
		} else {
			missing, extra := diffSets(cfg.expectedReleases, got)
			pass := len(missing) == 0 && len(extra) == 0
			res.add("expected-releases", pass, "got=%d missing=%v extra=%v", len(got), missing, extra)
		}
	}

	// pending-zero (every clause embedded)
	if cfg.pendingZero {
		if n, err := db.CountNullEmbeddings(ctx); err != nil {
			res.add("pending-zero", false, "count null embeddings: %v", err)
		} else {
			res.add("pending-zero", n == 0, "null_embeddings=%d", n)
		}
	}

	// require-embed-complete (dense convergence): no clause at/above the floor still
	// NULL. Floor-aware via the SAME oracle cmd/embed uses (CountNullAtFloor), so
	// intentionally-skipped below-floor/legacy clauses never count as a failure — the
	// right "dense is done" signal for a floored corpus (unlike global pending-zero).
	if cfg.requireEmbedComplete {
		floorOrd := 0
		if cfg.embedFloor != "" {
			if o, ok := model.ReleaseOrdinal(cfg.embedFloor); ok {
				floorOrd = o
			} else {
				res.add("require-embed-complete", false, "unparseable --embed-floor %q", cfg.embedFloor)
			}
		}
		if n, err := db.CountNullAtFloor(ctx, floorOrd, ""); err != nil {
			res.add("require-embed-complete", false, "count null at floor: %v", err)
		} else {
			res.add("require-embed-complete", n == 0, "null_at_floor=%d (floor=%q)", n, cfg.embedFloor)
		}
	}

	// embedding-dim (schema_meta)
	if cfg.embeddingDim > 0 {
		got := db.GetMeta(ctx, "embedding_dim")
		res.add("embedding-dim", got == fmt.Sprintf("%d", cfg.embeddingDim), "schema_meta.embedding_dim=%q want=%d", got, cfg.embeddingDim)
	}

	// expected-embed-identity (schema_meta embedding_model == EmbedIdentity digest)
	if cfg.expectedIdentity != "" {
		got := db.GetMeta(ctx, "embedding_model")
		res.add("embed-identity", got == cfg.expectedIdentity, "schema_meta.embedding_model=%q want=%q", got, cfg.expectedIdentity)
	}

	// require-hnsw — the index must be there, AND the server must agree that it is.
	//
	// hnsw_state is a schema_meta row, and rows travel. merge compacts the corpus with
	// COPY FROM DATABASE, which copies the data and deliberately leaves custom indexes
	// behind — so the flag lands in the new file still reading "frozen" while the index
	// it describes is gone. Checking only the flag passed a corpus that could answer
	// lexically and nothing else, which is exactly the failure `smoke` was written to
	// catch after it shipped for months.
	//
	// Checking the flag AND the index was still not enough, and the gap cost a shipped
	// corpus. This asked HNSWIndexPresent, which resolves the index name through
	// hnswTarget(); the SERVER asks store.LoadVSS, which did not, and which also
	// compares embedding_count against the vectors actually present. So the gate read
	// green while the server it gates refused the same index and exact-scanned every
	// vector. Two checks of the same property, one converted and one not.
	//
	// So ask the question the server asks. LoadVSS never creates an index, and its
	// error says which of the four conditions failed, which is more than "false".
	if cfg.requireHNSW {
		st := db.GetMeta(ctx, "hnsw_state")
		present := db.HNSWIndexPresent(ctx)
		if err := db.LoadVSS(ctx); err != nil {
			res.add("require-hnsw", false,
				"hnsw_state=%q index_present=%v but the server would REFUSE this index: %v", st, present, err)
		} else {
			res.add("require-hnsw", db.VSSAvailable(),
				"hnsw_state=%q index_present=%v serve_usable=%v", st, present, db.VSSAvailable())
		}
	}

	// require-fts — LOAD (never build) then probe availability
	if cfg.requireFTS {
		_ = db.LoadFTS(ctx)
		res.add("require-fts", db.FTSAvailable(), "fts_available=%v", db.FTSAvailable())
	}

	// require-sparse — the sparse (learned-lexical) arm: clause_sparse populated AND
	// the stamped sparse layer matches the build's expected sparse identity (so a
	// STALE sparse layer — built with an older sparse model — fails the bake gate,
	// just like the dense embedding_model coherence guard). Only set on a
	// sparse-enabled bake; off by default (dense-only DBs pass).
	if cfg.requireSparse {
		_ = db.LoadSparse(ctx)
		want := embed.SparseModelID() // "" when this build has no sparse head
		got := db.GetMeta(ctx, "sparse_model")
		ok := db.SparseAvailable() && (want == "" || got == want)
		res.add("require-sparse", ok, "sparse_available=%v sparse_model=%q expected=%q", db.SparseAvailable(), got, want)
	}

	// require-etsi — the OTHER half of the corpus, which the image serves beside
	// this one and which no gate could previously talk about.
	//
	// The check that matters is the LAST one: both corpora must carry the same
	// embedding identity. internal/mcp recomputes semantic availability per store
	// and the serve-time coherence guard disables VSS when a store's model differs
	// from the client's, so re-embedding 3GPP while ETSI keeps an older identity
	// drops the ETSI arm to lexical — with no error, on a corpus that looks
	// complete from every other angle. That is the failure this flag exists for;
	// the existence and clause-count checks are there so it cannot be satisfied by
	// an empty file that trivially "agrees".
	if cfg.requireETSI != "" {
		etsi, err := store.OpenReadOnly(cfg.requireETSI)
		if err != nil {
			res.add("require-etsi", false, "cannot open %s: %v", cfg.requireETSI, err)
		} else {
			// CONVERGENCE MEANS "no clause WITH TEXT lacks a vector", not
			// "vectors == clauses". A quarter of the ETSI corpus is headings whose
			// body is empty — PDF text extraction reads numbered figure captions
			// and sequence-diagram steps as clauses — and the embedder rightly
			// produces nothing for them. Measured: 354 of 1 396 clauses empty, all
			// 354 with a NULL embedding, and ZERO clauses that have text and no
			// vector. Comparing against the total would therefore have failed every
			// build on a corpus that had in fact converged, which is a worse gate
			// than none: it teaches the operator to pass --skip.
			var withText, vectors int64
			_ = etsi.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE length(text) > 0`).Scan(&withText)
			_ = etsi.QueryRowContext(ctx,
				`SELECT count(*) FROM clauses WHERE length(text) > 0 AND embedding IS NOT NULL`).Scan(&vectors)
			etsiModel := etsi.GetMeta(ctx, "embedding_model")
			mainModel := db.GetMeta(ctx, "embedding_model")
			// AND THE INDEX, asked the way the server asks it.
			//
			// Vectors plus a matching identity are not enough to make the ETSI half
			// answer semantically: internal/mcp calls LoadVSS per store, and LoadVSS
			// refuses an index whose schema_meta.embedding_count disagrees with the
			// vectors actually present. Measured on the 2026-09-01 corpus: 510 384
			// vectors, identity equal to the 3GPP one, and embedding_count still
			// reading 1042 from the era when the ETSI half held fourteen
			// deliverables — because `index-etsi` had been recorded VALID in 1.3s,
			// hnsw_state already saying "frozen" about the index of the small
			// corpus. Every condition this gate checked was true, and the shipped
			// image would have exact-scanned or fallen back to BM25 on half its
			// content, silently. That is the same class of hole require-hnsw was
			// widened to close, on the store no gate was asking about.
			vssErr := etsi.LoadVSS(ctx)
			serveUsable := vssErr == nil && etsi.VSSAvailable()
			ok := withText > 0 && vectors == withText && etsiModel != "" && etsiModel == mainModel && serveUsable
			why := "serve_usable=true"
			if !serveUsable {
				why = fmt.Sprintf("the server would REFUSE the ETSI index: %v", vssErr)
			}
			res.add("require-etsi", ok,
				"clauses_with_text=%d vectors=%d (missing=%d) embedding_model=%q (3gpp=%q) hnsw_state=%q %s",
				withText, vectors, withText-vectors, etsiModel, mainModel,
				etsi.GetMeta(ctx, "hnsw_state"), why)
			_ = etsi.Close()
		}
	}

	// catalog coverage: specs that have indexed clauses but no catalog title/WG
	// (the DynaReport overlay never matched them, or wrote empties) — surfaces the
	// silent-partial-enrichment failure mode at publish time.
	if cfg.emptyMetaGuard {
		n, sample, err := emptyMetaSpecs(ctx, sqldb)
		if err != nil {
			res.add("empty-meta", false, "%v", err)
		} else {
			res.add("empty-meta", n <= cfg.maxEmptyMeta, "specs_with_clauses_missing_title_or_wg=%d (max=%d) e.g. %v", n, cfg.maxEmptyMeta, sample)
		}
	}

	// anti-leak: a public channel must never carry verbatim clause text (content-based)
	if cfg.repoVisibility == "public" && cfg.forbidFulltext {
		var withText int
		if err := sqldb.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE text IS NOT NULL AND text <> ''`).Scan(&withText); err != nil {
			res.add("anti-leak", false, "inspect clauses.text: %v", err)
		} else {
			// PASS = no full text present (safe to publish on a public channel).
			res.add("anti-leak", withText == 0, "clauses_with_text=%d on a PUBLIC channel (must be 0)", withText)
		}
	}

	return res
}

// distinctReleases returns the release labels present in clauses.
func distinctReleases(ctx context.Context, sqldb *sql.DB) ([]string, error) {
	rows, err := sqldb.QueryContext(ctx, `SELECT DISTINCT release FROM clauses WHERE release IS NOT NULL AND release <> '' ORDER BY release`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// emptyMetaSpecs counts spec_ids that appear in `clauses` but whose `specs` row
// is missing or carries an empty title/working_group, and returns up to 20 of
// them as a sample for the failure detail. A LEFT JOIN catches the "no specs row
// at all" case (overlay never ran) as well as the empty-string case.
func emptyMetaSpecs(ctx context.Context, sqldb *sql.DB) (int, []string, error) {
	const where = `s.spec_id IS NULL
	     OR s.title IS NULL OR s.title = ''
	     OR s.working_group IS NULL OR s.working_group = ''`
	var n int
	if err := sqldb.QueryRowContext(ctx,
		`SELECT count(*) FROM (
		   SELECT DISTINCT c.spec_id FROM clauses c
		   LEFT JOIN specs s ON s.spec_id = c.spec_id
		   WHERE `+where+`)`).Scan(&n); err != nil {
		return 0, nil, fmt.Errorf("count empty-meta specs: %w", err)
	}
	rows, err := sqldb.QueryContext(ctx,
		`SELECT DISTINCT c.spec_id FROM clauses c
		 LEFT JOIN specs s ON s.spec_id = c.spec_id
		 WHERE `+where+`
		 ORDER BY c.spec_id LIMIT 20`)
	if err != nil {
		return n, nil, fmt.Errorf("sample empty-meta specs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var sample []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return n, sample, err
		}
		sample = append(sample, id)
	}
	return n, sample, rows.Err()
}

// splitCSV parses a comma-separated flag into a trimmed, empty-dropped slice.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// diffSets returns (missing = want\got, extra = got\want), order-independent.
func diffSets(want, got []string) (missing, extra []string) {
	w, g := map[string]bool{}, map[string]bool{}
	for _, x := range want {
		w[x] = true
	}
	for _, x := range got {
		g[x] = true
	}
	for x := range w {
		if !g[x] {
			missing = append(missing, x)
		}
	}
	for x := range g {
		if !w[x] {
			extra = append(extra, x)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// checksum returns (computed, expected) sha256 hex for a file vs its sidecar.
// The sidecar format is "<hex>  <name>" (sha256sum output); only the hex matters.
func checksum(path, sidecar string) (got, want string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", "", err
	}
	got = hex.EncodeToString(h.Sum(nil))
	b, err := os.ReadFile(sidecar)
	if err != nil {
		return "", "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", "", fmt.Errorf("empty sha256 sidecar %s", sidecar)
	}
	want = fields[0]
	return got, want, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
