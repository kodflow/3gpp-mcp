//! VRAM-aware dynamic batching — the core throughput optimisation.
//!
//! The work-list is length-sorted, then packed into batches whose SIZE adapts to each
//! batch's sequence length so peak GPU memory stays ~constant and the GPU stays
//! saturated across the whole length range. This replaces a fixed clause-count batch,
//! which is wrong at both ends: it underfills the GPU on the many short clauses (a
//! batch of 64 × ~20 tokens barely uses a T4) and OOMs / crawls on the few long ones
//! (64 × 1024 tokens blew the CUDA arena to ~8.5 GiB and dropped to ~12 clause/s).
//!
//! ## Memory model (attention-dominated, calibrated on a real run)
//!
//! Self-attention activation dominates GPU memory and scales as `batch · seq²`, so
//!   activation_bytes(batch, seq) ≈ K_ATTN · batch · seq².
//! For an available budget `A` bytes the largest safe batch at length `seq` is
//!   batch_max(seq) = clamp( A / (K_ATTN · seq²), 1, MAX_BATCH ).
//! Calibration point (Kaggle T4, fp16 BGE-M3): batch 64, seq 1024 reached ~8.59 GB
//! total with ~2.27 GB of weights ⇒ ~6.32 GB activations ⇒
//!   K_ATTN ≈ 6.32e9 / (64 · 1024²) ≈ 94 bytes.
//! `MAX_BATCH` caps tiny-sequence batches so the (un-modelled) linear-activation term
//! cannot explode; the runtime OOM backoff in `main.rs` grows `k_attn` if reality is
//! tighter than the model, so the constant only needs to be in the right ballpark.

/// Calibrated attention-memory constant (bytes per row·seq²-unit). See module docs.
pub const K_ATTN_DEFAULT: f64 = 94.0;

/// MemModel turns a VRAM budget into a per-length batch size. It is a small closed-loop
/// controller: [`shrink`](MemModel::shrink) raises `k_attn` after an OOM (smaller
/// batches), [`grow`](MemModel::grow) lowers it when VRAM headroom is left over (bigger
/// batches), bounded by `min_k` so growth can't run away.
#[derive(Debug, Clone)]
pub struct MemModel {
    /// Activation budget in bytes: free VRAM × fraction − model weights.
    pub avail_bytes: f64,
    /// Attention-memory constant; shrink raises it, grow lowers it.
    pub k_attn: f64,
    /// Growth floor for `k_attn`: the calibration is conservative (the constant was
    /// measured with the old doubling arena), so we allow growth down to this fraction
    /// of the default before relying on the OOM backoff to find the real ceiling.
    pub min_k: f64,
    /// Hard cap so tiny-sequence batches don't explode the linear-activation term.
    pub max_batch: usize,
    /// Ceiling for `k_attn`. Past the point where the LONGEST sequence already
    /// yields a batch of one, raising k_attn cannot make that batch any smaller —
    /// it only keeps shrinking the SHORT-sequence batches, which were never the
    /// thing that ran out of memory.
    ///
    /// Measured on the first full campaign: 13 OOMs took k_attn from 94 to 15 550,
    /// far past the 12 447 at which seq=1024 is already down to one clause. The
    /// excess bought nothing and cost throughput on every short batch after it.
    pub max_k: f64,
}

impl MemModel {
    /// batch_for_len returns the largest safe batch size at sequence length `seq`.
    pub fn batch_for_len(&self, seq: usize) -> usize {
        let seq = seq.max(1) as f64;
        let by_mem = (self.avail_bytes / (self.k_attn * seq * seq)).floor();
        let by_mem = if by_mem.is_finite() && by_mem >= 1.0 {
            by_mem as usize
        } else {
            1
        };
        by_mem.clamp(1, self.max_batch.max(1))
    }

    /// shrink raises k_attn (multiplicatively) so every subsequent batch is smaller —
    /// called after a CUDA out-of-memory so the campaign self-corrects to the real card.
    pub fn shrink(&mut self, factor: f64) {
        if factor > 1.0 {
            self.k_attn = (self.k_attn * factor).min(self.max_k);
        }
    }

    /// grow lowers k_attn (factor < 1) so subsequent batches are larger — called when a
    /// VRAM probe shows headroom left and no recent OOM. Floored at `min_k` so the
    /// controller can't grow without bound; the OOM backoff is the hard ceiling.
    pub fn grow(&mut self, factor: f64) {
        if (0.0..1.0).contains(&factor) {
            self.k_attn = (self.k_attn * factor).max(self.min_k);
        }
    }
}

/// batch_end returns the exclusive end of the batch starting at `start`, extending it
/// while the row count stays within `batch_for_len(frontier_len)`. The slice is sorted
/// ascending, so the last (largest) length governs the batch's memory. Always advances
/// by ≥1 (a single clause longer than the model allows still gets processed alone).
///
/// Computing one batch at a time (rather than planning all up front) lets the runtime
/// OOM backoff shrink the model and have every subsequent batch react immediately.
pub fn batch_end(token_lens: &[usize], start: usize, model: &MemModel) -> usize {
    let n = token_lens.len();
    let mut j = start + 1;
    while j < n {
        let cap = model.batch_for_len(token_lens[j]);
        if (j - start + 1) > cap {
            break;
        }
        j += 1;
    }
    j.max(start + 1).min(n)
}

/// plan_batches greedily packs a length-sorted token-length slice into `[start, end)`
/// ranges via [`batch_end`]. The hot loop in `main.rs` calls [`batch_end`] directly (so
/// the OOM backoff can adapt mid-run); this whole-plan form exists to exercise the packer
/// in tests.
#[cfg(test)]
pub fn plan_batches(token_lens: &[usize], model: &MemModel) -> Vec<(usize, usize)> {
    let mut out = Vec::new();
    let n = token_lens.len();
    let mut i = 0;
    while i < n {
        let end = batch_end(token_lens, i, model);
        out.push((i, end));
        i = end;
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn model(avail: f64, max_batch: usize) -> MemModel {
        MemModel {
            avail_bytes: avail,
            k_attn: K_ATTN_DEFAULT,
            min_k: K_ATTN_DEFAULT * 0.3,
            max_batch,
            max_k: f64::INFINITY,
        }
    }

    #[test]
    fn batch_for_len_is_monotonic_non_increasing() {
        let m = model(11.0e9, 1024);
        let mut prev = usize::MAX;
        for seq in [16, 32, 64, 128, 256, 512, 1024] {
            let b = m.batch_for_len(seq);
            assert!((1..=1024).contains(&b), "seq {seq} -> {b}");
            assert!(b <= prev, "not non-increasing at seq {seq}: {b} > {prev}");
            prev = b;
        }
    }

    #[test]
    fn short_clauses_hit_the_cap_long_ones_shrink() {
        let m = model(11.0e9, 512);
        assert_eq!(
            m.batch_for_len(16),
            512,
            "short clauses should hit MAX_BATCH"
        );
        // At seq 1024 on an ~11 GB budget the model allows ~114 rows (see module docs).
        let long = m.batch_for_len(1024);
        assert!((90..=140).contains(&long), "seq 1024 -> {long}");
    }

    #[test]
    fn a_single_oversized_clause_still_makes_progress() {
        // Tiny budget: batch_for_len is 1 everywhere, but every item must still be placed.
        let m = model(1.0, 512);
        let lens = vec![1000usize, 1000, 1000];
        let plan = plan_batches(&lens, &m);
        assert_eq!(plan, vec![(0, 1), (1, 2), (2, 3)]);
    }

    #[test]
    fn plan_covers_every_item_contiguously_and_respects_caps() {
        let m = model(11.0e9, 512);
        // Sorted ascending: many short, a few long.
        let mut lens: Vec<usize> = vec![20usize; 5000];
        lens.extend(vec![1024usize; 50]);
        lens.sort_unstable();
        let plan = plan_batches(&lens, &m);
        // Contiguous full cover.
        assert_eq!(plan.first().unwrap().0, 0);
        assert_eq!(plan.last().unwrap().1, lens.len());
        for w in plan.windows(2) {
            assert_eq!(w[0].1, w[1].0, "ranges must be contiguous");
        }
        // Every range respects the cap for its max (last) length, and is non-empty.
        for &(s, e) in &plan {
            assert!(e > s);
            let cap = m.batch_for_len(lens[e - 1]);
            assert!(e - s <= cap, "range {s}..{e} exceeds cap {cap}");
        }
        // Short-clause batches should be far larger than the old fixed 64.
        let first_sz = plan[0].1 - plan[0].0;
        assert!(
            first_sz > 200,
            "short batch should be large, got {first_sz}"
        );
    }

    #[test]
    fn shrink_raises_k_attn_and_lowers_batches() {
        let mut m = model(11.0e9, 1024);
        let before = m.batch_for_len(512);
        m.shrink(1.5);
        let after = m.batch_for_len(512);
        assert!(
            after < before,
            "shrink should lower batch: {after} !< {before}"
        );
    }

    #[test]
    fn grow_raises_batches_but_is_floored_at_min_k() {
        let mut m = model(11.0e9, 4096);
        let before = m.batch_for_len(256);
        m.grow(0.85);
        assert!(
            m.batch_for_len(256) >= before,
            "grow should not lower the batch"
        );
        // Growing repeatedly can never push k_attn below min_k.
        for _ in 0..100 {
            m.grow(0.85);
        }
        assert!(m.k_attn >= m.min_k - 1e-9, "k_attn fell below min_k");
    }
}

#[cfg(test)]
mod controller_tests {
    use super::*;

    /// The OOM backoff must stop where it stops helping. Once the LONGEST sequence
    /// is down to one clause per batch, a bigger k_attn cannot shrink that batch
    /// further — it only shrinks the short-sequence batches, which never ran out of
    /// memory. The first real campaign ran past that point: 13 OOMs took k_attn to
    /// 15 550 when 12 447 already meant "one at a time" at 1024 tokens.
    #[test]
    fn shrink_stops_once_the_longest_sequence_is_a_batch_of_one() {
        let avail = 13.05e9_f64;
        let cap = avail / (1024.0 * 1024.0); // ≈ 12 447
        let mut m = MemModel {
            avail_bytes: avail,
            k_attn: K_ATTN_DEFAULT,
            min_k: K_ATTN_DEFAULT * 0.3,
            max_batch: 512,
            max_k: cap,
        };
        for _ in 0..40 {
            m.shrink(1.5);
        }
        assert!(
            m.k_attn <= cap + 1e-6,
            "k_attn ran away to {} past the {cap} ceiling",
            m.k_attn
        );
        assert_eq!(
            m.batch_for_len(1024),
            1,
            "at the ceiling the longest sequence is exactly one clause per batch"
        );
        assert!(
            m.batch_for_len(128) > 1,
            "short sequences must NOT be dragged down to one — that is the whole point"
        );
    }

    /// Recovery must be the inverse of the backoff. Geometric up and arithmetic
    /// down leaves the model permanently over-conservative after a burst of OOMs.
    #[test]
    fn one_clean_window_undoes_one_oom() {
        let mut m = MemModel {
            avail_bytes: 13.05e9,
            k_attn: K_ATTN_DEFAULT,
            min_k: K_ATTN_DEFAULT * 0.3,
            max_batch: 512,
            max_k: f64::INFINITY,
        };
        let start = m.k_attn;
        m.shrink(1.5);
        m.grow(1.0 / 1.5);
        assert!(
            (m.k_attn - start).abs() < 1e-6,
            "shrink then grow left k_attn at {} instead of {start}",
            m.k_attn
        );
    }

    /// Growth is still floored: the controller may not talk itself into batches the
    /// calibration never supported.
    #[test]
    fn growth_is_still_bounded_by_min_k() {
        let mut m = MemModel {
            avail_bytes: 13.05e9,
            k_attn: K_ATTN_DEFAULT,
            min_k: K_ATTN_DEFAULT * 0.3,
            max_batch: 512,
            max_k: f64::INFINITY,
        };
        for _ in 0..50 {
            m.grow(1.0 / 1.5);
        }
        assert!((m.k_attn - K_ATTN_DEFAULT * 0.3).abs() < 1e-6);
    }
}
