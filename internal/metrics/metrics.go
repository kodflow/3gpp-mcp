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
)

// Collector accumulates request counts and latencies. The zero value is not
// usable — call New.
type Collector struct {
	mu       sync.Mutex
	total    uint64
	lat      []float64       // ring of recent latencies (ms); grows to latRingCap then overwrites
	latNext  int             // next overwrite index once the ring is full
	bucket   [tsHours]uint64 // per-hour request counts, indexed by hour % tsHours
	bucketHr [tsHours]int64  // the hour (unix/3600) each bucket currently holds; -1 = empty
	now      func() time.Time
}

// New returns an empty collector.
func New() *Collector {
	c := &Collector{lat: make([]float64, 0, latRingCap), now: time.Now}
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
