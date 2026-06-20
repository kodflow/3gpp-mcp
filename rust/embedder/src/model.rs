//! BGE-M3 dense embedding via ONNX Runtime (ort) + HF tokenizers.
//!
//! fastembed 4.9.1 ships no BGE-M3 and cannot load its >2GB external-data ONNX from a
//! single buffer, so we drive ORT directly: `commit_from_file` loads model.onnx and
//! ORT reads the sibling `model.onnx_data` automatically. We tokenise with the model's
//! own tokenizer.json (XLM-RoBERTa), run the forward pass, take the CLS token of the
//! last hidden state (BGE-M3's dense head) and L2-normalise.
//!
//! Execution providers are tried CUDA→CPU: ORT skips an EP that fails to register, so
//! the same binary uses the GPU on Kaggle (T4) and falls back to CPU on CI/local. The
//! CUDA arena uses `kSameAsRequested` so it grows by exactly what each batch needs
//! instead of doubling (the log showed it leaping to 2 GB+ chunks), which both wastes
//! VRAM and makes peak memory unpredictable for the dynamic batcher.
//!
//! ## Tokenise once, embed from ids
//!
//! `encode` returns the truncated token-id rows for a slice of texts; `embed_ids` runs
//! a forward pass on pre-tokenised rows. Splitting the two lets `main.rs` tokenise the
//! whole work-list once (to length-sort it and size batches by token budget) without
//! re-tokenising at inference time.

use std::path::Path;

use anyhow::{anyhow, Context, Result};
use ndarray::{Array2, Axis, Ix2, Ix3};
use ort::execution_providers::{ArenaExtendStrategy, CPUExecutionProvider, CUDAExecutionProvider};
use ort::session::{builder::GraphOptimizationLevel, Session};
use ort::value::Tensor;
use tokenizers::{Tokenizer, TruncationParams};

/// Tokenizer truncation length and long-clause strategy = truncate (embed only the
/// first MAX_TOKENS tokens). BGE-M3 supports 8192, but full self-attention at
/// batch×seq² blows up GPU memory; 1024 keeps the attention buffer bounded and captures
/// the discriminative head of a clause.
///
/// MAX_TOKENS (1024) and the truncate strategy are EmbedIdentity components on the Go
/// side (model.EmbedParts.MaxTokens / .Windowing, default 1024 / "truncate"). This
/// value MUST equal the Go registry's max_tokens (embed.DefaultMaxTokens) — they are
/// kept in lockstep so the identity Go hands us via --embed-identity matches what we
/// actually do. Changing it here REQUIRES bumping the Go registry (and forces a clean
/// re-embed via the identity change). The mean_pool long-clause strategy (better recall
/// on long normative clauses) is the urgent Rust port tracked in issue #208; until it
/// lands the canonical corpus config is truncate@1024.
//
// TODO(#208): port internal/embed/window.go mean_pool (window ≤300 words, embed each,
// mean-pool + L2) here, then flip the registry windowing to mean_pool (identity bump
// ⇒ clean re-embed). MUST be GPU-validated for Go↔Rust parity before any 31-series bake.
pub const MAX_TOKENS: usize = 1024;

/// DENSE_OUTPUT is the declared name of BGE-M3's dense (sentence-embedding) head —
/// the value the Go model registry declares (`output: sentence_embedding`). We bind
/// the dense output BY NAME, not by index 0: a dual-head export (dense +
/// `sparse_weights`, see issue #208) has more than one output, and there is no
/// guarantee the dense head is index 0 — reading `outputs[0]` blind would then
/// silently return the wrong tensor. Falls back to index 0 only when no output
/// carries this name (a pooled-less export).
const DENSE_OUTPUT: &str = "sentence_embedding";

/// Bge wraps a committed ORT session + tokenizer for repeated batch embedding.
pub struct Bge {
    session: Session,
    tokenizer: Tokenizer,
    needs_token_type: bool,
    /// Name of the dense output tensor to read, resolved at load (DENSE_OUTPUT if the
    /// graph declares it, else the first output) — bound by name, never by index.
    dense_output: String,
}

impl Bge {
    /// load commits the ONNX session (CUDA→CPU) and loads the tokenizer. `model_onnx`
    /// must sit next to its external-data file (model.onnx_data) — ORT finds it by the
    /// relative path baked into the graph.
    pub fn load(
        model_onnx: &Path,
        tokenizer_json: &Path,
        require_cuda: bool,
        device_id: i32,
    ) -> Result<Self> {
        // CUDA first, CPU fallback. `kSameAsRequested` stops the arena from doubling
        // (predictable peak for the dynamic batcher). `device_id` selects the GPU so the
        // multi-GPU launcher runs one process per card. With require_cuda the CUDA EP is
        // set to error_on_failure so a misconfigured GPU runtime is a LOUD error rather
        // than a silent ~13 clause/s CPU run.
        let cuda = CUDAExecutionProvider::default()
            .with_device_id(device_id)
            .with_arena_extend_strategy(ArenaExtendStrategy::SameAsRequested);
        let cuda = if require_cuda {
            cuda.build().error_on_failure()
        } else {
            cuda.build()
        };
        let session = Session::builder()?
            .with_execution_providers([cuda, CPUExecutionProvider::default().build()])?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(model_onnx)
            .with_context(|| format!("commit onnx {model_onnx:?}"))?;
        let needs_token_type = session.inputs.iter().any(|i| i.name == "token_type_ids");
        // Bind the dense output by its declared name; fall back to the first output
        // only if the graph has no `sentence_embedding` node (a pooled-less export).
        let dense_output = session
            .outputs
            .iter()
            .find(|o| o.name == DENSE_OUTPUT)
            .map(|o| o.name.clone())
            .unwrap_or_else(|| session.outputs[0].name.clone());
        let mut tokenizer =
            Tokenizer::from_file(tokenizer_json).map_err(|e| anyhow!("load tokenizer: {e}"))?;
        // TRUNCATE to MAX_TOKENS so a long clause never tokenises past the model's
        // position-embedding table (the Expand-node broadcast crash).
        tokenizer
            .with_truncation(Some(TruncationParams {
                max_length: MAX_TOKENS,
                ..Default::default()
            }))
            .map_err(|e| anyhow!("set truncation: {e}"))?;
        Ok(Self {
            session,
            tokenizer,
            needs_token_type,
            dense_output,
        })
    }

    /// encode tokenises a slice of texts (parallel, truncated to MAX_TOKENS) and returns
    /// the token-id rows. Done once up front so the work-list can be length-sorted and
    /// batched by token budget without re-tokenising at inference time.
    pub fn encode(&self, texts: &[String]) -> Result<Vec<Vec<i64>>> {
        let encs = self
            .tokenizer
            .encode_batch(texts.to_vec(), true)
            .map_err(|e| anyhow!("encode_batch: {e}"))?;
        Ok(encs
            .iter()
            .map(|e| e.get_ids().iter().map(|&id| id as i64).collect())
            .collect())
    }

    /// embed_ids runs a forward pass on pre-tokenised id rows, padding to the batch's
    /// longest row, and returns one dense (L2-normalised) vector per row. Generic over
    /// the row type (`AsRef<[i64]>`) so the caller can pass its own records without
    /// cloning the id vectors per batch.
    pub fn embed_ids<R: AsRef<[i64]>>(&self, rows: &[R]) -> Result<Vec<Vec<f32>>> {
        let bsz = rows.len();
        let seq = rows
            .iter()
            .map(|r| r.as_ref().len())
            .max()
            .unwrap_or(1)
            .max(1);

        let mut ids = Array2::<i64>::zeros((bsz, seq));
        let mut mask = Array2::<i64>::zeros((bsz, seq));
        for (i, r) in rows.iter().enumerate() {
            for (j, &id) in r.as_ref().iter().enumerate() {
                ids[[i, j]] = id;
                mask[[i, j]] = 1;
            }
        }

        let mut inputs = ort::inputs![
            "input_ids" => Tensor::from_array(ids)?,
            "attention_mask" => Tensor::from_array(mask)?,
        ]?;
        if self.needs_token_type {
            let tt = Array2::<i64>::zeros((bsz, seq));
            inputs.push(("token_type_ids".into(), Tensor::from_array(tt)?.into()));
        }

        let outputs = self.session.run(inputs)?;
        // Read the dense head BY NAME (resolved at load), never by index 0 — a
        // dual-head export must not silently return the wrong tensor (issue #208).
        let view = outputs[self.dense_output.as_str()].try_extract_tensor::<f32>()?;
        let shape = view.shape().to_vec();

        let mut out = Vec::with_capacity(bsz);
        match shape.len() {
            // [batch, seq, hidden] → CLS token (index 0) is BGE-M3's dense head.
            3 => {
                let v = view.into_dimensionality::<Ix3>()?;
                for i in 0..bsz {
                    // CLS row = v[i, 0, ..]; copy the contiguous hidden vector at once.
                    let mut row: Vec<f32> =
                        v.index_axis(Axis(0), i).index_axis(Axis(0), 0).to_vec();
                    l2_normalize(&mut row);
                    out.push(row);
                }
            }
            // [batch, hidden] → already pooled; just normalise.
            2 => {
                let v = view.into_dimensionality::<Ix2>()?;
                for i in 0..bsz {
                    let mut row: Vec<f32> = v.index_axis(Axis(0), i).to_vec();
                    l2_normalize(&mut row);
                    out.push(row);
                }
            }
            n => {
                return Err(anyhow!(
                    "unexpected model output rank {n} (shape {shape:?})"
                ))
            }
        }
        Ok(out)
    }
}

/// l2_normalize scales a vector to unit length in place (cosine-ready). A zero vector
/// is left untouched (avoids NaN).
pub fn l2_normalize(v: &mut [f32]) {
    let norm: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 0.0 {
        for x in v.iter_mut() {
            *x /= norm;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn l2_normalize_unit_length() {
        let mut v = vec![3.0f32, 4.0];
        l2_normalize(&mut v);
        let n: f32 = v.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((n - 1.0).abs() < 1e-6);
        assert!((v[0] - 0.6).abs() < 1e-6 && (v[1] - 0.8).abs() < 1e-6);
    }

    #[test]
    fn l2_normalize_zero_is_safe() {
        let mut v = vec![0.0f32, 0.0];
        l2_normalize(&mut v);
        assert_eq!(v, vec![0.0, 0.0]);
    }
}
