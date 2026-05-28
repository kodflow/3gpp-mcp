package embed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode/utf8"
)

// safeEncode runs encodeFn with a panic recovery net. A panic in the tokenizer
// (e.g. sugarme/tokenizer v0.3.0's Metaspace pretokenizer off-by-one on some
// UTF-8 inputs) becomes an ordinary error so the caller can decide how to
// degrade. Kept untagged + injection-based so the recovery contract is
// unit-tested without needing -tags onnx + the model on disk.
func safeEncode(encodeFn func(string) ([]int, error), text string) (ids []int, err error) {
	defer func() {
		if r := recover(); r != nil {
			ids = nil
			err = fmt.Errorf("tokenizer panic: %v", r)
		}
	}()
	return encodeFn(text)
}

// snippetHash returns 8 hex chars of sha256(text). Used as a stable correlation
// ID in logs: same problem text → same hash, so an operator can spot recurring
// inputs without ever putting the inputs themselves (possibly user queries) in
// the log. NOT a security primitive — collision-resistance isn't needed here,
// only stability.
func snippetHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:4])
}

// safePrefix returns up to n bytes of s on a valid UTF-8 boundary — kept for
// places that intentionally want to see content (debug-only logging gated by
// an opt-in env var; never used for user query content).
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
