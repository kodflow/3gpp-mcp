package embed

import (
	"strconv"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// BGE-M3 build-identity components (plan PR-6 / finding `model-commit-not-in-identity`).
//
// These describe EVERYTHING that changes the numeric vectors the BGE-M3 backend
// produces. They are folded into the canonical model.EmbedIdentity so that a
// weight/tokenizer/dim/normalisation/precision change flips the identity and the
// re-embed + serve-compat gates fire — instead of silently scoring a fresh query
// vector against corpus vectors built by a DIFFERENT model.
//
// The revision is the SINGLE source of truth for "which BGE-M3 weights".
// bootstrap.bgeCommit pins the actual download; this constant must equal its
// 7-hex prefix. The two live in different packages (embed must not import
// bootstrap, which drags in net/http + archive/tar), so the coupling is enforced
// by a test (identity_test.go: TestBGEModelRevisionMatchesPinnedCommit) rather
// than a compile-time reference — a future commit bump that forgets to update
// this constant fails CI.
const (
	// BGEModelRevision is the 7-hex prefix of the pinned BAAI/bge-m3 HF commit
	// (bootstrap.bgeCommit). Bump in lockstep with that pin.
	BGEModelRevision = "5617a9f"
	// BGETokenizerRevision is the tokenizer.json revision. BGE-M3 ships its
	// tokenizer in the SAME repo/commit as the weights, so it tracks the model
	// commit; kept separate because a future split (different tokenizer commit)
	// must still flip the identity on its own.
	BGETokenizerRevision = "5617a9f"

	// bgeModelName is the human-readable model family fed into the identity.
	bgeModelName = "bge-m3"
	// bgeNormalization records that the backend L2-normalises every vector
	// (l2normalize in embed_onnx.go); a switch to raw/none must flip the identity.
	bgeNormalization = "l2"
	// bgePrecision is the numeric precision of the produced vectors. The CPU ONNX
	// path runs fp32; a GPU fp16 run produces slightly different vectors (plan
	// PR-11 fp16 caveat), so precision is an identity component — fp16-GPU and
	// fp32-CPU vectors must never share one HNSW.
	bgePrecision = "fp32"
)

// BGEEmbedParts is the canonical EmbedParts for the production BGE-M3 backend.
// model.EmbedIdentity(BGEEmbedParts()) is the value stamped into DB meta
// (embedding_model), folded into ClauseHash, and compared at serve time, so a
// change in ANY component (weights, tokenizer, dim, normalisation, precision)
// re-embeds and refuses a mismatched corpus instead of mixing model revisions.
func BGEEmbedParts() model.EmbedParts {
	return model.EmbedParts{
		ModelID:           bgeModelName,
		ModelRevision:     BGEModelRevision,
		TokenizerRevision: BGETokenizerRevision,
		VectorDim:         strconv.Itoa(Dim),
		NormalizationMode: bgeNormalization,
		Precision:         bgePrecision,
	}
}

// bgeModelID is the canonical identity STRING for the BGE-M3 backend: the digest
// over BGEEmbedParts. It is what ModelID() returns, so DB meta, ClauseHash and
// the serve-time coherence guard all key on the SAME value — a published DB whose
// embedding_model differs (older weights, different precision/dim) is refused at
// serve and forced to re-embed by discover, never scored against a fresh query.
func bgeModelID() string { return model.EmbedIdentity(BGEEmbedParts()) }

// ResolveModelID maps an embedder FAMILY name (as passed by CI, e.g. EMBEDDER /
// the discover --embed-model flag) to the canonical ModelID() that backend would
// report. CGO-free: discover (no ONNX) needs the BGE-M3 identity to compare the
// published EmbedIdentity against the current code WITHOUT instantiating the real
// embedder. Keeps the family→identity mapping single-sourced with the embedder's
// own ModelID(), so the auto-delta model-drift detection (finding
// `model-change-needs-full-flag-not-auto-detected`) can't diverge from serve.
//
//	"bge-m3"     -> the full BGE-M3 EmbedIdentity digest (onnxEmbedder.ModelID)
//	"local"/"hash"/"hash-local" -> "hash-local" (Local.ModelID)
//	"" / "off" / "none" / "disabled" -> "" (Disabled.ModelID)
func ResolveModelID(family string) string {
	switch family {
	case "bge-m3", "bge_m3", "bgem3":
		return bgeModelID()
	case "local", "hash", "hash-local":
		return Local{}.ModelID()
	default:
		return "" // lexical / disabled / unknown -> no embed identity
	}
}
