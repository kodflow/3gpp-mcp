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

/// Sparse (learned-lexical) head output name, set by the dual-head export
/// (scripts/export-bge-m3-sparse.py). Absent on the dense-only model → no sparse arm.
const SPARSE_OUTPUT: &str = "sparse_weights";
/// Per-run sparse sequence cap (== Go maxSparseSeq default): BGE-M3 self-attention is
/// O(seq²); window longer inputs and merge term weights (max per id).
const SPARSE_MAX_SEQ: usize = 512;

struct Model {
    session: Session,
    tokenizer: Tokenizer,
    needs_token_type: bool,
    dense_output: String,
    /// Some(name) when the model was exported with the sparse head; None on dense-only.
    sparse_output: Option<String>,
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

    #[allow(unused_mut)]
    let mut builder =
        Session::builder()?.with_optimization_level(GraphOptimizationLevel::Level3)?;
    // Register the CUDA execution provider when built with --features cuda (bulk embed on a
    // GPU box). Without this the session is CPU-only — the bulk sparse pass crawled at ~0.5
    // clause/s (a GPU forward is ~100x faster). Best-effort: ort falls back to CPU if CUDA
    // can't initialise, so this never breaks the CPU serve path.
    #[cfg(feature = "cuda")]
    {
        use ort::execution_providers::CUDAExecutionProvider;
        builder = builder.with_execution_providers([CUDAExecutionProvider::default().build()])?;
    }
    let session = builder
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
    let sparse_output = session
        .outputs
        .iter()
        .find(|o| o.name == SPARSE_OUTPUT)
        .map(|o| o.name.clone());
    Ok(Model {
        session,
        tokenizer,
        needs_token_type,
        dense_output,
        sparse_output,
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

/// has_sparse reports whether the loaded model carries the sparse head.
pub fn has_sparse() -> bool {
    matches!(model(), Ok(m) if m.sparse_output.is_some())
}

/// embed_sparse_one returns the query's deduped sparse term→weight pairs (== Go EmbedSparse):
/// window the ids into ≤SPARSE_MAX_SEQ chunks, run the sparse head, keep the MAX ReLU weight
/// per token id, drop the 4 special ids (cls/pad/eos/unk) + non-positive weights, merge windows
/// (max per id). None on any failure / no sparse head. Sorted by descending weight.
pub fn embed_sparse_one(text: &str) -> Option<Vec<(u32, f32)>> {
    match embed_sparse_res(text) {
        Ok(v) => Some(v),
        Err(e) => {
            eprintln!("embed-core: sparse embed failed: {e:#}");
            None
        }
    }
}

fn embed_sparse_res(text: &str) -> Result<Vec<(u32, f32)>> {
    let m = model().as_ref().map_err(|e| anyhow!("model load: {e}"))?;
    let sparse_out = m
        .sparse_output
        .as_deref()
        .ok_or_else(|| anyhow!("model has no sparse head ({SPARSE_OUTPUT})"))?;
    let enc = m
        .tokenizer
        .encode(text, true)
        .map_err(|e| anyhow!("encode: {e}"))?;
    let ids: Vec<u32> = enc.get_ids().to_vec();
    if ids.is_empty() {
        return Ok(Vec::new());
    }
    // specials dropped from the sparse representation (== Go default cls/pad/eos/unk).
    let special = |id: u32| matches!(id, 0 | 1 | 2 | 3);
    let mut merged: std::collections::HashMap<u32, f32> = std::collections::HashMap::new();
    let mut start = 0usize;
    while start < ids.len() {
        let end = (start + SPARSE_MAX_SEQ).min(ids.len());
        let chunk = &ids[start..end];
        let seq = chunk.len();
        let mut id_arr = Array2::<i64>::zeros((1, seq));
        let mut mask = Array2::<i64>::zeros((1, seq));
        for (j, &id) in chunk.iter().enumerate() {
            id_arr[[0, j]] = i64::from(id);
            mask[[0, j]] = 1;
        }
        let mut inputs = ort::inputs![
            "input_ids" => Tensor::from_array(id_arr)?,
            "attention_mask" => Tensor::from_array(mask)?,
        ]?;
        if m.needs_token_type {
            inputs.push((
                "token_type_ids".into(),
                Tensor::from_array(Array2::<i64>::zeros((1, seq)))?.into(),
            ));
        }
        let outputs = m.session.run(inputs)?;
        let view = outputs[sparse_out].try_extract_tensor::<f32>()?;
        // sparse_weights is [1, seq] ReLU weights, one per position.
        let w = view.into_dimensionality::<Ix2>()?;
        for (j, &id) in chunk.iter().enumerate() {
            if special(id) {
                continue;
            }
            let weight = w[[0, j]];
            if weight <= 0.0 {
                continue;
            }
            let e = merged.entry(id).or_insert(0.0);
            if weight > *e {
                *e = weight; // max per token id (== toSparse)
            }
        }
        start = end;
    }
    let mut out: Vec<(u32, f32)> = merged.into_iter().collect();
    out.sort_by(|a, b| b.1.total_cmp(&a.1));
    Ok(out)
}

/// embed_sparse_batch runs the sparse head on a BATCH of clauses per session.run (padded to
/// the batch's max length) — ~batch× faster than per-clause, which is too slow for the corpus
/// campaign. Clauses longer than SPARSE_MAX_SEQ fall back to the windowed per-clause path.
/// Returns one Option per input (None on a per-row failure), same extraction as
/// embed_sparse_one (specials dropped, weight>0, max-per-id, sorted desc).
pub fn embed_sparse_batch(texts: &[&str], batch: usize) -> Vec<Option<Vec<(u32, f32)>>> {
    match embed_sparse_batch_res(texts, batch.max(1)) {
        Ok(v) => v,
        Err(e) => {
            eprintln!("embed-core: sparse batch failed: {e:#}");
            texts.iter().map(|_| None).collect()
        }
    }
}

fn embed_sparse_batch_res(texts: &[&str], batch: usize) -> Result<Vec<Option<Vec<(u32, f32)>>>> {
    let m = model().as_ref().map_err(|e| anyhow!("model load: {e}"))?;
    let sparse_out = m
        .sparse_output
        .as_deref()
        .ok_or_else(|| anyhow!("model has no sparse head ({SPARSE_OUTPUT})"))?;
    let special = |id: u32| matches!(id, 0 | 1 | 2 | 3);

    let mut encoded: Vec<Vec<u32>> = Vec::with_capacity(texts.len());
    for t in texts {
        let enc = m
            .tokenizer
            .encode(*t, true)
            .map_err(|e| anyhow!("encode: {e}"))?;
        encoded.push(enc.get_ids().to_vec());
    }
    let mut out: Vec<Option<Vec<(u32, f32)>>> = vec![None; texts.len()];

    // Short clauses (one window) take the batched path; long ones fall back to the windowed
    // per-clause path (rare); empty → empty posting set.
    let mut short: Vec<usize> = Vec::new();
    for (i, ids) in encoded.iter().enumerate() {
        if ids.is_empty() {
            out[i] = Some(Vec::new());
        } else if ids.len() <= SPARSE_MAX_SEQ {
            short.push(i);
        } else {
            out[i] = Some(embed_sparse_res(texts[i])?);
        }
    }

    // Group into TOKEN-BUDGET sub-batches to bound the padded [count, maxseq] tensor: a
    // fixed count×512 OOMs the T4 (rc=137). Sort by length so similar-length clauses batch
    // with minimal padding; close a group when count×maxseq would exceed the budget (or the
    // count cap `batch`). The budget (≈16×512) keeps each forward well within 16 GB VRAM.
    const TOKEN_BUDGET: usize = 8192;
    short.sort_by_key(|&i| encoded[i].len());
    let mut groups: Vec<Vec<usize>> = Vec::new();
    let mut cur: Vec<usize> = Vec::new();
    let mut cur_max = 0usize;
    for &i in &short {
        let new_max = cur_max.max(encoded[i].len());
        if !cur.is_empty() && ((cur.len() + 1) * new_max > TOKEN_BUDGET || cur.len() >= batch) {
            groups.push(std::mem::take(&mut cur));
            cur_max = 0;
        }
        cur_max = cur_max.max(encoded[i].len());
        cur.push(i);
    }
    if !cur.is_empty() {
        groups.push(cur);
    }

    for chunk in &groups {
        let bsz = chunk.len();
        let maxseq = chunk.iter().map(|&i| encoded[i].len()).max().unwrap_or(0);
        if maxseq == 0 {
            continue;
        }
        let mut id_arr = Array2::<i64>::zeros((bsz, maxseq));
        let mut mask = Array2::<i64>::zeros((bsz, maxseq));
        for (r, &gi) in chunk.iter().enumerate() {
            for (j, &id) in encoded[gi].iter().enumerate() {
                id_arr[[r, j]] = i64::from(id);
                mask[[r, j]] = 1;
            }
        }
        let mut inputs = ort::inputs![
            "input_ids" => Tensor::from_array(id_arr)?,
            "attention_mask" => Tensor::from_array(mask)?,
        ]?;
        if m.needs_token_type {
            inputs.push((
                "token_type_ids".into(),
                Tensor::from_array(Array2::<i64>::zeros((bsz, maxseq)))?.into(),
            ));
        }
        let outputs = m.session.run(inputs)?;
        let view = outputs[sparse_out].try_extract_tensor::<f32>()?;
        let w = view.into_dimensionality::<Ix2>()?; // [bsz, maxseq]
        for (r, &gi) in chunk.iter().enumerate() {
            let mut merged: std::collections::HashMap<u32, f32> = std::collections::HashMap::new();
            for (j, &id) in encoded[gi].iter().enumerate() {
                if special(id) {
                    continue;
                }
                let weight = w[[r, j]];
                if weight <= 0.0 {
                    continue;
                }
                let e = merged.entry(id).or_insert(0.0);
                if weight > *e {
                    *e = weight;
                }
            }
            let mut v: Vec<(u32, f32)> = merged.into_iter().collect();
            v.sort_by(|a, b| b.1.total_cmp(&a.1));
            out[gi] = Some(v);
        }
    }
    Ok(out)
}
