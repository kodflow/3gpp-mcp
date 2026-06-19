//! embed-core — the shared dense embedder exposed over a minimal C ABI (cdylib) so the Go
//! serve can query-embed through the SAME Rust code path the corpus pipeline uses, instead
//! of the Go ONNX seam (Phase 1: resolve the internal/embed write/read coupling).
//!
//! C ABI (stable, three functions):
//!   int  embed_core_dim(void);                          // → 1024
//!   int  embed_core_embed(const char* utf8,             // NUL-terminated query text
//!                         float* out, int out_len);      // out[0..1024) filled; 0 ok, <0 err
//!   const char* embed_core_backend(void);               // "hash" | "bge-m3-onnx" (diagnostics)
//!
//! The baseline backend is a DETERMINISTIC, L2-normalised hash embedding (no model, no GPU),
//! so the cdylib → cgo → Go seam is testable anywhere. The real BGE-M3 ONNX inference plugs
//! into the SAME three symbols behind `--features ort` (CI/infra, with the model present).

use std::ffi::CStr;
use std::os::raw::{c_char, c_int};

/// DENSE_DIM is BGE-M3's output width; the contract the Go side allocates for.
pub const DENSE_DIM: usize = 1024;

/// embed_core_dim returns the dense dimensionality the caller must allocate (== 1024).
#[no_mangle]
pub extern "C" fn embed_core_dim() -> c_int {
    DENSE_DIM as c_int
}

/// embed_core_backend names the active inference backend (diagnostics / coherence).
#[no_mangle]
pub extern "C" fn embed_core_backend() -> *const c_char {
    // Static NUL-terminated; valid for the program lifetime.
    if cfg!(feature = "ort") {
        c"bge-m3-onnx".as_ptr()
    } else {
        c"hash".as_ptr()
    }
}

#[cfg(feature = "ort")]
mod ort_backend;

/// embed_core_embed writes the query's dense vector into `out[0..out_len)`.
/// Returns 0 on success, -1 on a null/short buffer, -2 on invalid UTF-8, -3 if the backend
/// (ort) failed to produce a vector (e.g. model not loadable).
///
/// # Safety
/// `text` must be a valid NUL-terminated C string; `out` must point to at least `out_len`
/// writable `f32`s. The caller (Go cgo) owns both buffers.
#[no_mangle]
pub extern "C" fn embed_core_embed(text: *const c_char, out: *mut f32, out_len: c_int) -> c_int {
    if text.is_null() || out.is_null() || (out_len as usize) < DENSE_DIM {
        return -1;
    }
    let s = match unsafe { CStr::from_ptr(text) }.to_str() {
        Ok(s) => s,
        Err(_) => return -2,
    };
    match backend_embed(s) {
        Some(v) => {
            unsafe {
                std::ptr::copy_nonoverlapping(v.as_ptr(), out, DENSE_DIM);
            }
            0
        }
        None => -3,
    }
}

/// backend_embed routes to the active backend: the deterministic hash baseline (default) or
/// the real BGE-M3 ONNX forward pass (`--features ort`). None ⇒ the backend could not embed.
fn backend_embed(text: &str) -> Option<[f32; DENSE_DIM]> {
    #[cfg(feature = "ort")]
    {
        ort_backend::embed_one(text)
    }
    #[cfg(not(feature = "ort"))]
    {
        Some(embed(text))
    }
}

/// embed is the deterministic hash baseline (non-ort builds + unit tests): a sha256-seeded,
/// L2-normalised expansion. Same query → same vector; different queries → different vectors.
#[cfg(not(feature = "ort"))]
pub fn embed(text: &str) -> [f32; DENSE_DIM] {
    hash_embed(text)
}

/// hash_embed expands `text` into a deterministic unit vector: sha256 the text, then stream
/// a counter-mode digest into DENSE_DIM floats in [-1,1], and L2-normalise. Same query →
/// same vector (so the FFI seam is exercisable with stable assertions), different queries →
/// different vectors with overwhelming probability.
#[cfg(not(feature = "ort"))]
fn hash_embed(text: &str) -> [f32; DENSE_DIM] {
    use sha2::{Digest, Sha256};
    let mut v = [0f32; DENSE_DIM];
    let mut i = 0usize;
    let mut ctr: u64 = 0;
    while i < DENSE_DIM {
        let mut h = Sha256::new();
        h.update(text.as_bytes());
        h.update(ctr.to_le_bytes());
        let d = h.finalize(); // 32 bytes → 8 f32
        for chunk in d.chunks_exact(4) {
            if i >= DENSE_DIM {
                break;
            }
            let u = u32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]);
            // map to [-1, 1)
            v[i] = (u as f32 / u32::MAX as f32) * 2.0 - 1.0;
            i += 1;
        }
        ctr += 1;
    }
    let norm = v.iter().map(|x| x * x).sum::<f32>().sqrt();
    if norm > 0.0 {
        for x in &mut v {
            *x /= norm;
        }
    }
    v
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dim_is_1024() {
        assert_eq!(embed_core_dim(), 1024);
    }

    #[test]
    fn embed_is_deterministic_and_normalised() {
        let a = embed("AMF registration procedure");
        let b = embed("AMF registration procedure");
        assert_eq!(a, b, "same text → same vector");
        let n: f32 = a.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((n - 1.0).abs() < 1e-4, "L2-normalised (got {n})");
        let c = embed("a completely different query");
        assert!(a != c, "different text → different vector");
    }

    #[test]
    fn c_abi_fills_the_buffer() {
        let mut out = [0f32; DENSE_DIM];
        let txt = std::ffi::CString::new("hello").unwrap();
        let rc = embed_core_embed(txt.as_ptr(), out.as_mut_ptr(), DENSE_DIM as c_int);
        assert_eq!(rc, 0);
        let n: f32 = out.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((n - 1.0).abs() < 1e-4);
        // short buffer rejected
        assert_eq!(embed_core_embed(txt.as_ptr(), out.as_mut_ptr(), 10), -1);
    }
}
