//! GPU detection for VRAM-aware batch sizing.
//!
//! We query `nvidia-smi` once at startup — no extra crate and no link-time CUDA
//! dependency, so the binary still builds and runs on a CPU-only box. On any failure
//! (no GPU, nvidia-smi absent, parse error) `detect` returns `None` and the caller
//! falls back to a conservative CPU budget. The numbers feed the memory model in
//! `batch.rs`, which sizes each batch to the detected free VRAM instead of a fixed
//! clause count.

use std::process::Command;

/// GpuInfo is the device-0 VRAM picture used to size batches.
#[derive(Debug, Clone)]
pub struct GpuInfo {
    pub name: String,
    pub total_bytes: u64,
    pub free_bytes: u64,
}

/// detect queries `nvidia-smi` for device 0's total+free VRAM (MiB) and name. Returns
/// `None` on any failure so the caller can pick a CPU-safe default.
pub fn detect() -> Option<GpuInfo> {
    let out = Command::new("nvidia-smi")
        .args([
            "--query-gpu=memory.total,memory.free,name",
            "--format=csv,noheader,nounits",
            "--id=0",
        ])
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let text = String::from_utf8_lossy(&out.stdout);
    parse_nvidia_smi(&text)
}

/// parse_nvidia_smi pulls the first data row ("total, free, name") into a GpuInfo. Split
/// out so it is unit-testable without a GPU present.
pub fn parse_nvidia_smi(text: &str) -> Option<GpuInfo> {
    let first = text.lines().next()?.trim();
    let mut parts = first.split(',').map(|s| s.trim());
    let total_mib: u64 = parts.next()?.parse().ok()?;
    let free_mib: u64 = parts.next()?.parse().ok()?;
    let name = parts.next().unwrap_or("unknown").to_string();
    Some(GpuInfo {
        name,
        total_bytes: total_mib * 1024 * 1024,
        free_bytes: free_mib * 1024 * 1024,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_a_typical_row() {
        let g = parse_nvidia_smi("16384, 15109, Tesla T4\n").unwrap();
        assert_eq!(g.name, "Tesla T4");
        assert_eq!(g.total_bytes, 16384 * 1024 * 1024);
        assert_eq!(g.free_bytes, 15109 * 1024 * 1024);
    }

    #[test]
    fn name_with_commas_is_tolerated_as_unknown_tail() {
        // Names never contain commas in nvidia-smi CSV, but a missing name is safe.
        let g = parse_nvidia_smi("24576, 24000\n").unwrap();
        assert_eq!(g.name, "unknown");
        assert_eq!(g.total_bytes, 24576u64 * 1024 * 1024);
    }

    #[test]
    fn garbage_is_none() {
        assert!(parse_nvidia_smi("no gpu here").is_none());
        assert!(parse_nvidia_smi("").is_none());
    }
}
