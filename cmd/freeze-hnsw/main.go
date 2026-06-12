// Command freeze-hnsw builds and freezes the cosine HNSW index on an already
// embedded DuckDB — the corpus-data-image bake's final step, AFTER the vector
// overlay + compaction.
//
// Why a dedicated tool: the bake fuses vectors into the DB (cmd/overlay) and then
// compacts it (DuckDB COPY FROM DATABASE), but NOTHING ever builds the HNSW index,
// and COPY FROM DATABASE would drop a custom index anyway. So the shipped DB had
// hnsw_state="" → serve's LoadVSS refused it ("hnsw not frozen") → exact-scan →
// server_info reported hnsw:false / semantic:false despite the vectors being baked.
//
// This indexes the EXISTING embedding column (no embedder, no ONNX) and stamps the
// freeze markers BuildAndFreezeHNSW writes, so serve trusts the index and turns on
// HNSW-accelerated semantic search. The embedding_model is read from the DB meta
// so the freeze never disagrees with what the overlay stamped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "", "path to the embedded DuckDB to index (writable)")
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "freeze-hnsw: --db is required")
		os.Exit(2)
	}
	ctx := context.Background()
	st, err := store.Open(*db) // writable: BuildAndFreezeHNSW does CREATE INDEX + SetMeta
	if err != nil {
		fmt.Fprintln(os.Stderr, "freeze-hnsw: open:", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()

	model := st.GetMeta(ctx, "embedding_model")
	if model == "" {
		fmt.Fprintln(os.Stderr, "freeze-hnsw: no embedding_model in meta — DB is not embedded (refusing to index an empty column)")
		os.Exit(1)
	}
	if err := st.BuildAndFreezeHNSW(ctx, model); err != nil {
		fmt.Fprintln(os.Stderr, "freeze-hnsw: build/freeze failed:", err)
		os.Exit(1)
	}
	if got := st.GetMeta(ctx, "hnsw_state"); got != "frozen" {
		fmt.Fprintf(os.Stderr, "freeze-hnsw: post-state is %q, want frozen\n", got)
		os.Exit(1)
	}
	fmt.Printf("freeze-hnsw: frozen cosine HNSW index for model %s (%s clauses)\n",
		model, st.GetMeta(ctx, "embedding_count"))
}
