//! embed-core-check — a tiny GPU/CPU smoke for the real BGE-M3 path (feature `ort`): embed
//! each CLI-arg query via embed_core's C ABI and print {dim, l2_norm, first-4 dims} as one
//! JSON line per query. Used by the Kaggle FFI-validation kernel to confirm the serve-side
//! Rust query-embed produces sane, normalised 1024-d vectors with the real model on a GPU.
//!
//! Usage: EMBED_MODEL_DIR=/path/to/bge-m3 embed-core-check "query one" "query two"
use std::ffi::CString;

fn main() {
    let dim = embed_core::embed_core_dim() as usize;
    let backend = unsafe { std::ffi::CStr::from_ptr(embed_core::embed_core_backend()) }
        .to_string_lossy()
        .into_owned();
    eprintln!("embed-core-check: backend={backend} dim={dim}");
    let queries: Vec<String> = std::env::args().skip(1).collect();
    let queries = if queries.is_empty() {
        vec![
            "AMF registration procedure".to_string(),
            "lawful interception X2 interface".to_string(),
        ]
    } else {
        queries
    };
    let mut ok = true;
    for q in &queries {
        let mut out = vec![0f32; dim];
        let c = CString::new(q.as_str()).unwrap();
        let rc = embed_core::embed_core_embed(c.as_ptr(), out.as_mut_ptr(), dim as i32);
        if rc != 0 {
            println!("{{\"query\":{q:?},\"rc\":{rc},\"ok\":false}}");
            ok = false;
            continue;
        }
        let norm: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
        // Sparse arm (when the dual-head model carries the sparse head): print the count +
        // top-3 (term_id, weight) so the FFI sparse path is visible.
        let mut sparse_json = String::from("null");
        if embed_core::embed_core_has_sparse() == 1 {
            let cap = 16384usize;
            let mut ids = vec![0u32; cap];
            let mut w = vec![0f32; cap];
            let n = embed_core::embed_core_embed_sparse(
                c.as_ptr(),
                ids.as_mut_ptr(),
                w.as_mut_ptr(),
                cap as i32,
            );
            if n < 0 {
                sparse_json = format!("{{\"rc\":{n}}}");
                ok = false;
            } else {
                let k = (n as usize).min(3);
                let top: Vec<String> = (0..k)
                    .map(|j| format!("[{},{:.4}]", ids[j as usize], w[j as usize]))
                    .collect();
                sparse_json = format!("{{\"terms\":{n},\"top3\":[{}]}}", top.join(","));
                if n == 0 {
                    ok = false; // a real query must yield some sparse terms
                }
            }
        }
        println!(
            "{{\"query\":{q:?},\"rc\":0,\"backend\":{backend:?},\"dim\":{dim},\"l2_norm\":{norm:.5},\"head\":[{:.5},{:.5},{:.5},{:.5}],\"sparse\":{sparse_json}}}",
            out[0], out[1], out[2], out[3]
        );
        eprintln!("[check] embedded {q:?} (dense l2={norm:.4}, sparse={sparse_json})");
        if (norm - 1.0).abs() > 1e-3 {
            ok = false;
        }
    }
    eprintln!(
        "[check] {} quer{} done, ok={ok}",
        queries.len(),
        if queries.len() == 1 { "y" } else { "ies" }
    );
    if !ok {
        std::process::exit(1);
    }
}
