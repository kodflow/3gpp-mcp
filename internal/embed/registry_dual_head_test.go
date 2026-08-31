package embed

import "testing"

// TestDualHeadModelSharesTheDenseIdentity is the regression test for the reason an
// image can serve BOTH retrieval arms with ONE model.
//
// SparseCapable() reads the ACTIVE registry entry, so exactly one model is in play
// at serve time: a build whose active model is dense-only drops the sparse arm no
// matter what else is on disk, and the corpus's sparse postings become bytes
// nothing can query. The dual-head export is therefore the model to bake — but only
// if its DENSE half is interchangeable with the one the corpus was embedded with,
// because the serve-time guard compares identities and disables VSS on a mismatch.
//
// The bge-m3-sparse entry used to omit windowing and max_tokens, so it defaulted to
// truncate/1024 and stamped fd18ca72f577 against the corpus's 38067f8c6efe. Baking
// it in that state would have produced an image that serves neither arm well: the
// sparse one on, the dense one guarded off.
//
// That the two entries may claim one identity is not a convention, it was measured:
// re-embedding 20 clauses of TS 23.501 whose vectors are already in the corpus gave
// cos(bge-m3, bge-m3-sparse) = 1.000000, and both scored 0.999995 min / 0.999999
// avg against the stored vectors — the float32 round-trip, not a model difference.
func TestDualHeadModelSharesTheDenseIdentity(t *testing.T) {
	reg := loadRegistry()

	byName := map[string]ModelSpec{}
	for _, m := range reg.Models {
		byName[m.Name] = m
	}
	dense, ok := byName["bge-m3"]
	if !ok {
		t.Fatal("the built-in registry has no bge-m3 entry")
	}
	sparse, ok := byName["bge-m3-sparse"]
	if !ok {
		t.Fatal("the built-in registry has no bge-m3-sparse entry")
	}

	if got, want := sparse.embedParts().Identity(), dense.embedParts().Identity(); got != want {
		t.Errorf("bge-m3-sparse dense identity = %s, want %s (bge-m3).\n"+
			"An image baking the dual-head model would compute a client identity its own "+
			"corpus cannot match, and the serve guard would disable vector search.\n"+
			"windowing=%q/%q max_tokens=%d/%d precision=%q/%q revision=%q/%q",
			got, want,
			sparse.Windowing, dense.Windowing,
			sparse.MaxTokens, dense.MaxTokens,
			sparse.Precision, dense.Precision,
			sparse.Revision, dense.Revision)
	}

	// And it must still be the model that HAS a sparse head — otherwise the two
	// entries have been made identical by deleting the thing that distinguishes them.
	if sparse.SparseOutput == "" {
		t.Error("bge-m3-sparse declares no sparse_output — nothing would light up the sparse arm")
	}
	if dense.SparseOutput != "" {
		t.Error("bge-m3 declares a sparse_output; the dense-only entry is what proves the two identities are independent")
	}
}
