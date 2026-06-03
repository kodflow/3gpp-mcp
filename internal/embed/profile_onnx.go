//go:build onnx

package embed

import (
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// embedProfiler is the opt-in (EMBED_PROFILE=1) per-batch instrumentation that
// localises the embed throughput wall: it splits wall time into CPU tokenisation
// vs GPU/ORT Run, and tracks padded-token volume so tok/s (the throughput-bound
// invariant — inference cost scales with padded tokens, not clauses) is visible.
// Disabled by default with near-zero overhead (one branch). It logs an aggregate
// line every logEvery batches, so a long remote run shows WHERE the time goes
// (tokenize_s vs run_s vs tok/s) instead of just clause/s.
type embedProfiler struct {
	on       bool
	logEvery int64

	tokNs   atomic.Int64 // total ns in tokenise+pad (prepareBatch)
	runNs   atomic.Int64 // total ns in session.Run
	batches atomic.Int64
	rows    atomic.Int64 // clauses processed
	padToks atomic.Int64 // sum(n*maxLen) — the work ORT actually did (incl. padding)
}

// prof is the process-global profiler; cheap when off.
var prof = newProfiler()

func newProfiler() *embedProfiler {
	every := int64(64)
	if v := os.Getenv("EMBED_PROFILE_EVERY"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			every = n
		}
	}
	return &embedProfiler{on: os.Getenv("EMBED_PROFILE") == "1", logEvery: every}
}

// addTokenize records one prepareBatch's tokenise+pad cost.
func (p *embedProfiler) addTokenize(d time.Duration) {
	if !p.on {
		return
	}
	p.tokNs.Add(int64(d))
}

// addRun records one session.Run: its wall time, row count, and padded length.
// It emits an aggregate line every logEvery batches.
func (p *embedProfiler) addRun(d time.Duration, n, maxLen int) {
	if !p.on {
		return
	}
	p.runNs.Add(int64(d))
	p.rows.Add(int64(n))
	p.padToks.Add(int64(n * maxLen))
	if b := p.batches.Add(1); b%p.logEvery == 0 {
		p.flush(b)
	}
}

// flush logs the running split. Safe to call from multiple goroutines (atomics).
func (p *embedProfiler) flush(b int64) {
	runS := time.Duration(p.runNs.Load()).Seconds()
	tokS := time.Duration(p.tokNs.Load()).Seconds()
	rows := p.rows.Load()
	pad := p.padToks.Load()
	clS, tokThru := 0.0, 0.0
	if runS > 0 {
		clS = float64(rows) / runS
		tokThru = float64(pad) / runS
	}
	avgLen := 0.0
	if rows > 0 {
		avgLen = float64(pad) / float64(rows)
	}
	log.Printf("embed-profile: batches=%d rows=%d run_s=%.1f tok_s(prep)=%.1f | %.0f clause/s %.0f tok/s avg_pad_len=%.0f (run-bound: %s)",
		b, rows, runS, tokS, clS, tokThru, avgLen, boundLabel(runS, tokS))
}

// boundLabel names the dominant stage so the operator sees at a glance whether to
// attack tokenisation (CPU) or inference (GPU). prepareBatch runs on producer
// goroutines concurrently with Run, so these overlap; the larger sum is the wall.
func boundLabel(runS, tokS float64) string {
	if tokS > runS {
		return "TOKENIZE-bound"
	}
	return "RUN-bound"
}
