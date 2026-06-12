// Package metrics is a tiny, dependency-free, in-process request-metrics collector
// for the HTTP serve mode: a request counter, a bounded ring of recent latencies
// (for avg / p50 / p95 / p99), and a rolling per-minute count timeseries. It is
// concurrency-safe and bounded in memory — no Prometheus, no external store, no
// persistence (the dashboard is a live at-a-glance view, CLAUDE.md §10 vanilla).
package metrics

import (
	"sort"
	"sync"
	"time"
)

const (
	latRingCap = 4096 // recent latency samples kept for percentile estimation
	tsMinutes  = 60   // rolling window of per-minute request counts
)

// Collector accumulates request counts and latencies. The zero value is not
// usable — call New.
type Collector struct {
	mu        sync.Mutex
	total     uint64
	lat       []float64         // ring of recent latencies (ms); grows to latRingCap then overwrites
	latNext   int               // next overwrite index once the ring is full
	bucket    [tsMinutes]uint64 // per-minute request counts, indexed by minute % tsMinutes
	bucketMin [tsMinutes]int64  // the minute (unix/60) each bucket currently holds; -1 = empty
	now       func() time.Time  // time seam (overridable in tests)
}

// New returns an empty collector.
func New() *Collector {
	c := &Collector{lat: make([]float64, 0, latRingCap), now: time.Now}
	for i := range c.bucketMin {
		c.bucketMin[i] = -1
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
	minute := c.now().Unix() / 60
	idx := int(minute % tsMinutes)
	if c.bucketMin[idx] != minute { // reuse / reset a stale (or first-touch) bucket
		c.bucketMin[idx] = minute
		c.bucket[idx] = 0
	}
	c.bucket[idx]++
	c.mu.Unlock()
}

// Point is one minute of the request timeseries: T is the unix start-of-minute.
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
	Series    []Point `json:"series"`     // last WindowMin minutes, oldest → newest
	WindowMin int     `json:"window_min"` // length of Series
	Samples   int     `json:"samples"`    // latency samples backing the percentiles
}

// Snapshot computes the current aggregates. O(n log n) on the latency ring; the
// ring is bounded, so this is cheap and never blocks Observe for long.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	lat := make([]float64, len(c.lat))
	copy(lat, c.lat)
	total := c.total
	nowMin := c.now().Unix() / 60
	series := make([]Point, tsMinutes)
	for i := 0; i < tsMinutes; i++ {
		m := nowMin - int64(tsMinutes-1-i) // oldest → newest
		idx := int(((m % tsMinutes) + tsMinutes) % tsMinutes)
		var cnt uint64
		if c.bucketMin[idx] == m {
			cnt = c.bucket[idx]
		}
		series[i] = Point{T: m * 60, Count: cnt}
	}
	c.mu.Unlock()

	sort.Float64s(lat)
	s := Snapshot{Total: total, Series: series, WindowMin: tsMinutes, Samples: len(lat)}
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
