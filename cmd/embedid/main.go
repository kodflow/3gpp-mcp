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
package main

import (
	"fmt"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

func main() {
	fmt.Print(embed.ResolveModelID("bge-m3"))
}
