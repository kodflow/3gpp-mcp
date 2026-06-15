// Package metrics is a tiny, dependency-free, in-process request-metrics collector
// for the HTTP serve mode: a request counter, a bounded ring of recent latencies
// (for avg / p50 / p95 / p99), and a rolling per-HOUR count timeseries over the
// last 24 hours. It is concurrency-safe and bounded in memory — no Prometheus, no
// external store, no persistence (the dashboard is a live at-a-glance view,
// CLAUDE.md §10 vanilla).
//
// The timeseries is per-hour over 24h (not per-minute over 1h): a long-lived serve
// process (uptime measured in hours/days) tells a far more useful story as a
// 24-bar day profile — and each bar carries its start-of-hour unix time so the
// dashboard can label the x-axis with the local hour, instead of 60 unlabelled
// near-empty minute bars.
package metrics

import (
	"sort"
	"sync"
	"time"
)

const (
	latRingCap = 4096 // recent latency samples kept for percentile estimation
	tsHours    = 24   // rolling window of per-hour request counts (last 24h)
	secPerHour = 3600
	recentCap  = 10000 // recent per-request log entries kept for the dashboard feed
	queryMax   = 200   // a logged query is truncated to this many runes (bounded memory)
)

// ReqLog is one entry of the recent-request feed: what arrived (HTTP method,
// JSON-RPC method, MCP tool, query text) and how long it took. It powers the
// dashboard's live "recent requests" table — the per-request timer the operator
// needs to SEE which call is slow (vs the aggregate percentiles, which can't
// attribute a tail latency to a specific query).
type ReqLog struct {
	T      int64   `json:"t"`      // unix seconds when the request completed
	Method string  `json:"method"` // HTTP method ("POST" | "GET")
	RPC    string  `json:"rpc"`    // JSON-RPC method ("tools/call", "initialize", …); "" if not parsed
	Tool   string  `json:"tool"`   // MCP tool name for tools/call ("search_spec", …); "" otherwise
	Query  string  `json:"query"`  // the query argument (truncated); "" when absent
	DurMs  float64 `json:"dur_ms"` // wall-clock handling time in milliseconds
	Status int     `json:"status"` // HTTP status code written to the client
}

// Collector accumulates request counts and latencies. The zero value is not
// usable — call New.
type Collector struct {
	mu       sync.Mutex
	total    uint64
	lat      []float64       // ring of recent latencies (ms); grows to latRingCap then overwrites
	latNext  int             // next overwrite index once the ring is full
	bucket   [tsHours]uint64 // per-hour request counts, indexed by hour % tsHours
	bucketHr [tsHours]int64  // the hour (unix/3600) each bucket currently holds; -1 = empty

	recent     []ReqLog // ring of recent per-request log entries (the feed)
	recentNext int      // next overwrite index once the ring is full

	now func() time.Time
}

// New returns an empty collector.
func New() *Collector {
	c := &Collector{
		lat:    make([]float64, 0, latRingCap),
		recent: make([]ReqLog, 0, recentCap),
		now:    time.Now,
	}
	for i := range c.bucketHr {
		c.bucketHr[i] = -1
	}
	return c
}

// Observe records one request of duration d.
func (c *Collector) Observe(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	c.mu.Lock()
	c.total++
	if len(c.lat) < latRingCap {
		c.lat = append(c.lat, ms)
	} else {
		c.lat[c.latNext] = ms
		c.latNext = (c.latNext + 1) % latRingCap
	}
	hour := c.now().Unix() / secPerHour
	idx := int(hour % tsHours)
	if c.bucketHr[idx] != hour { // reuse / reset a stale (or first-touch) bucket
		c.bucketHr[idx] = hour
		c.bucket[idx] = 0
	}
	c.bucket[idx]++
	c.mu.Unlock()
}

// Record appends one entry to the recent-request feed (a bounded ring of the last
// recentCap requests). The query is truncated to queryMax runes so a pathological
// payload can't blow up memory; T is stamped from the collector's clock if unset.
// Record is independent of Observe: the middleware Observes only request/response
// latencies (POST) into the percentiles, but Records every request (incl. the
// long-lived SSE GET stream) into the feed so the operator still sees it.
func (c *Collector) Record(e ReqLog) {
	e.Query = truncateRunes(e.Query, queryMax)
	c.mu.Lock()
	if e.T == 0 {
		e.T = c.now().Unix()
	}
	if len(c.recent) < recentCap {
		c.recent = append(c.recent, e)
	} else {
		c.recent[c.recentNext] = e
		c.recentNext = (c.recentNext + 1) % recentCap
	}
	c.mu.Unlock()
}

// Recent returns up to limit of the most recent log entries, NEWEST FIRST. limit
// is clamped to [1, recentCap]; a non-positive limit defaults to 100.
func (c *Collector) Recent(limit int) []ReqLog {
	if limit <= 0 {
		limit = 100
	}
	if limit > recentCap {
		limit = recentCap
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.recent)
	if n == 0 {
		return nil
	}
	if limit > n {
		limit = n
	}
	out := make([]ReqLog, 0, limit)
	// Walk backwards from the most recently written slot. Before the ring fills,
	// the newest entry is at len-1; once full, it is at recentNext-1 (mod cap).
	start := n - 1
	if n == recentCap {
		start = (c.recentNext - 1 + recentCap) % recentCap
	}
	for i := 0; i < limit; i++ {
		out = append(out, c.recent[(start-i+recentCap)%recentCap])
	}
	return out
}

// truncateRunes caps s to at most max runes (rune-safe so a multibyte char is
// never cut mid-sequence), appending an ellipsis when it had to cut.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// Point is one hour of the request timeseries: T is the unix start-of-hour.
type Point struct {
	T     int64  `json:"t"`
	Count uint64 `json:"count"`
}

// Snapshot is an immutable read of the collector at one instant.
type Snapshot struct {
	Total     uint64  `json:"total"`
	AvgMs     float64 `json:"avg_ms"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	Series    []Point `json:"series"`       // last WindowHrs hours, oldest → newest
	WindowHrs int     `json:"window_hours"` // length of Series (hours)
	Samples   int     `json:"samples"`      // latency samples backing the percentiles
}

// Snapshot computes the current aggregates. O(n log n) on the latency ring; the
// ring is bounded, so this is cheap and never blocks Observe for long.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	lat := make([]float64, len(c.lat))
	copy(lat, c.lat)
	total := c.total
	nowHr := c.now().Unix() / secPerHour
	series := make([]Point, tsHours)
	for i := 0; i < tsHours; i++ {
		h := nowHr - int64(tsHours-1-i) // oldest → newest
		idx := int(((h % tsHours) + tsHours) % tsHours)
		var cnt uint64
		if c.bucketHr[idx] == h {
			cnt = c.bucket[idx]
		}
		series[i] = Point{T: h * secPerHour, Count: cnt}
	}
	c.mu.Unlock()

	sort.Float64s(lat)
	s := Snapshot{Total: total, Series: series, WindowHrs: tsHours, Samples: len(lat)}
	if n := len(lat); n > 0 {
		var sum float64
		for _, v := range lat {
			sum += v
		}
		s.AvgMs = sum / float64(n)
		s.P50Ms = percentile(lat, 0.50)
		s.P95Ms = percentile(lat, 0.95)
		s.P99Ms = percentile(lat, 0.99)
	}
	return s
}

// percentile returns the p-quantile (0..1) of a SORTED slice (nearest-rank).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p*float64(len(sorted)-1) + 0.5)
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
