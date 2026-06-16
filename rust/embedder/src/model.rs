//! BGE-M3 dense embedding via ONNX Runtime (ort) + HF tokenizers.
//!
//! fastembed 4.9.1 ships no BGE-M3 and cannot load its >2GB external-data ONNX from a
//! single buffer, so we drive ORT directly: `commit_from_file` loads model.onnx and
//! ORT reads the sibling `model.onnx_data` automatically. We tokenise with the model's
//! own tokenizer.json (XLM-RoBERTa), run the forward pass, take the CLS token of the
//! last hidden state (BGE-M3's dense head) and L2-normalise.
//!
//! Execution providers are tried CUDA→CPU: ORT skips an EP that fails to register, so
//! the same binary uses the GPU on Kaggle (T4) and falls back to CPU on CI/local.

use std::path::Path;

use anyhow::{anyhow, Context, Result};
use ndarray::{Array2, Ix2, Ix3};
use ort::execution_providers::{CPUExecutionProvider, CUDAExecutionProvider};
use ort::session::{builder::GraphOptimizationLevel, Session};
use ort::value::Tensor;
use tokenizers::{Tokenizer, TruncationParams};

/// BGE-M3 max sequence length (its position-embedding table is 8194 = 8192 + 2 special).
const MAX_TOKENS: usize = 8192;

/// Bge wraps a committed ORT session + tokenizer for repeated batch embedding.
pub struct Bge {
    session: Session,
    tokenizer: Tokenizer,
    needs_token_type: bool,
}

impl Bge {
    /// load commits the ONNX session (CUDA→CPU) and loads the tokenizer. `model_onnx`
    /// must sit next to its external-data file (model.onnx_data) — ORT finds it by the
    /// relative path baked into the graph.
    pub fn load(model_onnx: &Path, tokenizer_json: &Path) -> Result<Self> {
        let session = Session::builder()?
            .with_execution_providers([
                CUDAExecutionProvider::default().build(),
                CPUExecutionProvider::default().build(),
            ])?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            .commit_from_file(model_onnx)
            .with_context(|| format!("commit onnx {model_onnx:?}"))?;
        let needs_token_type = session.inputs.iter().any(|i| i.name == "token_type_ids");
        let mut tokenizer =
            Tokenizer::from_file(tokenizer_json).map_err(|e| anyhow!("load tokenizer: {e}"))?;
        // TRUNCATE to BGE-M3's max (8192). Without this a long clause tokenises past the
        // model's position-embedding table (8194) and the graph's Expand node fails:
        // "left operand cannot broadcast … LeftShape {1,8194} RightShape {64,10493}".
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
        })
    }

    /// embed_batch returns one dense (L2-normalised) vector per input text.
    pub fn embed_batch(&self, texts: &[String]) -> Result<Vec<Vec<f32>>> {
        let encs = self
            .tokenizer
            .encode_batch(texts.to_vec(), true)
            .map_err(|e| anyhow!("encode_batch: {e}"))?;
        let bsz = encs.len();
        let seq = encs
            .iter()
            .map(|e| e.get_ids().len())
            .max()
            .unwrap_or(1)
            .max(1);

        let mut ids = Array2::<i64>::zeros((bsz, seq));
        let mut mask = Array2::<i64>::zeros((bsz, seq));
        for (i, e) in encs.iter().enumerate() {
            for (j, (&id, &m)) in e.get_ids().iter().zip(e.get_attention_mask()).enumerate() {
                ids[[i, j]] = id as i64;
                mask[[i, j]] = m as i64;
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
        let out_name = self.session.outputs[0].name.clone();
        let view = outputs[out_name.as_str()].try_extract_tensor::<f32>()?;
        let shape = view.shape().to_vec();

        let mut out = Vec::with_capacity(bsz);
        match shape.len() {
            // [batch, seq, hidden] → CLS token (index 0) is BGE-M3's dense head.
            3 => {
                let hidden = shape[2];
                let v = view.into_dimensionality::<Ix3>()?;
                for i in 0..bsz {
                    let mut row = vec![0f32; hidden];
                    for (h, slot) in row.iter_mut().enumerate() {
                        *slot = v[[i, 0, h]];
                    }
                    l2_normalize(&mut row);
                    out.push(row);
                }
            }
            // [batch, hidden] → already pooled; just normalise.
            2 => {
                let hidden = shape[1];
                let v = view.into_dimensionality::<Ix2>()?;
                for i in 0..bsz {
                    let mut row = vec![0f32; hidden];
                    for (h, slot) in row.iter_mut().enumerate() {
                        *slot = v[[i, h]];
                    }
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
