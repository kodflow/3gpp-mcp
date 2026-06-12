package metrics

import (
	"testing"
	"time"
)

func TestObserveAndSnapshot(t *testing.T) {
	c := New()
	base := time.Unix(1_000_000*60, 0) // a fixed start-of-minute
	c.now = func() time.Time { return base }

	for i := 1; i <= 100; i++ {
		c.Observe(time.Duration(i) * time.Millisecond)
	}
	s := c.Snapshot()
	if s.Total != 100 {
		t.Errorf("total=%d, want 100", s.Total)
	}
	if s.Samples != 100 {
		t.Errorf("samples=%d, want 100", s.Samples)
	}
	// avg of 1..100 ms = 50.5
	if s.AvgMs < 50.0 || s.AvgMs > 51.0 {
		t.Errorf("avg=%.2f, want ~50.5", s.AvgMs)
	}
	// nearest-rank percentiles on 1..100 (sorted): p99 ≈ 100, p50 ≈ 50-51, p95 ≈ 95-96
	if s.P99Ms < 99 || s.P99Ms > 100 {
		t.Errorf("p99=%.1f, want ~100", s.P99Ms)
	}
	if s.P50Ms < 49 || s.P50Ms > 52 {
		t.Errorf("p50=%.1f, want ~50", s.P50Ms)
	}
	if s.P95Ms < 94 || s.P95Ms > 97 {
		t.Errorf("p95=%.1f, want ~95", s.P95Ms)
	}
	if len(s.Series) != tsMinutes || s.WindowMin != tsMinutes {
		t.Fatalf("series len=%d window=%d, want %d", len(s.Series), s.WindowMin, tsMinutes)
	}
	// All 100 requests landed in the current (newest) minute.
	last := s.Series[len(s.Series)-1]
	if last.Count != 100 {
		t.Errorf("newest minute count=%d, want 100", last.Count)
	}
	if last.T != base.Unix()/60*60 {
		t.Errorf("newest minute t=%d, want %d", last.T, base.Unix()/60*60)
	}
}

func TestRingBoundsMemory(t *testing.T) {
	c := New()
	base := time.Unix(2_000_000*60, 0)
	c.now = func() time.Time { return base }
	// Push more than the ring capacity; latency slice must stay capped.
	for i := 0; i < latRingCap+500; i++ {
		c.Observe(time.Millisecond)
	}
	s := c.Snapshot()
	if s.Total != uint64(latRingCap+500) {
		t.Errorf("total=%d, want %d (total is unbounded)", s.Total, latRingCap+500)
	}
	if s.Samples != latRingCap {
		t.Errorf("samples=%d, want %d (ring is bounded)", s.Samples, latRingCap)
	}
}

func TestTimeseriesRolls(t *testing.T) {
	c := New()
	cur := time.Unix(3_000_000*60, 0)
	c.now = func() time.Time { return cur }
	c.Observe(time.Millisecond) // minute 0
	cur = cur.Add(2 * time.Minute)
	c.Observe(time.Millisecond) // minute +2
	c.Observe(time.Millisecond)
	s := c.Snapshot()
	last := s.Series[len(s.Series)-1]
	if last.Count != 2 {
		t.Errorf("current minute=%d, want 2", last.Count)
	}
	// minute -1 (the gap) must read zero, minute -2 must read the first request.
	if got := s.Series[len(s.Series)-2].Count; got != 0 {
		t.Errorf("gap minute=%d, want 0", got)
	}
	if got := s.Series[len(s.Series)-3].Count; got != 1 {
		t.Errorf("minute -2=%d, want 1", got)
	}
}
