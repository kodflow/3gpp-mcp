package main

// checkdata.go — the IMAGE-BUILD inheritance guard. `mcp-3gpp check-data` opens a
// DuckDB snapshot read-only and asserts it is fully indexed before the image that
// embeds it is allowed to ship. It exists because the mcp image inherits its data
// layer FROM 3gpp-data by digest pinned at build time: a correct (FTS+HNSW) data
// image can sit on the registry while an OLD, unindexed layer is the one actually
// baked into mcp:full. Serving that layer is catastrophic (LIKE full-scan p99,
// exact-scan recall) yet silent. Running this as a `RUN` step in the full stage
// turns "stale inherited data layer" from a 23h-blind prod incident into a hard
// build failure. See [[project_served_stale_data_layer]].
//
// It is deliberately the SHIPPED binary self-checking (no extra COPY of a tool):
// the same DuckDB the server links does the assertion. cmd/validate remains the
// full corpus promotion gate (anti-leak, release set, sha sidecars, …); this is a
// narrow, fast subset focused on the two index invariants the inheritance can break.

import (
	"context"
	"flag"
	"fmt"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// checkData implements `mcp-3gpp check-data --db <path> [--require-fts] [--require-hnsw]`.
// Returns a non-nil error (→ exit 1) on the FIRST violated invariant so the build
// fails loudly; prints an OK line and returns nil when the DB is fully indexed.
func checkData(args []string) error {
	fs := flag.NewFlagSet("check-data", flag.ExitOnError)
	dbPath := fs.String("db", "/data/mcp-3gpp/3gpp.duckdb", "DuckDB snapshot to verify")
	requireFTS := fs.Bool("require-fts", true, "fail if the BM25 FTS index is absent")
	requireHNSW := fs.Bool("require-hnsw", true, "fail if the DB carries vectors but its HNSW index is not frozen")
	// Extended contract (off by default so a code-only build with the default dense
	// contract is unaffected; the data-completeness gate passes these via scripts/
	// data-contract.sh). Mirror cmd/validate exactly so both gates agree.
	requireEmbed := fs.Bool("require-embed-complete", false, "fail unless NO clause at/above --embed-floor still lacks a vector (dense convergence)")
	embedFloor := fs.String("embed-floor", "", "release floor for --require-embed-complete; empty = all releases")
	requireSparse := fs.Bool("require-sparse", false, "fail unless clause_sparse is populated and sparse_model matches this build's sparse identity")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", *dbPath, err)
	}
	defer func() { _ = st.Close() }()

	// FTS: LoadFTS returns an error when no persisted index exists (it never
	// rebuilds). A present index → FTSAvailable() true.
	ftsErr := st.LoadFTS(ctx)
	ftsOK := st.FTSAvailable()

	hasVectors := st.GetMeta(ctx, "embedding_model") != ""
	hnswState := st.GetMeta(ctx, "hnsw_state")
	hnswFrozen := hnswState == "frozen"

	// Dense convergence (floor-aware, like cmd/embed --count-null-at-floor): no clause
	// at/above the floor still NULL. Below-floor/legacy clauses are intentionally NULL.
	nullAtFloor := -1
	if *requireEmbed {
		floorOrd := 0
		if *embedFloor != "" {
			if o, ok := model.ReleaseOrdinal(*embedFloor); ok {
				floorOrd = o
			} else {
				return fmt.Errorf("unparseable --embed-floor %q", *embedFloor)
			}
		}
		if n, err := st.CountNullAtFloor(ctx, floorOrd, ""); err != nil {
			return fmt.Errorf("count null at floor: %w", err)
		} else {
			nullAtFloor = n
		}
	}

	// Sparse arm: populated AND the stamped sparse_model matches this build's sparse
	// identity (a stale sparse layer fails, mirroring cmd/validate --require-sparse).
	sparseOK, sparseModel, wantSparse := true, "", ""
	if *requireSparse {
		_ = st.LoadSparse(ctx)
		wantSparse = embed.SparseModelID() // "" when this build has no sparse head
		sparseModel = st.GetMeta(ctx, "sparse_model")
		sparseOK = st.SparseAvailable() && (wantSparse == "" || sparseModel == wantSparse)
	}

	// Report the full picture first (so a failing build log shows every signal at
	// once, not just the first tripwire).
	fmt.Printf("check-data: db=%s fts_index=%v vectors=%v hnsw_state=%q null_at_floor=%d sparse_model=%q\n",
		*dbPath, ftsOK, hasVectors, hnswState, nullAtFloor, sparseModel)

	if *requireFTS && !ftsOK {
		return fmt.Errorf("FTS index absent (LoadFTS: %v) — server would fall back to LIKE full-scan; "+
			"re-bake the corpus DB (ingest builds the FTS index)", ftsErr)
	}
	if *requireHNSW && hasVectors && !hnswFrozen {
		return fmt.Errorf("vectors present but hnsw_state=%q (want \"frozen\") — vector arm would degrade to "+
			"bounded exact-scan; run cmd/freeze-hnsw as the final bake step", hnswState)
	}
	if *requireEmbed && nullAtFloor != 0 {
		return fmt.Errorf("dense incomplete: %d clause(s) at/above floor %q still lack a vector — "+
			"the embed campaign has not converged; do not promote this data layer", nullAtFloor, *embedFloor)
	}
	if *requireSparse && !sparseOK {
		return fmt.Errorf("sparse incomplete/stale: sparse_available=%v sparse_model=%q expected=%q — "+
			"run the sparse campaign to convergence before promoting", st.SparseAvailable(), sparseModel, wantSparse)
	}
	fmt.Println("check-data: OK — data layer meets the completeness contract")
	return nil
}
