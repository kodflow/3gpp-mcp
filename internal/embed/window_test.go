package embed

import (
	"math"
	"strings"
	"testing"
)

func TestWindowText(t *testing.T) {
	short := "amf registration procedure"
	if w := windowText(short, 5); len(w) != 1 || w[0] != short {
		t.Fatalf("short text → single window, got %v", w)
	}
	long := strings.TrimSpace(strings.Repeat("x ", 700)) // 700 words
	w := windowText(long, 300)
	if len(w) != 3 { // 300 + 300 + 100
		t.Fatalf("700 words / 300 → 3 windows, got %d", len(w))
	}
	for _, ww := range w {
		if n := len(strings.Fields(ww)); n > 300 {
			t.Fatalf("window has %d words, want <= 300", n)
		}
	}
}

func TestMeanPoolL2(t *testing.T) {
	vecs := [][]float32{{1, 0, 0}, {0, 1, 0}}

	// One window → passthrough (unchanged).
	if got := meanPoolL2(vecs, []int{0}); len(got) != 3 || got[0] != 1 {
		t.Fatalf("single window should pass through, got %v", got)
	}
	// Empty → nil.
	if got := meanPoolL2(vecs, nil); got != nil {
		t.Fatalf("empty idx → nil, got %v", got)
	}
	// Mean of two orthogonal unit vectors → (.5,.5,0) → L2-renorm → unit norm,
	// equal positive components.
	got := meanPoolL2(vecs, []int{0, 1})
	var sum float64
	for _, x := range got {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1) > 1e-5 {
		t.Fatalf("pooled vector not unit norm: %v (|.|²=%v)", got, sum)
	}
	if got[0] <= 0 || got[0] != got[1] || got[2] != 0 {
		t.Fatalf("expected equal positive x/y, zero z, got %v", got)
	}
}
