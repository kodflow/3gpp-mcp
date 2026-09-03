package goal

import (
	"os"
	"strings"
	"testing"
)

// TestEveryStepThatWritesClausesRestoresTheWriteShape.
//
// A converted corpus (ADR 0004) serves `clauses` as a VIEW, and DuckDB answers
// an INSERT into a view with "Catalog Error: clauses is not a table". The ETSI
// ingest runs ONE transaction across every deliverable, so that single error
// aborted the transaction and every deliverable behind it failed with "Current
// transaction is aborted" — a whole ETSI pass lost, measured on 2026-09-03.
//
// The 3GPP half had always called ensureWriteShape before folding. The ETSI half
// was written before its corpus was ever converted, so the requirement was never
// carried across, and nothing failed until corpus-etsi first ran after
// paragraphs-etsi had converted the ETSI corpus. Neither step had changed.
//
// This pins the rule by source, because the failure only appears in a
// combination of steps that a unit test cannot cheaply reproduce: any step whose
// Run writes clauses must call ensureWriteShape first.
func TestEveryStepThatWritesClausesRestoresTheWriteShape(t *testing.T) {
	for _, f := range []string{
		"pipeline_etsi.go",  // corpus-etsi ingests ETSI deliverables
		"pipeline_steps.go", // merge folds the 3GPP shards
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "ensureWriteShape(c,") {
			t.Errorf("%s writes clauses but never calls ensureWriteShape: a converted corpus will "+
				"reject the INSERT and abort the whole transaction", f)
		}
	}
}

// TestTheETSIStepSaysItChanged. corpus-etsi does something new — it restores the
// write shape — and the orchestrator's own Go source is deliberately not
// provenance, so Version is the only thing that can invalidate it.
func TestTheETSIStepSaysItChanged(t *testing.T) {
	if v := stepCorpusETSI().Version; v < 2 {
		t.Errorf("corpus-etsi changed what it does but still declares Version %d", v)
	}
}
