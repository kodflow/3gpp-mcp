package goal

import (
	"path/filepath"
	"strings"
	"testing"
)

// THE SPLIT IS ONLY WORTH ANYTHING IF THE TWO HALVES STOP INVALIDATING EACH OTHER.
//
// `fetch-etsi` and `corpus-etsi` were one step until 2026-09-07, so one Impl list
// covered both the downloader and the Rust parser. Build 24 paid for it:
//
//	STEP corpus-etsi
//	  reason  implementation changed: rust/store/src/lib.rs
//
// rust/store/src/lib.rs cannot alter one downloaded byte. The reverse was true at
// the same time: editing scripts/etsi-corpus.sh re-derived every clause.
//
// Both directions are asserted, and so is the fact that each step still re-runs
// for its OWN sources — narrowing too far turns a loud waste into a corpus that is
// silently stale, which is the worse failure of the two.
func TestTheETSIFetchAndIngestNoLongerInvalidateEachOther(t *testing.T) {
	fetch, ingest := stepFetchETSI(), stepCorpusETSI()

	for _, tc := range []struct {
		name    string
		step    *Step
		file    string
		replays bool
		why     string
	}{
		{
			name: "the ingest ignores the download script", step: ingest,
			file: "scripts/etsi-fetch.sh", replays: false,
			why: "it never runs it; a download-loop edit would re-derive every ETSI clause",
		},
		{
			name: "the ingest ignores the PDF converter", step: ingest,
			file: "scripts/lib/convert.sh", replays: false,
			why: "convert_pdf is called by the fetch and is not even sourced by the shared prelude",
		},
		{
			name: "the fetch ignores the store library", step: fetch,
			file: "rust/store/src/lib.rs", replays: false,
			why: "measured on build 24: this is what re-ran the whole ETSI half for nothing",
		},
		{
			name: "the fetch ignores the ingest binary", step: fetch,
			file: "rust/ingest/src/main.rs", replays: false,
			why: "the fetch runs cmd/discover-etsi and never the Rust ingest",
		},
		{
			name: "the fetch still re-runs for its own script", step: fetch,
			file: "scripts/etsi-fetch.sh", replays: true,
			why: "the download loop decides which bytes land on disk",
		},
		{
			name: "the fetch still re-runs for the converter", step: fetch,
			file: "scripts/lib/convert.sh", replays: true,
			why: "convert_pdf decides the HTML the ingest will read",
		},
		{
			name: "the ingest still re-runs for its own script", step: ingest,
			file: "scripts/etsi-ingest.sh", replays: true,
			why: "it is the command line that writes the corpus",
		},
		{
			name: "the ingest still re-runs for the store library", step: ingest,
			file: "rust/store/src/lib.rs", replays: true,
			why: "store-rs writes every clause; a change there produces different rows from identical HTML",
		},
		{
			name: "the ingest still re-runs for the parser", step: ingest,
			file: "rust/parse/placeholder.rs", replays: true,
			why: "parse3gpp turns the HTML into clauses",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			implFixture(t, root, tc.step.Impl)
			// The file must exist either way, or "unchanged" would only mean "absent"
			// and every negative case would pass for the wrong reason.
			write(t, filepath.Join(root, filepath.FromSlash(tc.file)), "before\n")

			before, _, err := implHash(root, tc.step.Impl, false)
			if err != nil {
				t.Fatal(err)
			}
			after := implAfterWriting(t, root, tc.step.Impl, tc.file, "after\n")

			switch {
			case tc.replays && before == after:
				t.Fatalf("%s does NOT re-run when %s changes — %s", tc.step.Name, tc.file, tc.why)
			case !tc.replays && before != after:
				t.Fatalf("%s re-runs when %s changes — %s", tc.step.Name, tc.file, tc.why)
			}
		})
	}
}

// The ingest must read what the fetch actually PRODUCED, not what the work list
// said should be fetched. The work list is a prediction; the converted tree is the
// fact, and a deliverable that failed to convert is exactly the difference.
func TestTheIngestTakesTheConvertedTreeAsItsInput(t *testing.T) {
	c := &Ctx{Root: t.TempDir()}
	in, err := stepCorpusETSI().Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(in, string(filepath.ListSeparator))
	if !strings.Contains(joined, filepath.FromSlash("sources/convert-etsi")) {
		t.Errorf("corpus-etsi does not take the converted tree as input: %v", in)
	}
	if strings.Contains(joined, "etsi-worklist") {
		t.Errorf("corpus-etsi still keys on the work list, which is what SHOULD have been "+
			"fetched rather than what was: %v", in)
	}
}

// The two steps must not both claim to produce the corpus, and the fetch must not
// claim outputs it does not declare — `fetch` on the 3GPP side declares none for
// the same reason: enumerating a converted tree makes the fingerprint enormous.
func TestOnlyTheIngestProducesTheETSICorpus(t *testing.T) {
	c := &Ctx{Root: t.TempDir()}
	if out := stepFetchETSI().Outputs(c); len(out) != 0 {
		t.Errorf("fetch-etsi declares outputs (%v); `fetch` declares none, for the same reason", out)
	}
	out := stepCorpusETSI().Outputs(c)
	if len(out) != 1 || !strings.HasSuffix(out[0], "etsi.duckdb") {
		t.Errorf("corpus-etsi should produce exactly data/etsi.duckdb, got %v", out)
	}
}

// The DAG must actually run the fetch first. A dependency that is merely implied
// by the file layout is not a dependency.
func TestTheIngestDependsOnTheFetch(t *testing.T) {
	deps := stepCorpusETSI().Deps
	var found bool
	for _, d := range deps {
		if d == "fetch-etsi" {
			found = true
		}
		if d == "discover-etsi" {
			t.Errorf("corpus-etsi still depends on discover-etsi directly; that is the fetch's "+
				"dependency now, and keeping it here re-couples the two halves: %v", deps)
		}
	}
	if !found {
		t.Fatalf("corpus-etsi does not depend on fetch-etsi: %v", deps)
	}
}
