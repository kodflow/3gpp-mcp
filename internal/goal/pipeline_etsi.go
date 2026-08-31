package goal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The ETSI half of the corpus.
//
// 3GPP and ETSI are complementary, not redundant. ETSI republishes 3GPP specs
// under its own numbering (TS 23.501 becomes ETSI TS 123 501) and indexing THAT
// would be the same text twice under two ids. What is worth having is ETSI's OWN
// deliverables — the Lawful Interception suite (103 221-1/-2 for X1/X2/X3,
// 103 280 for the common parameter dictionary, 103 120 for HI1/ADMF,
// 102 232-x for HI2/HI3 delivery). 3GPP's LI specs, TS 33.127 and 33.128, do not
// restate those interfaces: they PROFILE them. A question about lawful
// interception is unanswerable from either corpus alone.
//
// These steps are part of the DEFAULT pipeline, not an opt-in. The two corpora
// are always built together, so a `goal run` can never leave one of them silently
// behind — which is exactly what happened while the ETSI tooling existed but was
// wired to nothing.
//
// The two indexes stay SEPARATE (data/etsi.duckdb next to data/3gpp.duckdb).
// cmd/server serves them side by side and routes by id shape; merging them would
// blur provenance, and provenance is the product.

// stepDiscoverETSI resolves the ETSI deliverable work list.
func stepDiscoverETSI() *Step {
	return &Step{
		Name:    "discover-etsi",
		Version: 1,
		Doc:     "resolve the ETSI deliverable work list from the /deliver archive",
		Deps:    []string{"build-go"},
		Impl:    []string{"cmd/discover-etsi", "internal/etsicat"},
		Extra: func(c *Ctx) (map[string]string, error) {
			return map[string]string{"etsi_scope": c.Cfg("etsi_scope")}, nil
		},
		Outputs: func(c *Ctx) []string { return []string{c.statePath("etsi-worklist.tsv")} },
		Validate: func(c *Ctx) error {
			if countLines(c.statePath("etsi-worklist.tsv")) == 0 {
				return fmt.Errorf("the ETSI work list is empty — discover resolved nothing")
			}
			return nil
		},
		Run: func(c *Ctx) error {
			args := append([]string{"--emit-worklist"}, etsiScopeArgs(c.Cfg("etsi_scope"))...)
			out, err := c.Output(Cmd{Name: c.bin("discover-etsi"), Args: args})
			if err != nil {
				return err
			}
			if err := WriteAtomic(c.statePath("etsi-worklist.tsv"), []byte(out+"\n")); err != nil {
				return err
			}
			n := countLines(c.statePath("etsi-worklist.tsv"))
			c.Log.Printf("ETSI work list: %d deliverable(s)", n)
			c.Checkpoint("etsi_deliverables", strconv.Itoa(n))
			return nil
		},
	}
}

// stepCorpusETSI downloads, converts and ingests the ETSI deliverables.
//
// Acquisition and ingestion are one step rather than three because
// scripts/etsi-corpus.sh already streams them per deliverable (download →
// pdftotext → HTML → ingest at the end) and is itself resumable: an existing
// converted HTML is skipped. Splitting it would mean rewriting that streaming
// into the pipeline for no gain.
func stepCorpusETSI() *Step {
	return &Step{
		Name:    "corpus-etsi",
		Version: 1,
		Doc:     "download, extract and ingest the ETSI deliverables into data/etsi.duckdb",
		Deps:    []string{"discover-etsi", "build-rust"},
		Impl:    []string{"scripts/etsi-corpus.sh", "scripts/lib/convert.sh", "rust/ingest/src"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.statePath("etsi-worklist.tsv")}, nil
		},
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{c.dataPath("etsi.duckdb")} },
		Validate: func(c *Ctx) error {
			// The DB must open AND hold clauses. An ETSI DuckDB with a schema and no
			// rows is what a run produces when every PDF failed its text-layer check,
			// and it would serve as an empty corpus without complaining.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("etsi.duckdb")}})
			if err != nil {
				return fmt.Errorf("the ETSI DB does not open: %w", err)
			}
			n := countFiles(c.dataPath("sources", "convert-etsi"), ".html")
			if n == 0 {
				return fmt.Errorf("no converted ETSI HTML under %s", c.dataPath("sources", "convert-etsi"))
			}
			c.Log.Printf("ETSI corpus: %d converted deliverable(s); %s", n, firstLine(out))
			return nil
		},
		Run: func(c *Ctx) error {
			if _, err := c.Output(Cmd{Name: "pdftotext", Args: []string{"-v"}}); err != nil {
				// pdftotext -v exits non-zero on some builds while still printing a
				// version, so only a missing binary is fatal.
				if _, lookErr := lookPath("pdftotext"); lookErr != nil {
					return fmt.Errorf("pdftotext (poppler/xpdf) is required to read ETSI PDFs and is not on PATH: %w", lookErr)
				}
			}
			env := []string{
				"DISCOVER_ETSI_BIN=" + c.bin("discover-etsi"),
				"INGEST_BIN=" + c.rbin("ingest"),
				"ETSI_OUT=" + c.dataPath("etsi.duckdb"),
				"ETSI_CONVERT=" + c.dataPath("sources", "convert-etsi"),
				"ETSI_ORIGIN=" + c.dataPath("sources", "etsi-origin"),
			}
			env = append(env, etsiScopeEnv(c.Cfg("etsi_scope"))...)
			c.Log.Printf("building the ETSI corpus (PDF text layer, never OCR)")
			return c.Run(Cmd{Name: "bash", Args: []string{"scripts/etsi-corpus.sh"}, Env: env, Echo: true})
		},
	}
}

// ScopeAll is the etsi_scope value that widens the ETSI half from the built-in
// Lawful-Interception suite to the WHOLE /deliver archive (etsi_ts + etsi_tr +
// etsi_en) — thousands of deliverables rather than fourteen.
//
// It is a value of the knob rather than a second knob because the knob already
// existed and was DEAD: both steps read c.Cfg("etsi_scope"), and nothing ever put
// an "etsi_scope" key into Ctx.Config, so the ETSI corpus was pinned to the
// fourteen built-in LI specs with no reachable way to widen it. cmd/discover-etsi
// has carried --all, and scripts/etsi-corpus.sh has carried ETSI_ALL, the whole
// time; only the path from the operator to them was missing.
const ScopeAll = "all"

// etsiScopeArgs turns the scope knob into cmd/discover-etsi flags.
func etsiScopeArgs(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "":
		return nil // the built-in LI suite
	case ScopeAll:
		return []string{"--all"}
	default:
		return []string{"--specs", scope}
	}
}

// etsiScopeEnv turns the same knob into the environment scripts/etsi-corpus.sh
// reads. The script passes these straight through to the same binary, so the two
// helpers must agree — which is why they sit next to each other.
func etsiScopeEnv(scope string) []string {
	switch strings.TrimSpace(scope) {
	case "":
		return nil
	case ScopeAll:
		return []string{"ETSI_ALL=1"}
	default:
		return []string{"ETSI_SPECS=" + scope}
	}
}

// lookPath tells "the tool is absent" apart from "the tool answered oddly" — the
// distinction this pipeline keeps getting wrong when it does not make it explicit.
func lookPath(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(p); statErr != nil {
		return "", statErr
	}
	return filepath.Clean(p), nil
}
