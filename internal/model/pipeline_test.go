package model

import "testing"

func TestPipelineVersion(t *testing.T) {
	lexical := PipelineVersion("")
	bge := PipelineVersion("bge-m3")
	local := PipelineVersion("hash-local")

	if lexical == bge || bge == local || lexical == local {
		t.Fatalf("embedding model must change the pipeline version: lexical=%s bge=%s local=%s", lexical, bge, local)
	}
	if PipelineVersion("bge-m3") != bge {
		t.Fatal("PipelineVersion must be deterministic")
	}
	if len(bge) != 12 {
		t.Fatalf("want a 12-char digest, got %d (%q)", len(bge), bge)
	}
}
