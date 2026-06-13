package model

// SparseVec is a BGE-M3 learned-lexical (sparse) embedding: a map of XLM-RoBERTa
// vocab token id → non-negative weight. It is the post-processed form (per-token-id
// max, special tokens + non-positive weights dropped — see the embed seam), NOT the
// raw per-position model output.
//
// It lives in `model` (not `embed` or `store`) so the embed seam, the store, and the
// search engine can all reference it without an import cycle — exactly like the dense
// []float32 path. The lexical-match score between a query and a clause is the dot
// product over shared token ids: Σ qw[t]·dw[t] (FlagEmbedding compute_lexical_matching_score;
// no normalization at index or query time).
type SparseVec map[uint32]float32
