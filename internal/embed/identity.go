package embed

import (
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
)

// PrecisionFP32 / PrecisionFP16 are the two supported numeric precisions of the
// produced vectors. Precision is an EmbedIdentity component because fp16 vectors
// differ slightly from fp32 — so an fp16 corpus and an fp32 query (or two shards of
// different precision) must NEVER share one HNSW. The flip is what the serve guard
// and the re-embed gate key on.
const (
	PrecisionFP32 = "fp32"
	PrecisionFP16 = "fp16"
)

// BGEEmbedParts is the canonical EmbedParts for the ACTIVE model in the registry
// (default: the pinned fp32 BGE-M3). model.EmbedIdentity(BGEEmbedParts()) is the
// value stamped into DB meta (embedding_model), folded into ClauseHash, and compared
// at serve time, so swapping the active model OR its precision (via the registry /
// EMBED_MODEL) re-embeds and refuses a mismatched corpus instead of mixing models.
// It reads the registry (CGO-free), so discover and the ONNX backend agree.
func BGEEmbedParts() model.EmbedParts {
	return ActiveModel().embedParts()
}

// bgeModelID is the canonical identity STRING for the BGE-M3 backend: the digest
// over BGEEmbedParts. It is what ModelID() returns, so DB meta, ClauseHash and
// the serve-time coherence guard all key on the SAME value — a published DB whose
// embedding_model differs (older weights, different precision/dim) is refused at
// serve and forced to re-embed by discover, never scored against a fresh query.
func bgeModelID() string { return model.EmbedIdentity(BGEEmbedParts()) }

// BGESparseParts is the canonical SparseParts for the ACTIVE registry model. It is
// non-empty only when that model declares a sparse head (sparse_output), so
// SparseModelID() returns "" for the current dense-only export and a stable digest
// once a sparse-exported model is made active.
func BGESparseParts() model.SparseParts {
	m := ActiveModel()
	return model.SparseParts{
		ModelID:           m.Family,
		ModelRevision:     m.Revision,
		TokenizerRevision: m.TokenizerRevision,
		SparseOutput:      m.SparseOutput,
	}
}

// SparseModelID is the canonical sparse identity for the active registry model
// ("" when the model has no sparse head). It is what gets stamped into DB meta
// (sparse_model) by `embed --sparse-only` and compared at bake/serve time, so a
// sparse model swap is detected and re-run WITHOUT touching the dense vectors.
// CGO-free (reads the registry), so discover / embedid / the onnx backend agree.
func SparseModelID() string { return model.SparseIdentity(BGESparseParts()) }

// SparseCapable reports whether the ACTIVE model declares a sparse head — i.e.
// whether this build can produce/serve sparse postings at all. False for the
// current dense-only export.
func SparseCapable() bool { return ActiveModel().SparseOutput != "" }

// ResolveSparseID maps an embedder FAMILY to the sparse identity that family would
// stamp ("" when not sparse-capable). Mirrors ResolveModelID for the sparse arm so
// the bake's self-heal check ("is the served sparse layer the expected one?") is
// single-sourced with what the embedder writes.
func ResolveSparseID(family string) string {
	switch family {
	case "bge-m3", "bge_m3", "bgem3":
		return SparseModelID()
	case "local", "hash", "hash-local":
		return Local{}.SparseModelID()
	default:
		return ""
	}
}

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
