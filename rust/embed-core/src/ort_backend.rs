//! ort_backend — the REAL BGE-M3 ONNX inference behind embed-core's C ABI (feature `ort`).
//! A single-query, serve-side mirror of rust/embedder/src/model.rs: same MAX_TOKENS=1024
//! truncate strategy, same dense-output-BY-NAME binding (`sentence_embedding`, never index 0),
//! same CLS-vs-pooled handling, same L2-normalise — so a query vector is bit-identical to the
//! corpus vector the embedder wrote (cosine-comparable). The session is lazy-initialised once
//! from $EMBED_MODEL_DIR (model.onnx + tokenizer.json), CPU by default (one query is cheap);
//! the `cuda` feature adds the GPU EP for batch/bench boxes.

use super::DENSE_DIM;
use anyhow::{anyhow, Context, Result};
use ndarray::{Array2, Axis, Ix2, Ix3};
use ort::session::{builder::GraphOptimizationLevel, Session};
use ort::value::Tensor;
use std::path::PathBuf;
use std::sync::OnceLock;
use tokenizers::{Tokenizer, TruncationParams};

/// Same identity-bound truncation length as the corpus embedder (must stay in lockstep).
const MAX_TOKENS: usize = 1024;
/// Dense head declared name (bound by name, never index 0 — dual-head export safety).
const DENSE_OUTPUT: &str = "sentence_embedding";

struct Model {
    session: Session,
    tokenizer: Tokenizer,
    needs_token_type: bool,
    dense_output: String,
}

static MODEL: OnceLock<Result<Model, String>> = OnceLock::new();

fn model() -> &'static Result<Model, String> {
    MODEL.get_or_init(|| load().map_err(|e| format!("{e:#}")))
}

fn load() -> Result<Model> {
    let dir = std::env::var("EMBED_MODEL_DIR").unwrap_or_else(|_| "data/models/bge-m3".to_string());
    let dir = PathBuf::from(dir);
    let model_onnx = dir.join("model.onnx");
    let tokenizer_json = dir.join("tokenizer.json");

    let session = Session::builder()?
        .with_optimization_level(GraphOptimizationLevel::Level3)?
        .commit_from_file(&model_onnx)
        .with_context(|| format!("commit onnx {model_onnx:?}"))?;
    let needs_token_type = session.inputs.iter().any(|i| i.name == "token_type_ids");
    let dense_output = session
        .outputs
        .iter()
        .find(|o| o.name == DENSE_OUTPUT)
        .map(|o| o.name.clone())
        .unwrap_or_else(|| session.outputs[0].name.clone());
    let mut tokenizer =
        Tokenizer::from_file(&tokenizer_json).map_err(|e| anyhow!("load tokenizer: {e}"))?;
    tokenizer
        .with_truncation(Some(TruncationParams {
            max_length: MAX_TOKENS,
            ..Default::default()
        }))
        .map_err(|e| anyhow!("set truncation: {e}"))?;
    Ok(Model {
        session,
        tokenizer,
        needs_token_type,
        dense_output,
    })
}

/// embed_one runs a single-query forward pass and returns the L2-normalised dense vector.
/// None on any failure (model load, tokenise, forward) — the caller maps it to a C-ABI error.
pub fn embed_one(text: &str) -> Option<[f32; DENSE_DIM]> {
    match embed_one_res(text) {
        Ok(v) => Some(v),
        Err(e) => {
            eprintln!("embed-core: embed failed: {e:#}");
            None
        }
    }
}

fn embed_one_res(text: &str) -> Result<[f32; DENSE_DIM]> {
    let m = model().as_ref().map_err(|e| anyhow!("model load: {e}"))?;
    let enc = m
        .tokenizer
        .encode(text, true)
        .map_err(|e| anyhow!("encode: {e}"))?;
    let ids: Vec<i64> = enc.get_ids().iter().map(|&id| id as i64).collect();
    let seq = ids.len().max(1);

    let mut id_arr = Array2::<i64>::zeros((1, seq));
    let mut mask = Array2::<i64>::zeros((1, seq));
    for (j, &id) in ids.iter().enumerate() {
        id_arr[[0, j]] = id;
        mask[[0, j]] = 1;
    }
    let mut inputs = ort::inputs![
        "input_ids" => Tensor::from_array(id_arr)?,
        "attention_mask" => Tensor::from_array(mask)?,
    ]?;
    if m.needs_token_type {
        let tt = Array2::<i64>::zeros((1, seq));
        inputs.push(("token_type_ids".into(), Tensor::from_array(tt)?.into()));
    }

    let outputs = m.session.run(inputs)?;
    let view = outputs[m.dense_output.as_str()].try_extract_tensor::<f32>()?;
    let shape = view.shape().to_vec();
    let mut row: Vec<f32> = match shape.len() {
        3 => view
            .into_dimensionality::<Ix3>()?
            .index_axis(Axis(0), 0)
            .index_axis(Axis(0), 0)
            .to_vec(),
        2 => view
            .into_dimensionality::<Ix2>()?
            .index_axis(Axis(0), 0)
            .to_vec(),
        n => return Err(anyhow!("unexpected output rank {n} (shape {shape:?})")),
    };
    l2_normalize(&mut row);
    if row.len() < DENSE_DIM {
        return Err(anyhow!("output dim {} < {DENSE_DIM}", row.len()));
    }
    let mut out = [0f32; DENSE_DIM];
    out.copy_from_slice(&row[..DENSE_DIM]);
    Ok(out)
}

fn l2_normalize(v: &mut [f32]) {
    let n = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if n > 0.0 {
        for x in v {
            *x /= n;
        }
    }
}
