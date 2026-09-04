// Package rerank is the optional cross-encoder reranker seam (axis #7). After
// RRF fusion the engine retrieves broad (top-20) and a reranker re-scores the
// (query, passage) pairs to return a sharper top-k. Mirrors the embed seam: the
// default build is Disabled{} (degrade, never block); the real
// bge-reranker-v2-m3 ONNX backend lives behind `-tags onnx`. A dependency-free
// Lexical reranker (EMBEDDER-style) proves the path without a model download.
package rerank

import (
	"context"
	"os"
	"strings"
	"unicode"
)

// Reranker re-scores passages for a query; higher score = more relevant.
type Reranker interface {
	Enabled() bool
	// Score returns one score per passage, aligned to the input slice.
	Score(ctx context.Context, query string, passages []string) ([]float64, error)
}

// Disabled is the no-op reranker (default): the engine keeps the RRF order.
type Disabled struct{}

func (Disabled) Enabled() bool { return false }
func (Disabled) Score(context.Context, string, []string) ([]float64, error) {
	return nil, nil
}

// Lexical is a deterministic token-overlap reranker: score = |query∩passage
// tokens| weighted by passage brevity. Not semantic — it exists to exercise the
// rerank path in tests/bench without the ~0.5 GB cross-encoder. The production
// reranker is the ONNX bge-reranker-v2-m3 behind `-tags onnx`.
type Lexical struct{}

func (Lexical) Enabled() bool { return true }

func (Lexical) Score(_ context.Context, query string, passages []string) ([]float64, error) {
	q := tokenSet(query)
	out := make([]float64, len(passages))
	for i, p := range passages {
		toks := tokenize(p)
		hits := 0
		for _, t := range toks {
			if q[t] {
				hits++
			}
		}
		// Reward overlap, lightly penalise length so a focused clause outranks a
		// long one with the same number of incidental matches.
		denom := 1.0 + float64(len(toks))/200.0
		out[i] = float64(hits) / denom
	}
	return out, nil
}

// New selects the reranker from the RERANKER env var:
//
//	RERANKER=lexical    -> Lexical{}    (deterministic; proves the path)
//	RERANKER=off|none   -> Disabled{}
//	(unset)             -> default build: Disabled{}, or ONNX cross-encoder with -tags onnx
func New() Reranker {
	switch strings.ToLower(os.Getenv("RERANKER")) {
	case "lexical":
		reason = ""
		return Lexical{}
	case "off", "none", "disabled":
		reason = "turned off by RERANKER"
		return Disabled{}
	}
	reason = ""
	r := newReranker()
	if !r.Enabled() && reason == "" {
		reason = "unavailable, and the backend did not say why"
	}
	return r
}

// reason records WHY the cross-encoder is off. Written by New/newReranker at
// startup and read afterwards, so no lock: the server builds one reranker before
// it serves anything.
var reason string

// Reason explains why Enabled() is false, and is empty when the arm is live.
//
// Every failure path in the ONNX backend returns Disabled{} — missing runtime,
// missing model, a tokenizer that will not parse, a session that will not build —
// and that is the right behaviour: a retrieval arm degrades, it does not stop the
// server. But it left `"reranker": false` in server_info with nothing to act on,
// on a machine where the model is on disk and the embedder using the same runtime
// is live. The sparse arm already reports sparse_reason for exactly this;
// reranking is the arm that had no such answer.
func Reason() string { return reason }

func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range tokenize(s) {
		m[t] = true
	}
	return m
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
