//! Cross-runtime embedding fingerprint, byte-for-byte identical to the Go
//! `internal/embed.ClauseHash`:
//!
//! ```text
//! hash = hex(sha256( EmbedText(heading,text) + "|" + embed_identity ))[:16]
//! EmbedText(heading,text) = heading + "\n" + text
//! ```
//!
//! `embed_identity` is the canonical Go `EmbedIdentity` digest, passed in via the
//! `--embed-identity` flag (printed by `go run ./cmd/embedid`). Single-sourcing the
//! identity in Go — rather than re-deriving the digest formula here — means the Rust
//! embedder and the Go re-embed gate / serve-time coherence guard can never diverge.
//! A run that writes the matching hash makes a later Go `embed` re-check a no-op.

use sha2::{Digest, Sha256};

/// The canonical text fed to the embedder for a clause (mirrors Go `embed.EmbedText`).
pub fn embed_text(heading: &str, text: &str) -> String {
    format!("{heading}\n{text}")
}

/// The 16-hex-char clause fingerprint stored alongside the vector.
pub fn clause_hash(heading: &str, text: &str, embed_identity: &str) -> String {
    let mut h = Sha256::new();
    h.update(embed_text(heading, text).as_bytes());
    h.update(b"|");
    h.update(embed_identity.as_bytes());
    let digest = h.finalize();
    hex::encode(digest)[..16].to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn embed_text_joins_with_newline() {
        assert_eq!(embed_text("6 X1", "body"), "6 X1\nbody");
    }

    #[test]
    fn clause_hash_golden_cross_runtime() {
        // The IDENTICAL constant is asserted by the Go test
        // internal/embed TestClauseHashGoldenCrossRuntime. If both pass, the Go
        // ClauseHash and Rust clause_hash agree byte-for-byte (modelID hashed RAW,
        // no EmbedIdentity re-wrap on the Go side). This pins the Phase −1 fix.
        assert_eq!(
            clause_hash("6 X1", "body", "embedv1-deadbeef0000"),
            "a2e0978ed24f247a"
        );
    }

    #[test]
    fn clause_hash_is_16_hex_and_stable() {
        let a = clause_hash("h", "t", "id-1");
        assert_eq!(a.len(), 16);
        assert!(a.chars().all(|c| c.is_ascii_hexdigit()));
        // deterministic
        assert_eq!(a, clause_hash("h", "t", "id-1"));
        // identity participates: a different identity flips the hash
        assert_ne!(a, clause_hash("h", "t", "id-2"));
        // text participates
        assert_ne!(a, clause_hash("h", "t2", "id-1"));
    }
}
