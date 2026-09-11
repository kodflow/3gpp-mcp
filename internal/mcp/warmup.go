package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/search"
)

// WithWarmup starts, as soon as the server is built, one search through every
// arm of each half (search.Engine.Warm), in the background, and reports what it
// took through logf. See Warm for why: the first dense query of a session loads
// the HNSW index, 21-28 s on the 3GPP half, and did it inside the client's
// budget.
func WithWarmup(logf func(format string, args ...any)) Option {
	return func(h *handlers) { h.warmLog = logf }
}

// warmUp warms the halves one after the other — the 3GPP engine first, since
// every unscoped query reaches it — so the two cold starts do not compete for
// the same disk and cores.
func (h *handlers) warmUp() {
	for _, e := range []*search.Engine{h.eng, h.etsiEng} {
		if e == nil {
			continue
		}
		r := e.Warm(context.Background())
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
