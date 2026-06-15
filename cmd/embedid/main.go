// Command embedid prints the canonical EmbedIdentity of the ACTIVE embedding
// model (registry resolution: $EMBED_MODEL > $EMBED_MODELS_CONFIG / models.yaml
// `active` > built-in default) — the exact string stamped into DB meta as
// embedding_model and compared by serve's coherence guards.
//
// CI helper: the image bake computes the identity its baked models.yaml will
// make the server report, and hands it to cmd/overlay --embedding-model so the
// fused DB carries the same truth the runtime checks against. CGO-free (the
// registry/identity path needs no ONNX), so it runs anywhere with `go run`.
//
//	EMBED_MODELS_CONFIG=image-data/models/models.yaml go run ./cmd/embedid
//
// With --sparse it prints the SPARSE identity instead (the sparse_model meta a
// --sparse-only pass stamps; empty when the active model has no sparse head). The
// bake self-heal compares this against the DB's stamped sparse_model to decide
// whether a sparse-only pass is needed — WITHOUT re-running the dense embed.
//
//	EMBED_MODELS_CONFIG=image-data/models/models.yaml go run ./cmd/embedid --sparse
package main

import (
	"flag"
	"fmt"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

func main() {
	sparse := flag.Bool("sparse", false, "print the SPARSE identity (sparse_model) instead of the dense EmbedIdentity")
	flag.Parse()
	if *sparse {
		fmt.Print(embed.ResolveSparseID("bge-m3"))
		return
	}
	fmt.Print(embed.ResolveModelID("bge-m3"))
}
