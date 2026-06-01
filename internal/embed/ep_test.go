package embed_test

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// TestExecutionProviderSelection locks the ORT_EP → execution-provider mapping.
// It is intentionally tag-free (no `onnx`, no real GPU): it exercises ONLY the
// selection logic that decides cpu vs cuda, which is the seam the Kaggle GPU POC
// flips. The CUDA SessionOptions wiring itself is compile-checked by the onnx
// build and validated at runtime on a GPU box (operator-driven, see
// scripts/kaggle-embed-poc.sh), never here.
func TestExecutionProviderSelection(t *testing.T) {
	// All cases set ORT_EP via t.Setenv (which auto-restores). The unset case is
	// behaviourally identical to empty (TrimSpace("")=="" → cpu), so empty stands
	// in for it without mutating the ambient environment.
	cases := []struct {
		name string
		val  string
		want string
	}{
		{name: "empty (==unset) defaults to cpu", val: "", want: embed.EPCPU},
		{name: "explicit cpu", val: "cpu", want: embed.EPCPU},
		{name: "explicit cuda", val: "cuda", want: embed.EPCUDA},
		{name: "cuda is case-insensitive", val: "CUDA", want: embed.EPCUDA},
		{name: "cuda tolerates whitespace", val: "  cuda ", want: embed.EPCUDA},
		{name: "garbage falls back to cpu", val: "tensorrt", want: embed.EPCPU},
		{name: "typo falls back to cpu", val: "guda", want: embed.EPCPU},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ORT_EP", tc.val)
			if got := embed.ExecutionProvider(); got != tc.want {
				t.Fatalf("ORT_EP=%q: ExecutionProvider()=%q, want %q", tc.val, got, tc.want)
			}
		})
	}
}
