package search

// report.go — what each retrieval arm ACTUALLY did for one Search, so a caller
// can say it instead of implying the arms it asked for all ran.
//
// WHY THIS EXISTS. Search degrades on purpose: an arm whose budget has run out,
// whose embedder or store call failed, or whose capability is absent contributes
// nothing and the fusion carries on with what it has — degrade, never block
// (CLAUDE.md §1). That is the right behaviour and it was completely silent. The
// served retrieval gate measured the hybrid arm at 80-120 s per call on the 3GPP
// half (#340), against a served SEARCH_BUDGET of 20 s: on the image, the sparse
// and the rerank passes were skipped on every such call, and the answer still
// said mode "hybrid", with `rerank: true` honoured in name only. A client asking
// for a cross-encoded page received the fused order and had no way to tell.
// ModeServed could not say it either: it reports what the engine is CAPABLE of,
// not what one call did.
//
// The report travels on the context (WithTrace) rather than through Search's
// signature: Search has callers that do not care (bench, the dashboard, tests),
// and the one that does — the search_spec handler — calls it through the ETSI
// federation too, several times per request.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The arm names, as they appear in a report.
const (
	ArmLexical = "lexical"
	ArmDense   = "dense"
	ArmSparse  = "sparse"
	ArmRerank  = "rerank"
)

// ArmRun is what one retrieval arm did for one Search.
//
// Ran means the arm produced its list (or, for rerank, its order) and that output
// is in the answer — with Hits results, possibly zero: an arm that ran and found
// nothing under the caller's filter is not a degradation. Skipped says why a
// requested arm contributed NOTHING; it is empty exactly when Ran is true.
type ArmRun struct {
	Arm     string `json:"arm"`
	Ran     bool   `json:"ran"`
	Hits    int    `json:"hits"`
	Ms      int64  `json:"ms"`
	Skipped string `json:"skipped,omitempty"`
}

// Report is one Search: which corpus answered it (the engine's name), under
// which document type (the one thing that tells the ETSI half's passes apart),
// and each requested arm in the order it ran.
type Report struct {
	Corpus  string   `json:"corpus,omitempty"`
	DocType string   `json:"doc_type,omitempty"`
	Ms      int64    `json:"ms"`
	Arms    []ArmRun `json:"arms"`
}

func (r *Report) ran(arm string, hits int, since time.Time) {
	r.Arms = append(r.Arms, ArmRun{Arm: arm, Ran: true, Hits: hits, Ms: msSince(since)})
}

func (r *Report) skip(arm, why string, since time.Time) {
	r.Arms = append(r.Arms, ArmRun{Arm: arm, Skipped: why, Ms: msSince(since)})
}

// Degraded names every requested arm that contributed nothing, and why — one
// line per arm and pass, prefixed with the corpus when there is one.
func Degraded(reps []Report) []string {
	var out []string
	for _, r := range reps {
		where := r.Corpus
		if r.DocType != "" && where != "" {
			where += " " + r.DocType
		}
		for _, a := range r.Arms {
			if a.Skipped == "" {
				continue
			}
			if where != "" {
				out = append(out, fmt.Sprintf("%s (%s): %s", a.Arm, where, a.Skipped))
			} else {
				out = append(out, a.Arm+": "+a.Skipped)
			}
		}
	}
	return out
}

func msSince(t time.Time) int64 { return time.Since(t).Milliseconds() }

// Trace collects the Report of every Search run under one context.
type Trace struct {
	mu   sync.Mutex
	reps []Report
}

type traceKey struct{}

// WithTrace returns a context under which every Engine.Search appends its Report
// to the returned Trace.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// Reports returns what was recorded, in the order the searches finished.
func (t *Trace) Reports() []Report {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Report(nil), t.reps...)
}

func (t *Trace) add(r Report) {
	t.mu.Lock()
	t.reps = append(t.reps, r)
	t.mu.Unlock()
}

func traceFrom(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

// budgetKey marks a context whose deadline IS the search budget (WithBudget).
type budgetKey struct{}

// WithBudget starts the search budget for a whole request — every Search run
// under the returned context shares one deadline instead of each starting its
// own.
//
// search_spec federates: one call runs the 3GPP engine, then the ETSI engine once
// per normative document type. With the budget started inside Search, each of
// those passes got a fresh SEARCH_BUDGET, so a "20 s" request could spend three
// budgets before answering — the cap named a wall-clock the call never honoured.
// The budget is the CALLER's promise to its client, so the caller starts it.
//
// A zero or negative SEARCH_BUDGET returns ctx unchanged (no budget), as Search
// does. The deadline never reaches DuckDB: Search hands its store calls
// context.WithoutCancel, see storeCtxNote.
func WithBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := searchBudgetFor()
	if budget <= 0 {
		return ctx, func() {}
	}
	c, cancel := context.WithTimeout(ctx, budget)
	return context.WithValue(c, budgetKey{}, budgetStart{at: time.Now(), budget: budget}), cancel
}

type budgetStart struct {
	at     time.Time
	budget time.Duration
}

// budgetCtx returns the context the expensive arms run under, and how to say
// why they were skipped once it is done: the request's shared budget when the
// caller started one (WithBudget), else a budget of this Search's own.
func budgetCtx(ctx context.Context) (context.Context, context.CancelFunc, func() string) {
	if b, ok := ctx.Value(budgetKey{}).(budgetStart); ok {
		return ctx, func() {}, func() string { return skipReason(ctx, b) }
	}
	budget := searchBudgetFor()
	if budget <= 0 {
		return ctx, func() {}, func() string { return skipReason(ctx, budgetStart{}) }
	}
	b := budgetStart{at: time.Now(), budget: budget}
	c, cancel := context.WithTimeout(ctx, budget)
	return c, cancel, func() string { return skipReason(c, b) }
}

func skipReason(ctx context.Context, b budgetStart) string {
	switch {
	case b.budget > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("the search budget ran out (SEARCH_BUDGET=%s, %.1fs spent) before this arm could run",
			b.budget, time.Since(b.at).Seconds())
	case ctx.Err() != nil:
		return "the request was cancelled before this arm could run (" + ctx.Err().Error() + ")"
	default:
		return "the request's deadline passed before this arm could run"
	}
}

// failed renders an arm's error as the reason it was skipped, on one line.
func failed(what string, err error) string {
	return what + ": " + strings.Join(strings.Fields(err.Error()), " ")
}
