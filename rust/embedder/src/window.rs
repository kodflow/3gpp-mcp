//! mean_pool windowing — the long-clause strategy, ported from `internal/embed/window.go`.
//!
//! WHY THIS EXISTS. The embedder used to truncate at MAX_TOKENS, which drops the tail of
//! a long clause: the body of a big table, the second half of an ASN.1 block, the closing
//! normative paragraphs. Those tails are high-value content, and a query that matches only
//! there was unreachable. Windowing embeds the WHOLE clause in ≤300-word pieces and pools
//! the pieces into one vector, so nothing is silently dropped.
//!
//! This is a PORT, not a re-design. It mirrors `internal/embed/window.go` line for line,
//! because the two must agree: `windowing` is an `EmbedIdentity` component, so a corpus
//! embedded by one and queried by the other would be a silent quality regression that no
//! gate would catch. Two details of the Go original are load-bearing and easy to "improve"
//! by accident:
//!
//!   1. A SHORT text returns the ORIGINAL string, whitespace and all — not the words
//!      rejoined. A long text returns rejoined windows, so its whitespace IS normalised.
//!      The asymmetry looks like a bug and is not: it means the overwhelmingly common
//!      single-window case tokenises exactly the byte sequence it always did.
//!   2. The mean is over the NON-EMPTY windows only, and a single window is returned
//!      unchanged rather than re-normalised (it is already unit norm).
//!
//! See `internal/embed/testdata/window_parity.json`, the fixture both this module and the
//! Go package assert against, so the two implementations cannot drift apart unnoticed.

/// Bounds a window so it stays comfortably under the model's token limit for typical
/// prose (~1.3 tokens/word). Mirrors Go's `defaultWindowWords`.
pub const DEFAULT_WINDOW_WORDS: usize = 300;

/// window_text splits `text` into ≤`max_words` word-windows (no overlap), never breaking
/// a word. Short text yields a single window holding the ORIGINAL text.
///
/// Mirrors Go `windowText`. `strings.Fields` splits on runs of Unicode whitespace and
/// drops empties, which is exactly `str::split_whitespace`.
pub fn window_text(text: &str, max_words: usize) -> Vec<String> {
    let max_words = if max_words < 1 {
        DEFAULT_WINDOW_WORDS
    } else {
        max_words
    };
    let words: Vec<&str> = text.split_whitespace().collect();
    if words.len() <= max_words {
        // The original text, NOT words.join(" ") — see the module note.
        return vec![text.to_string()];
    }
    words.chunks(max_words).map(|c| c.join(" ")).collect()
}

/// mean_pool_l2 averages `vecs` component-wise and L2-normalises the result — the pooled
/// embedding of a multi-window clause.
///
/// Mirrors Go `meanPoolL2`: no windows → None; exactly one → that vector unchanged (it is
/// already unit norm); otherwise the mean over the NON-EMPTY windows, L2-normalised. A
/// window that could not be embedded is passed as empty and must not drag the mean toward
/// zero, so it is excluded from the divisor as well as the sum.
pub fn mean_pool_l2(vecs: &[Vec<f32>]) -> Option<Vec<f32>> {
    match vecs.len() {
        0 => return None,
        1 => {
            return if vecs[0].is_empty() {
                None
            } else {
                Some(vecs[0].clone())
            }
        }
        _ => {}
    }
    let mut dim = 0usize;
    let mut count = 0usize;
    for v in vecs {
        if !v.is_empty() {
            dim = v.len();
            count += 1;
        }
    }
    if count == 0 {
        return None;
    }
    // f64 accumulation, in window order, exactly as the Go original: the sum of many
    // small f32 components is where the two would otherwise diverge.
    let mut acc = vec![0f64; dim];
    for v in vecs {
        for (j, x) in v.iter().enumerate() {
            acc[j] += *x as f64;
        }
    }
    let mut out = vec![0f32; dim];
    let mut sum = 0f64;
    for (j, a) in acc.iter().enumerate() {
        let v = a / count as f64;
        out[j] = v as f32;
        sum += v * v;
    }
    if sum > 0.0 {
        let inv = (1.0 / sum.sqrt()) as f32;
        for o in out.iter_mut() {
            *o *= inv;
        }
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn short_text_is_one_window_and_keeps_its_original_bytes() {
        // The whitespace here is deliberate: a short clause must reach the tokenizer as
        // the exact bytes it always did, or every single-window vector in the corpus
        // shifts for no reason.
        let t = "alpha   beta\tgamma\n\ndelta";
        let w = window_text(t, 300);
        assert_eq!(w, vec![t.to_string()]);
    }

    #[test]
    fn a_long_text_splits_on_word_boundaries_with_no_overlap() {
        let words: Vec<String> = (0..750).map(|i| format!("w{i}")).collect();
        let text = words.join(" ");
        let w = window_text(&text, 300);
        assert_eq!(w.len(), 3, "750 words at 300 -> 3 windows");
        assert_eq!(w[0].split_whitespace().count(), 300);
        assert_eq!(w[1].split_whitespace().count(), 300);
        assert_eq!(w[2].split_whitespace().count(), 150);
        // No overlap, nothing lost, order preserved: the concatenation is the input.
        assert_eq!(w.join(" "), text);
        // And never mid-word.
        assert!(w[0].ends_with("w299"));
        assert!(w[1].starts_with("w300"));
    }

    #[test]
    fn exactly_max_words_is_still_one_window() {
        let text = (0..300)
            .map(|i| format!("w{i}"))
            .collect::<Vec<_>>()
            .join(" ");
        assert_eq!(window_text(&text, 300).len(), 1);
        let text301 = (0..301)
            .map(|i| format!("w{i}"))
            .collect::<Vec<_>>()
            .join(" ");
        assert_eq!(window_text(&text301, 300).len(), 2);
    }

    #[test]
    fn zero_max_words_falls_back_to_the_default() {
        let text = (0..400)
            .map(|i| format!("w{i}"))
            .collect::<Vec<_>>()
            .join(" ");
        assert_eq!(window_text(&text, 0).len(), 2); // 400 at 300 -> 2
    }

    #[test]
    fn one_window_is_returned_unchanged_not_renormalised() {
        // Deliberately NOT unit norm: Go returns vecs[idx[0]] as-is, and re-normalising
        // here would make the single-window path disagree with the corpus already on disk.
        let v = vec![0.5f32, 0.5, 0.5, 0.5];
        assert_eq!(mean_pool_l2(&[v.clone()]), Some(v));
    }

    #[test]
    fn pooling_averages_then_normalises() {
        let a = vec![1.0f32, 0.0];
        let b = vec![0.0f32, 1.0];
        let got = mean_pool_l2(&[a, b]).expect("pooled");
        let inv = 1.0f32 / 2f32.sqrt();
        assert!((got[0] - inv).abs() < 1e-6, "got {got:?}");
        assert!((got[1] - inv).abs() < 1e-6, "got {got:?}");
        let norm: f32 = got.iter().map(|x| x * x).sum::<f32>().sqrt();
        assert!((norm - 1.0).abs() < 1e-6, "norm {norm}");
    }

    #[test]
    fn an_unembeddable_window_is_excluded_from_the_mean_not_counted_as_zero() {
        let a = vec![1.0f32, 0.0];
        let empty: Vec<f32> = vec![];
        let b = vec![0.0f32, 1.0];
        let with_hole = mean_pool_l2(&[a.clone(), empty, b.clone()]).expect("pooled");
        let without = mean_pool_l2(&[a, b]).expect("pooled");
        assert_eq!(
            with_hole, without,
            "a window that could not be embedded must not drag the mean toward zero"
        );
    }

    #[test]
    fn no_windows_and_all_empty_windows_both_yield_nothing() {
        assert_eq!(mean_pool_l2(&[]), None);
        assert_eq!(mean_pool_l2(&[vec![], vec![]]), None);
    }
}

/// Cross-language parity: the split is pinned in ONE file, generated from the Go
/// reference (`internal/embed/window.go`) and asserted here.
///
/// `windowing` is an EmbedIdentity component. If Go and Rust ever disagreed about where a
/// clause splits, the corpus would be embedded under one split and queried under another —
/// and nothing else would notice, because both sides still emit 1024 well-formed floats.
/// This test is the only thing standing between that and production.
#[cfg(test)]
mod parity {
    use super::*;
    use std::path::PathBuf;

    #[derive(serde::Deserialize)]
    struct Case {
        name: String,
        max_words: usize,
        text: String,
        windows: Vec<String>,
    }

    fn fixture() -> Vec<Case> {
        // Unit tests of a binary crate run with CWD = the crate root.
        let p = PathBuf::from("../../internal/embed/testdata/window_parity.json");
        let raw = std::fs::read_to_string(&p).unwrap_or_else(|e| {
            panic!(
                "read {}: {e} — regenerate with `go test ./internal/embed -run TestWindowParityFixture -update`",
                p.display()
            )
        });
        serde_json::from_str(&raw).expect("parse the parity fixture")
    }

    #[test]
    fn rust_splits_exactly_where_go_splits() {
        let cases = fixture();
        assert!(
            cases.len() >= 16,
            "the fixture pins only {} cases; it is supposed to cover the table and ASN.1 shapes too",
            cases.len()
        );
        let mut multi = 0;
        for c in &cases {
            let got = window_text(&c.text, c.max_words);
            assert_eq!(
                got.len(),
                c.windows.len(),
                "case {:?}: Go produced {} window(s), Rust produced {}",
                c.name,
                c.windows.len(),
                got.len()
            );
            for (j, (g, w)) in got.iter().zip(&c.windows).enumerate() {
                assert_eq!(
                    g, w,
                    "case {:?} window {j}: Rust and Go disagree on the split",
                    c.name
                );
            }
            if got.len() > 1 {
                multi += 1;
            }
        }
        assert!(
            multi >= 4,
            "only {multi} multi-window cases — the fixture would not actually exercise pooling"
        );
    }

    /// The property #208 is about: truncation dropped the tail, windowing drops nothing.
    #[test]
    fn no_word_is_lost_across_the_windows() {
        for c in fixture() {
            let got = window_text(&c.text, c.max_words);
            let out: Vec<&str> = got.iter().flat_map(|w| w.split_whitespace()).collect();
            let want: Vec<&str> = c.text.split_whitespace().collect();
            assert_eq!(
                out, want,
                "case {:?}: the windows do not reconstruct the clause word for word",
                c.name
            );
        }
    }
}
