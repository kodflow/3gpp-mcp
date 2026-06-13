package embed

import (
	"context"
	"testing"
)

func TestToSparseDedupAndSpecials(t *testing.T) {
	// positions: ids 0(cls),5,5,2(eos),7 ; weights .9,.3,.8,.5,0
	ids := []uint32{0, 5, 5, 2, 7}
	w := []float32{0.9, 0.3, 0.8, 0.5, 0.0}
	got := toSparse(ids, w, nil)
	// id 0 (cls) + id 2 (eos) dropped; id 5 keeps MAX(.3,.8)=.8; id 7 w<=0 dropped.
	if len(got) != 1 {
		t.Fatalf("sparse=%v, want only {5:0.8}", got)
	}
	if got[5] != 0.8 {
		t.Errorf("id 5 weight=%v, want 0.8 (max-pool)", got[5])
	}
}

func TestLocalEmbedSparseDeterministicAndShared(t *testing.T) {
	var l Local
	ctx := context.Background()
	a, err := l.EmbedSparse(ctx, []string{"AMF registration procedure", "AMF registration procedure"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || len(a[0]) == 0 {
		t.Fatalf("got %d vecs, first len %d", len(a), len(a[0]))
	}
	// Deterministic: same text → identical sparse vec.
	if len(a[0]) != len(a[1]) {
		t.Fatalf("non-deterministic sparse: %d vs %d terms", len(a[0]), len(a[1]))
	}
	for k, v := range a[0] {
		if a[1][k] != v {
			t.Fatalf("non-deterministic weight for term %d: %v vs %v", k, v, a[1][k])
		}
		if v <= 0 || v >= 1 {
			t.Errorf("weight %v out of expected (0,1) range", v)
		}
		if xlmrSpecialTokens[k] {
			t.Errorf("term %d is a special token, must be excluded", k)
		}
	}
	// Local satisfies the SparseEmbedder interface.
	var _ SparseEmbedder = Local{}
}
