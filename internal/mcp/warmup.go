package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/kodflow/3gpp-mcp/internal/search"
)

// WithWarmup starts, as soon as the server is built, one search through every arm
// of each half (search.Engine.Warm), in the background, and reports what it took
// through logf. See Warm for why: the first dense query of a session loads the
// HNSW index, 19-28 s on the 3GPP half, and did it inside the client's budget.
//
// IT IS TIED TO THE SERVER'S LIFETIME, both ends (Qodo, #348). ctx stops it —
// between arms and between halves, since a DuckDB query in flight is never
// cancelled (storeCtxNote) — and running adds one to wg, so the caller can wait
// for it before closing the stores it queries. An untracked goroutine would race
// the deferred Close of both corpora on any shutdown.
//
// THE COST IT MOVES, honestly: the store serves one connection at a time, so a
// client query arriving during the warm-up waits behind the warm-up query in
// flight (up to the index load) instead of paying that load itself. For the
// hybrid path — what this server is for — that is strictly better; for a client
// whose very first call is lexical-only, it can be slower than it would have
// been. MCP3GPP_NO_WARMUP=1 declines the whole thing.
func WithWarmup(ctx context.Context, wg *sync.WaitGroup, logf func(format string, args ...any)) Option {
	return func(h *handlers) {
		h.warmCtx, h.warmWG, h.warmLog = ctx, wg, logf
	}
}

// warmUp warms the halves one after the other — the 3GPP engine first, since
// every unscoped query reaches it — so the two cold starts do not compete for
// the same disk and cores.
func (h *handlers) warmUp() {
	if h.warmWG != nil {
		defer h.warmWG.Done()
	}
	ctx := h.warmCtx
	if ctx == nil {
		ctx = context.Background()
	}
	for _, e := range []*search.Engine{h.eng, h.etsiEng} {
		if e == nil {
			continue
		}
		if ctx.Err() != nil {
			h.warmLog("warm-up stopped before the %s half: %v", e.Name(), ctx.Err())
			return
		}
		r := e.Warm(ctx)
		var arms []string
		for _, a := range r.Arms {
			s := fmt.Sprintf("%s %.1fs", a.Arm, float64(a.Ms)/1000)
			if !a.Ran {
				s += " (skipped: " + a.Skipped + ")"
			}
			arms = append(arms, s)
		}
		h.warmLog("warm-up of the %s half done in %.1fs: %s", r.Corpus, float64(r.Ms)/1000, strings.Join(arms, ", "))
	}
}
