package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeFTS struct {
	err   error
	avail bool
}

func (f fakeFTS) LoadFTS(context.Context) error { return f.err }
func (f fakeFTS) FTSAvailable() bool            { return f.avail }

// TestRequireFTSRefusesToScoreAFallback pins the review finding on #324: when FTS
// is not usable SearchClauses falls back to token matching, and the bench scored
// that under the "lexical (BM25)" label — a quality gate approving measurements of
// an engine the corpus does not serve. Both ways FTS can be unusable are refused:
// the extension failing to load, and the extension loading onto a corpus with no
// index schema (a nil error with FTSAvailable false).
func TestRequireFTSRefusesToScoreAFallback(t *testing.T) {
	ctx := context.Background()
	if err := requireFTS(ctx, fakeFTS{err: errors.New("fts \"LOAD fts\": boom")}, "x.duckdb"); err == nil ||
		!strings.Contains(err.Error(), "BM25") {
		t.Errorf("a failed LoadFTS must refuse the run, naming the BM25 label; got %v", err)
	}
	if err := requireFTS(ctx, fakeFTS{avail: false}, "x.duckdb"); err == nil ||
		!strings.Contains(err.Error(), "no usable FTS index") {
		t.Errorf("a loaded extension with no index must refuse too — a nil error is not an index; got %v", err)
	}
	if err := requireFTS(ctx, fakeFTS{avail: true}, "x.duckdb"); err != nil {
		t.Errorf("a usable FTS index must be accepted, or the gate refuses every build; got %v", err)
	}
}
