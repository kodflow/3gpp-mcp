package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE ETSI INGEST DECLINES WHEN IT HAS NOTHING TO ADD, and only then.

func TestTheETSIIngestPlanIsReadFromTheBinarysLine(t *testing.T) {
	p := parseETSIIngestPlan("noise\ningest-plan: ETSI pending_docs=19 pending_clauses=0 hnsw_state=frozen corpus=present files=11822\n")
	want := etsiIngestPlan{ok: true, pendingDocs: 19, pendingClauses: 0, hnswState: "frozen", corpusPresent: true, files: 11822}
	p.raw = ""
	if p != want {
		t.Fatalf("parsed %+v, want %+v", p, want)
	}
	if parseETSIIngestPlan("ingest: something else entirely").ok {
		t.Fatal("an unrelated line was read as a plan")
	}
}

func TestTheETSIIngestDeclinesOnlyOverAnUntouchedCorpusWithNothingToAdd(t *testing.T) {
	clean := etsiIngestPlan{ok: true, pendingDocs: 19, pendingClauses: 0, hnswState: "frozen", corpusPresent: true, files: 11822}
	if etsiIngestDeclines(clean) == "" {
		t.Fatal("a frozen corpus holding every deliverable that yields a clause did not decline: " +
			"the ETSI chain replays for 19 files that parse to nothing")
	}
	for _, tc := range []struct {
		name string
		mut  func(*etsiIngestPlan)
	}{
		{"unreadable plan", func(p *etsiIngestPlan) { p.ok = false }},
		{"no corpus yet", func(p *etsiIngestPlan) { p.corpusPresent = false }},
		{"a new deliverable with text", func(p *etsiIngestPlan) { p.pendingClauses = 42 }},
		{"a pass since the last freeze", func(p *etsiIngestPlan) { p.hnswState = "building" }},
		{"no index state at all", func(p *etsiIngestPlan) { p.hnswState = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := clean
			tc.mut(&p)
			if why := etsiIngestDeclines(p); why != "" {
				t.Fatalf("declined (%s) where the ingest has work or the file cannot be vouched for", why)
			}
		})
	}
}

// THE QUESTION IS ASKED BEFORE THE FILE IS TOUCHED. Declining after the restore
// would be worse than not declining: the restore undoes paragraphs-etsi's
// conversion, and a decline carries the old provenance forward, so nothing
// downstream would convert it back. Read from the source because the order of two
// calls is the whole property.
func TestTheETSIIngestPlansBeforeItRestoresTheWriteShape(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("pipeline_etsi.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	start := strings.Index(src, "func stepIngestETSI() *Step {")
	end := strings.Index(src[start:], "\n}\n")
	body := src[start : start+end]
	plan := strings.Index(body, "planETSIIngest(c)")
	restore := strings.Index(body, "ensureWriteShape(c,")
	if plan < 0 || restore < 0 {
		t.Fatalf("cannot find both calls in stepIngestETSI (plan=%d restore=%d)", plan, restore)
	}
	if plan > restore {
		t.Fatal("stepIngestETSI restores the write shape before asking whether there is anything to write")
	}
}
