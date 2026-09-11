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

// forTokenizer is what a query or a passage looks like when it reaches the
// cross-encoder's tokenizer: its trailing whitespace removed.
//
// THE TOKENIZER PANICS ON TRAILING CONTROL WHITESPACE. github.com/sugarme/tokenizer
// v0.3.0, loaded with bge-reranker-v2-m3's tokenizer.json, answers
//
//	"Foreword\n"    slice bounds out of range [21:17]
//	"5.1 Scope\n"   slice bounds out of range [24:19]
//	"Foreword\t"    slice bounds out of range [21:17]
//	"Foreword\r\n"  index out of range [10] with length 10
//
// while "Foreword", "Foreword " and "\nForeword" encode (measured 2026-09-11). The
// engine builds every passage as heading + "\n" + text, so EVERY clause with an
// empty body — a heading-only clause, ETSI's "0 1 0 1 1 5" code rows — ends in
// "\n" and takes the whole call down. The first run of the served retrieval gate
// found it: search_spec(rerank=true) for a judged query panicked inside the
// stdio worker, which mcp-go recovers WITHOUT answering, so the client waited
// forever.
//
// Trimming is neutral for the model: XLM-R's sentencepiece normalisation folds
// whitespace into the ▁ prefix of the NEXT piece, so a trailing run contributes no
// token in the reference tokenizer either.
func forTokenizer(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

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
