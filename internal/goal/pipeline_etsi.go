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
		// The Run below writes this file and nothing else. An ETSI catalogue that
		// enumerates to the same deliverables must not replay corpus-etsi, which is
		// hours of download and PDF conversion over a corpus that has since been
		// content-addressed and compacted.
		OutputsComplete: true,
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
		Version: 2,
		Doc:     "download, extract and ingest the ETSI deliverables into data/etsi.duckdb",
		Deps:    []string{"discover-etsi", "build-rust"},
		// NAMED FILES, NOT THE CRATE. This step runs exactly one binary, `ingest`,
		// whose source is rust/ingest/src/main.rs. It never invokes anything from
		// rust/ingest/src/bin — ETSI has no Lawful-Interception registry, no 5GC
		// OpenAPI overlay and no DynaReport catalogue. Declaring the directory made
		// a fix to ingest_li.rs invalidate the whole ETSI half: measured 2026-09-06,
		// ~1 h of rework and 18.8 GiB re-pushed for a file this step cannot reach.
		//
		// rust/parse and rust/store/src are ADDED, not kept: they were missing, and
		// that is the opposite mistake. `ingest` parses ETSI HTML with parse3gpp and
		// writes it with store-rs, so a change in either produces different clauses
		// from identical input — and this step would have kept the old ones without
		// a word. The 3GPP `ingest` step already declares both; this is the same
		// declaration, minus the binaries neither of them runs.
		Impl: []string{
			"scripts/etsi-corpus.sh", "scripts/lib/convert.sh",
			// The binary this step runs, and its manifest.
			"rust/ingest/src/main.rs", "rust/ingest/Cargo.toml",
			// The crates it links. rust/store/src/lib.rs, NOT rust/store/src, which
			// also holds src/bin — binaries this step never runs.
			"rust/parse", "rust/store/src/lib.rs", "rust/store/Cargo.toml",
			"rust/identity",
			// The workspace manifest and LOCKFILE: `cargo update` alone can change
			// the binary, and build-rust is a Tool that never replays a data step.
			"rust/Cargo.toml", "rust/Cargo.lock",
			"internal/store/schema.sql",
		},
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
				return stillOpenElsewhere("etsi.duckdb",
					fmt.Errorf("the ETSI DB does not open: %w", err))
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

			// THIS STEP WRITES CLAUSES, so it needs the corpus in write shape.
			//
			// A converted corpus (ADR 0004) serves `clauses` as a VIEW over the
			// occurrences, and DuckDB answers an INSERT into a view with "Catalog
			// Error: clauses is not a table". The ETSI ingest runs one transaction
			// across every deliverable, so that first error aborted the transaction
			// and every deliverable after it failed with "Current transaction is
			// aborted" — a whole ETSI pass lost to one unrestored view.
			//
			// The 3GPP half has always called this before folding; the ETSI half was
			// written before its corpus was ever converted, and the requirement was
			// never carried across. It surfaced the first time corpus-etsi ran after
			// paragraphs-etsi (2026-09-03), not because either step changed.
			if err := ensureWriteShape(c, c.dataPath("etsi.duckdb")); err != nil {
				return fmt.Errorf("the ETSI corpus could not be put back into write shape: %w", err)
			}

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

// ScopeAllVersions is ScopeAll plus every PUBLISHED VERSION of each deliverable
// rather than only the latest.
//
// It is what makes the ETSI half comparable to the 3GPP one, which already keeps
// every release of every spec so a reader can see what changed. TS 103 221-1
// alone has 23 published versions, so this multiplies the work list several-fold
// — the download, the conversion and the GPU pass with it. A separate value
// rather than the default, because that cost is a decision.
const ScopeAllVersions = "all-versions"

// etsiScopeArgs turns the scope knob into cmd/discover-etsi flags.
//
// The value is trimmed ONCE and the trimmed value is what travels. Trimming only
// for the dispatch and then forwarding the original passed " 103 280 " through to
// --specs, where the leading space becomes part of the first id and resolves
// nothing.
func etsiScopeArgs(scope string) []string {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "":
		return nil // the built-in LI suite
	case ScopeAll:
		return []string{"--all"}
	case ScopeAllVersions:
		return []string{"--all", "--all-versions"}
	default:
		return []string{"--specs", scope}
	}
}

// etsiScopeEnv turns the same knob into the environment scripts/etsi-corpus.sh
// reads. The script passes these straight through to the same binary, so the two
// helpers must agree — which is why they sit next to each other.
func etsiScopeEnv(scope string) []string {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "":
		return nil
	case ScopeAll:
		return []string{"ETSI_ALL=1"}
	case ScopeAllVersions:
		return []string{"ETSI_ALL=1", "ETSI_ALL_VERSIONS=1"}
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
