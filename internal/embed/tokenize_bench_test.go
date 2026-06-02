package embed

// Throwaway diagnostic benchmark (no build tag — the tokenizer is pure Go). Measures
// single-threaded SentencePiece tokenisation throughput to confirm whether the embed
// pipeline is CPU-tokenisation-bound (the multi-GPU post-mortem hypothesis). Run:
//   go test ./internal/embed -run xxx -bench BenchmarkTokenizeThroughput -benchtime 3x

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sugarme/tokenizer/pretrained"
)

func BenchmarkTokenizeThroughput(b *testing.B) {
	tokPath := "../../data/models/bge-m3/tokenizer.json"
	if _, err := os.Stat(tokPath); err != nil {
		b.Skipf("no tokenizer at %s", tokPath)
	}
	tok, err := pretrained.FromFile(tokPath)
	if err != nil {
		b.Fatalf("load tokenizer: %v", err)
	}
	// A realistic ~400-char 3GPP clause (heading + body), the unit the embedder feeds.
	clause := "5.2.3.2 Registration procedure. " + strings.Repeat(
		"The AMF shall, upon receipt of the Registration Request, verify the security context "+
			"and initiate the NAS authentication procedure with the UE if no valid 5G NAS security "+
			"context is available; the SMF then establishes the PDU session over the N4 reference point. ", 3)

	const n = 2000
	start := time.Now()
	total := 0
	for i := 0; i < n; i++ {
		en, err := tok.EncodeSingle(clause)
		if err != nil {
			b.Fatalf("encode: %v", err)
		}
		total += len(en.Ids)
	}
	el := time.Since(start)
	rate := float64(n) / el.Seconds()
	b.ReportMetric(rate, "clauses/s")
	b.ReportMetric(float64(total)/float64(n), "tokens/clause")
	b.Logf("tokenised %d clauses in %s → %.0f clauses/s (single thread), %.0f tokens/clause",
		n, el.Round(time.Millisecond), rate, float64(total)/float64(n))
}
