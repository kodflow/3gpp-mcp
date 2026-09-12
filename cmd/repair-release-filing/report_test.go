package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// The decisions belong to internal/refile and are tested there. What is this
// binary's own is the two report forms, so that is what these cover.
//
// The fixture is the smallest corpus with one move and one refusal: 26.510 filed
// at 18.4.0 under Rel-20 with text, and under Rel-18 as a bookkeeping row with
// none, plus a Rel-4 filing of a Rel-99 document that must stay where it is.
func fixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "c.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO bodies (body_id, heading) VALUES (1, 'Scope')`,
		`INSERT INTO spec_versions (spec_id, release, version, docx_url) VALUES
			('26.510','Rel-18','18.4.0',''),
			('26.510','Rel-20','18.4.0','https://x/26510-i40.zip'),
			('21.810','Rel-4','3.0.0','https://x/21810-300.zip')`,
		`INSERT INTO clause_occ (chunk_id, spec_id, release, version, clause_path, is_normative, body_id) VALUES
			(1,'26.510','Rel-20','18.4.0','1',true,1),
			(2,'21.810','Rel-4','3.0.0','1',true,1)`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = st.Close()
	return dbPath
}

// THE TEXT REPORT NAMES EVERY DECISION, and a refusal always carries its reason:
// a repair that skips silently cannot be told apart from one that misses silently.
func TestTheTextReportNamesEveryDecision(t *testing.T) {
	db := fixture(t)
	var buf bytes.Buffer
	if _, err := run(db, false, false, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"MOVE 26.510      Rel-20  18.4.0    -> Rel-18",
		"KEEP 21.810      Rel-4   3.0.0        the corpus files 21.810 under no Rel-99",
		"1 to move, 1 occurrence(s); 1 left alone",
		"DRY RUN — pass --apply to write",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report must contain %q; got:\n%s", want, out)
		}
	}
}

// --report json is the documented machine-readable form (cmd/CLAUDE.md), and the
// text report must not also appear on the same stream — a caller parsing stdout
// would get neither.
func TestTheJSONReportCarriesEveryDecision(t *testing.T) {
	db := fixture(t)
	var buf bytes.Buffer
	res, err := run(db, false, true, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("json mode must not also print the text report; got %q", buf.String())
	}
	var enc bytes.Buffer
	if err := writeJSONTo(&enc, res); err != nil {
		t.Fatal(err)
	}
	var got result
	if err := json.Unmarshal(enc.Bytes(), &got); err != nil {
		t.Fatalf("the report must parse as json: %v\n%s", err, enc.String())
	}
	if got.Candidates != len(got.Filings) {
		t.Errorf("every candidate must carry a decision: %d candidates, %d filings", got.Candidates, len(got.Filings))
	}
	if got.Moved != 0 || got.Applied {
		t.Errorf("a dry run has moved nothing, got moved=%d applied=%v", got.Moved, got.Applied)
	}
	if got.Occurrences != 1 || got.LeftAlone != 1 {
		t.Errorf("got occurrences=%d left_alone=%d", got.Occurrences, got.LeftAlone)
	}
	for _, f := range got.Filings {
		switch f.Action {
		case "move":
			if f.Destination == "" {
				t.Errorf("a move must say what it does with the destination filing: %+v", f)
			}
		case "keep":
			if f.Reason == "" {
				t.Errorf("a refusal without a reason is indistinguishable from a miss: %+v", f)
			}
		default:
			t.Errorf("unknown action %q", f.Action)
		}
	}
}

// A FAILURE IS A RESULT TOO: `--report json` promises a self-contained response on
// every path, so the error belongs inside the object. A caller parsing stdout on
// the failure path would otherwise get nothing at all.
func TestTheJSONReportCarriesTheError(t *testing.T) {
	var buf bytes.Buffer
	res, err := run(filepath.Join(t.TempDir(), "does-not-exist.duckdb"), false, true, &buf)
	if err == nil {
		t.Fatal("opening a corpus that does not exist must fail")
	}
	if res == nil {
		t.Fatal("run must return a result to carry the error even when it fails")
	}
	res.Error = err.Error()
	var enc bytes.Buffer
	if err := writeJSONTo(&enc, res); err != nil {
		t.Fatal(err)
	}
	var got result
	if err := json.Unmarshal(enc.Bytes(), &got); err != nil {
		t.Fatalf("the failure report must parse as json: %v\n%s", err, enc.String())
	}
	if got.Error == "" {
		t.Errorf("the failure report must name the reason; got %+v", got)
	}
}
