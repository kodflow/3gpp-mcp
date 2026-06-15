package metrics

import (
	"testing"
	"time"
)

func TestObserveAndSnapshot(t *testing.T) {
	c := New()
	base := time.Unix(1_000_000*secPerHour, 0) // a fixed start-of-hour
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
	if len(s.Series) != tsHours || s.WindowHrs != tsHours {
		t.Fatalf("series len=%d window=%d, want %d", len(s.Series), s.WindowHrs, tsHours)
	}
	// All 100 requests landed in the current (newest) hour.
	last := s.Series[len(s.Series)-1]
	if last.Count != 100 {
		t.Errorf("newest hour count=%d, want 100", last.Count)
	}
	if last.T != base.Unix()/secPerHour*secPerHour {
		t.Errorf("newest hour t=%d, want %d", last.T, base.Unix()/secPerHour*secPerHour)
	}
}

func TestRingBoundsMemory(t *testing.T) {
	c := New()
	base := time.Unix(2_000_000*secPerHour, 0)
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

func TestRecentNewestFirstAndClamp(t *testing.T) {
	c := New()
	base := time.Unix(4_000_000*secPerHour, 0)
	c.now = func() time.Time { return base }
	for i := 0; i < 5; i++ {
		c.Record(ReqLog{Method: "POST", Tool: "search_spec", Query: "q", DurMs: float64(i)})
	}
	// Default limit when non-positive.
	if got := c.Recent(0); len(got) != 5 {
		t.Fatalf("Recent(0) len=%d, want 5 (default)", len(got))
	}
	got := c.Recent(3)
	if len(got) != 3 {
		t.Fatalf("Recent(3) len=%d, want 3", len(got))
	}
	// Newest first: the last Recorded (DurMs=4) must come first.
	if got[0].DurMs != 4 || got[1].DurMs != 3 || got[2].DurMs != 2 {
		t.Errorf("order=%v, want newest-first [4 3 2]", []float64{got[0].DurMs, got[1].DurMs, got[2].DurMs})
	}
	// T is stamped from the clock when zero.
	if got[0].T != base.Unix() {
		t.Errorf("T=%d, want %d (stamped from clock)", got[0].T, base.Unix())
	}
}

func TestRecentRingEvictsOldest(t *testing.T) {
	c := New()
	base := time.Unix(5_000_000*secPerHour, 0)
	c.now = func() time.Time { return base }
	// Overfill the ring; only the last recentCap entries survive, newest first.
	for i := 0; i < recentCap+10; i++ {
		c.Record(ReqLog{Method: "POST", DurMs: float64(i)})
	}
	got := c.Recent(recentCap + 100) // clamp to recentCap
	if len(got) != recentCap {
		t.Fatalf("len=%d, want %d (ring bounded)", len(got), recentCap)
	}
	// Newest is the last Recorded (recentCap+9); oldest survivor is index 10.
	if got[0].DurMs != float64(recentCap+9) {
		t.Errorf("newest=%.0f, want %d", got[0].DurMs, recentCap+9)
	}
	if got[len(got)-1].DurMs != 10 {
		t.Errorf("oldest survivor=%.0f, want 10", got[len(got)-1].DurMs)
	}
}

func TestRecordTruncatesQuery(t *testing.T) {
	c := New()
	long := make([]rune, queryMax+50)
	for i := range long {
		long[i] = 'é' // multibyte, to exercise rune-safety
	}
	c.Record(ReqLog{Query: string(long)})
	got := c.Recent(1)
	if len([]rune(got[0].Query)) != queryMax+1 { // +1 for the ellipsis
		t.Errorf("query rune len=%d, want %d (truncated + ellipsis)", len([]rune(got[0].Query)), queryMax+1)
	}
}

func TestTimeseriesRolls(t *testing.T) {
	c := New()
	cur := time.Unix(3_000_000*secPerHour, 0)
	c.now = func() time.Time { return cur }
	c.Observe(time.Millisecond) // hour 0
	cur = cur.Add(2 * time.Hour)
	c.Observe(time.Millisecond) // hour +2
	c.Observe(time.Millisecond)
	s := c.Snapshot()
	last := s.Series[len(s.Series)-1]
	if last.Count != 2 {
		t.Errorf("current hour=%d, want 2", last.Count)
	}
	// hour -1 (the gap) must read zero, hour -2 must read the first request.
	if got := s.Series[len(s.Series)-2].Count; got != 0 {
		t.Errorf("gap hour=%d, want 0", got)
	}
	if got := s.Series[len(s.Series)-3].Count; got != 1 {
		t.Errorf("hour -2=%d, want 1", got)
	}
}
