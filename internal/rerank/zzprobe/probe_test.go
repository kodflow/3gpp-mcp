package zzprobe

import (
	"testing"

	"github.com/sugarme/tokenizer/pretrained"
)

func TestProbe(t *testing.T) {
	tok, err := pretrained.FromFile("C:/Users/Public/3gpp-mcp/data/models/bge-reranker-v2-m3/tokenizer.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a  b", "a\nb", "a\nb  c", "a \nb", "a\n b", "Test procedure\nStep Direction                                    Description", "Step Direction    Description", "x\ty", "a\n\nb", "a \n b", "a  \nb", "1\nDVB-SH mode     msec", "8 8 8 8\nServices   YES", "a\u00a0\u00a0b", "a\tb c", "ab cd\nef"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("PANIC %q: %v", p, r)
				}
			}()
			if _, err := tok.EncodeSingle(p, true); err != nil {
				t.Logf("err %q: %v", p, err)
				return
			}
			t.Logf("ok %q", p)
		}()
	}
}
