// Command freeze-hnsw builds and freezes the cosine HNSW index over a corpus,
// on whichever table actually holds the vectors.
//
//	freeze-hnsw --db data/3gpp.duckdb
//
// It exists because rust/store's freeze-hnsw indexes `clauses` by name, and on a
// content-addressed corpus (ADR 0004) that name is a VIEW over the occurrences:
// the vectors live on `bodies`, one per distinct text. Indexing the occurrences
// would index 2 752 688 copies of 897 556 vectors — paying the duplication the
// conversion exists to remove, in RAM and in build time — and DuckDB refuses to
// index a view at all, so it does not even get that far.
//
// internal/store already resolves the right target for both shapes and is tested
// on both, so this is a thin front for store.BuildAndFreezeHNSW rather than a
// second implementation of it. A Go binary that writes the corpus is the
// established exception for offline producer tools (see internal/store/reader.go);
// what must never write is the SERVED binary, and this is not it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "data/3gpp.duckdb", "corpus to index")
	model := flag.String("model", "", "embedding model to stamp (default: the corpus's own `embedding_model`)")
	flag.Parse()

	st, err := store.Open(*db)
	if err != nil {
		die("open %s: %v", *db, err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	// THE BUILD MATERIALISES THE WHOLE EMBEDDING COLUMN. GIVE IT ITS CEILINGS.
	//
	// Same two knobs, same names and same reasons as the Rust tool this replaces:
	// a buffer budget, and a cap on the spill it takes when that budget does not
	// fit. The spill is a DISK ceiling and DuckDB defaults it to 90% of whatever
	// is free — which is not a guard but a race against the file being written.
	// preserve_insertion_order is pure cost: HNSW is unordered and the scan
	// feeding it has no ORDER BY, so keeping row order only widens what spills.
	knobs := []string{
		fmt.Sprintf("SET memory_limit = '%s'", envOr("HNSW_BUILD_MEMORY_LIMIT", "4GB")),
		"SET preserve_insertion_order = false",
	}
	if t := os.Getenv("HNSW_BUILD_TEMP_LIMIT"); strings.TrimSpace(t) != "" {
		knobs = append(knobs, fmt.Sprintf("SET max_temp_directory_size = '%s'", t))
	}
	if n := os.Getenv("HNSW_BUILD_THREADS"); strings.TrimSpace(n) != "" {
		knobs = append(knobs, fmt.Sprintf("SET threads = %s", n))
	}
	for _, k := range knobs {
		if _, err := st.DB().ExecContext(ctx, k); err != nil {
			die("%s: %v", k, err)
		}
	}

	m := *model
	if m == "" {
		m = st.GetMeta(ctx, "embedding_model")
	}
	if m == "" {
		die("the corpus carries no `embedding_model` and none was given — refusing to stamp an index with an unknown identity")
	}
	// ALREADY DONE IS NOT THE SAME AS CHEAP TO REDO. The index survives a rebuild
	// (the CREATE is IF NOT EXISTS), but the run still flips hnsw_state to
	// "building" and back and checkpoints twice — measured 2026-09-05 on the
	// shipped corpus, a freeze with nothing to do moved the file by 262 144 bytes.
	// The corpus is one layer of the published image and imgtar zeroes tar mtimes,
	// so content alone decides the layer digest: an unchanged corpus is answered
	// "existing blob", a changed one is an 11 GB upload. Every build re-froze, so
	// every build re-pushed.
	if ok, why := st.HNSWFrozenAndCurrent(ctx, m); ok {
		fmt.Fprintf(os.Stderr, "freeze-hnsw: already frozen for %s and current — leaving the corpus untouched\n", m)
		fmt.Printf("hnsw_state=frozen embedding_count=%s\n", st.GetMeta(ctx, "embedding_count"))
		return
	} else if why != "" {
		fmt.Fprintf(os.Stderr, "freeze-hnsw: rebuilding because %s\n", why)
	}

	fmt.Fprintf(os.Stderr, "freeze-hnsw: building the cosine index for %s (%s)\n", m, strings.Join(knobs, "; "))
	if err := st.BuildAndFreezeHNSW(ctx, m); err != nil {
		die("build: %v", err)
	}
	if got := st.GetMeta(ctx, "hnsw_state"); got != "frozen" {
		die("the build reported success but hnsw_state is %q", got)
	}
	fmt.Fprintf(os.Stderr, "freeze-hnsw: frozen cosine HNSW index for model %s\n", m)
	fmt.Printf("hnsw_state=frozen embedding_count=%s\n", st.GetMeta(ctx, "embedding_count"))
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "freeze-hnsw: "+f+"\n", a...)
	os.Exit(1)
}
