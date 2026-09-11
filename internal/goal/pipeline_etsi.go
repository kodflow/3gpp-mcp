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

// stepDiscoverETSI resolves the ETSI deliverable work list. It is the ETSI
// analogue of `discover`.

func stepDiscoverETSI() *Step {
	return &Step{
		Name:    "discover-etsi",
		Version: 1,
		Doc:     "resolve the ETSI deliverable work list from the /deliver archive",
		// seed-etsi, for the reason discover names seed: a snapshot that already
		// holds these deliverables is work this step must not re-enumerate.
		Deps: []string{"build-go", "seed-etsi"},
		Impl: []string{"cmd/discover-etsi", "internal/etsicat"},
		// THE RESOLVED SCOPE, NOT THE KNOB. Recording c.Cfg("etsi_scope") records
		// what the operator TYPED, and the empty string is the value almost every
		// run carries — so the day the empty string stopped meaning "the fourteen
		// built-in LI deliverables" and started meaning "the whole archive, every
		// version", the recorded determinant did not move and this step SKIPPED.
		// The fix shipped, every gate stayed green, and the ETSI half went on being
		// discovered exactly as narrowly as before. Measured 2026-09-08 21:11.
		//
		// A determinant has to name what the step will DO. etsiScopeArgs is that,
		// and it is the same function the Run below hands to the binary, so the two
		// cannot drift.
		Extra: func(c *Ctx) (map[string]string, error) {
			return map[string]string{
				"etsi_scope": strings.Join(etsiScopeArgs(c.Cfg("etsi_scope")), " "),
			}, nil
		},
		Outputs: func(c *Ctx) []string { return []string{c.statePath("etsi-worklist.tsv")} },
		// The Run below writes this file and nothing else. An ETSI catalogue that
		// enumerates to the same deliverables must not replay ingest-etsi, which is
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

// stepFetchETSI downloads and converts the ETSI deliverables to HTML. It is the
// ETSI analogue of `fetch`, and it exists because acquisition and ingestion used
// to be ONE step.
//
// THE COMMENT THIS REPLACES SAID "splitting it would mean rewriting that
// streaming into the pipeline for no gain", and it was wrong on both halves.
//
// There was no streaming to rewrite: scripts/etsi-corpus.sh ran the whole
// download loop to completion and only then called the ingest once. The cut is
// where the script already had a seam.
//
// And the gain is provenance. One step declaring both the downloader and the
// Rust parser means either invalidates both. Measured on build 24 (2026-09-07):
//
//	STEP ingest-etsi
//	  reason  implementation changed: rust/store/src/lib.rs
//
// rust/store/src/lib.rs cannot alter one downloaded byte, and this is the same
// over-broad-declaration defect that cost an hour on this very step in build 20 —
// one level up, and still unfixed at that level.
//
// It is also the only place in the ETSI chain where PARALLELISM is available.
// Every other heavy step is bounded by the 16 GB DuckDB writer cap, and the
// machine has 28 GB, so no two of those can overlap — measured before writing any
// of this. Downloads are network-bound and pdftotext is small and short-lived, so
// the loop is a worker pool now (see scripts/etsi-fetch.sh).
func stepFetchETSI() *Step {
	return &Step{
		Name:    "fetch-etsi",
		Version: 1,
		Doc:     "download the ETSI deliverables and convert them to HTML (PDF text layer)",
		// build-go, not build-rust: this step runs cmd/discover-etsi and never
		// touches the Rust ingest. That asymmetry IS the split.
		Deps: []string{"discover-etsi", "build-go"},
		// THIS STEP RE-DERIVES THE SCOPE and must therefore record it. It does not
		// read discover-etsi's work list: scripts/etsi-fetch.sh runs the enumerator
		// again from ETSI_ALL/ETSI_ALL_VERSIONS/ETSI_SPECS. Leaning on the dependency
		// edge alone would be leaning on the claim that the two always agree, which
		// is exactly what went wrong when the narrow scope left those variables to
		// the ambient environment.
		Extra: func(c *Ctx) (map[string]string, error) {
			return map[string]string{
				"etsi_scope": strings.Join(etsiScopeEnv(c.Cfg("etsi_scope")), " "),
			}, nil
		},
		Impl: []string{
			"scripts/etsi-fetch.sh",
			"scripts/lib/etsi-common.sh",
			// convert_pdf lives here and is called from the worker.
			"scripts/lib/convert.sh",
			// The binary this step runs to build its work list.
			"cmd/discover-etsi",
		},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.statePath("etsi-worklist.tsv")}, nil
		},
		Heavy: true,
		// No Outputs, exactly as `fetch` declares none. What this step produces is a
		// tree of converted HTML whose per-file enumeration would make the
		// fingerprint enormous; `ingest-etsi` takes that tree as its INPUT instead,
		// which is where the signal is actually needed.
		Outputs: func(c *Ctx) []string { return nil },
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
				"ETSI_CONVERT=" + c.dataPath("sources", "convert-etsi"),
				"ETSI_ORIGIN=" + c.dataPath("sources", "etsi-origin"),
			}
			env = append(env, etsiScopeEnv(c.Cfg("etsi_scope"))...)

			c.Log.Printf("fetching the ETSI deliverables (PDF text layer, never OCR)")
			if err := c.Run(Cmd{Name: "bash", Args: []string{"scripts/etsi-fetch.sh"}, Env: env, Echo: true}); err != nil {
				return err
			}
			n := countFiles(c.dataPath("sources", "convert-etsi"), ".html")
			c.Checkpoint("etsi_converted", strconv.Itoa(n))
			// ZERO CONVERTED FILES IS A FAILURE, NOT AN EMPTY RESULT. The ingest would
			// otherwise run on nothing and leave a schema-only DB that serves as an
			// empty corpus without complaining — the failure mode ingest-etsi's own
			// Validate was written to catch, caught one step earlier and named.
			if n == 0 {
				return fmt.Errorf("the ETSI fetch converted no deliverable at all under %s",
					c.dataPath("sources", "convert-etsi"))
			}
			return nil
		},
	}
}

// stepIngestETSI ingests the converted ETSI deliverables into data/etsi.duckdb.
//
// Acquisition is `fetch-etsi`; this step is the ETSI analogue of `ingest`, and it
// declares the Rust chain and nothing else.
//
// IT IS CALLED `ingest-etsi`, AND THE NAME IS THE POINT. It was `corpus-etsi`,
// which is the only step in either arm that did not share its twin's name: the
// pipeline pairs `fetch`/`fetch-etsi`, `embed`/`embed-etsi`, `enrich`/`enrich-etsi`
// and six more, and then called the ETSI ingest something else. A name that does
// not pair is a step nobody looks for when they check whether both halves get the
// same treatment -- which is how this arm went without an enrich, a sparse-aware
// compaction and a contract of its own for as long as it did.
func stepIngestETSI() *Step {
	return &Step{
		Name:    "ingest-etsi",
		Version: 3,
		Doc:     "ingest the converted ETSI deliverables into data/etsi.duckdb",
		// build-go, as on the 3GPP ingest: the Run below restores the write shape
		// with cmd/migrate-paragraphs before the parse writes a row, and that is a
		// Go binary. Neither the edge nor the source was declared, so `--only
		// ingest-etsi` launched whatever migrate-paragraphs sat on disk and a change
		// to the restore could not replay this step.
		Deps: []string{"fetch-etsi", "build-rust", "build-go"},
		// Test files are not determinants: no binary this step runs compiles them.
		ExcludeTests: true,
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
		// scripts/lib/convert.sh IS GONE FROM THIS LIST, and its absence is the
		// point of the split: convert_pdf is called by the fetch and by nothing
		// here. It is not sourced by the shared prelude either, so the omission is
		// a fact about the code rather than a claim about it.
		Impl: []string{
			"scripts/etsi-ingest.sh", "scripts/lib/etsi-common.sh",
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
			// The write-shape restore the Run performs first (ensureWriteShape) —
			// the same binary the 3GPP ingest runs before its fold.
			"cmd/migrate-paragraphs",
		},
		Inputs: func(c *Ctx) ([]string, error) {
			// THE CONVERTED TREE IS THE INPUT NOW, not the work list. The work list
			// says what SHOULD have been fetched; the tree is what the fetch actually
			// produced, and it is the only thing this step reads. Declaring the
			// directory rather than every file mirrors `ingest` on the 3GPP side: the
			// per-file enumeration would make the fingerprint enormous, and
			// `ingest --resume` is the real per-deliverable checkpoint through the
			// ingest_log table.
			return []string{c.dataPath("sources", "convert-etsi", "ETSI")}, nil
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
			// pdftotext IS NOT CHECKED HERE ANY MORE. It is the fetch's tool, and this
			// step neither converts nor reads a PDF. Keeping the guard would have been
			// the same over-broad coupling as the provenance it just shed.
			env := []string{
				"INGEST_BIN=" + c.rbin("ingest"),
				"ETSI_OUT=" + c.dataPath("etsi.duckdb"),
				"ETSI_CONVERT=" + c.dataPath("sources", "convert-etsi"),
				"ETSI_ORIGIN=" + c.dataPath("sources", "etsi-origin"),
			}

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
			// never carried across. It surfaced the first time the ETSI ingest ran after
			// paragraphs-etsi (2026-09-03), not because either step changed.
			if err := ensureWriteShape(c, c.dataPath("etsi.duckdb")); err != nil {
				return fmt.Errorf("the ETSI corpus could not be put back into write shape: %w", err)
			}

			c.Log.Printf("ingesting the converted ETSI deliverables")
			return c.Run(Cmd{Name: "bash", Args: []string{"scripts/etsi-ingest.sh"}, Env: env, Echo: true})
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

// ScopeLISuite pins the ETSI half to the fourteen built-in Lawful-Interception
// deliverables. It is the value the EMPTY knob used to mean, and it now has a
// name because it stopped being the default.
//
// WHY THE DEFAULT MOVED. `make build` runs `goal run` with no -etsi-scope and
// nothing sets GOAL_ETSI_SCOPE, so the command that PUBLISHES resolved a work
// list of fourteen deliverables while `discover` diffed 20 163 3GPP versions on
// the other arm. The corpus did not shrink — ingest-etsi reads the converted
// tree, which holds 11 822 files from a campaign run by hand — so nothing looked
// wrong. But no ETSI deliverable published after that campaign could ever be
// discovered, and the half was frozen in time with no gate able to say so.
//
// This is the same defect as the one ScopeAll was introduced for, one layer up:
// there, the knob existed and nothing could set it; here, the knob can be set and
// the command that matters does not set it. A capability reachable only by an
// operator who remembers is not reachable.
//
// The narrow scope stays available, by name, because a first build on a machine
// that only wants the LI suite is a real use. Narrowing is now a decision someone
// types, which is the direction that costs a surprise rather than a corpus.
const ScopeLISuite = "li-suite"

// etsiScopeArgs turns the scope knob into cmd/discover-etsi flags.
//
// The value is trimmed ONCE and the trimmed value is what travels. Trimming only
// for the dispatch and then forwarding the original passed " 103 280 " through to
// --specs, where the leading space becomes part of the first id and resolves
// nothing.
func etsiScopeArgs(scope string) []string {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "", ScopeAllVersions:
		// EMPTY IS THE WHOLE ARCHIVE, EVERY VERSION. See ScopeLISuite.
		return []string{"--all", "--all-versions"}
	case ScopeLISuite:
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
	// EVERY BRANCH SETS ALL THREE, EMPTY WHERE IT MEANS "NOT THIS".
	//
	// Ctx.Run passes cmd.Env as append(os.Environ(), ...), so a variable this
	// function does not mention is INHERITED from whatever the operator's shell
	// carries. Returning nil for the narrow scope therefore did not select the
	// narrow scope: `discover-etsi` took its scope from FLAGS and resolved the
	// fourteen built-in deliverables, while scripts/etsi-fetch.sh rebuilt its own
	// work list from an ambient ETSI_ALL and downloaded the whole archive. The two
	// steps of one arm would have been running on different corpora, and the only
	// symptom is a fetch that takes all night when fourteen specs were asked for.
	//
	// The script tests with [ -n "${VAR:-}" ], so an explicit empty value disarms
	// an inherited one — which is why clearing is expressible at all.
	scope = strings.TrimSpace(scope)
	const (
		all  = "ETSI_ALL="
		vers = "ETSI_ALL_VERSIONS="
		spec = "ETSI_SPECS="
	)
	// The variables the script feeds into its enumeration that this pipeline models
	// no knob for. They are CLEARED, not merely unmentioned: an exported
	// ETSI_INCLUDE_3GPP adds ETSI's republications of 3GPP specs to the work list,
	// and ETSI_TYPE_DIRS changes which archives are scanned at all — so two runs
	// with the same etsi_scope would download different corpora behind the same
	// determinant, and the second would skip on a corpus the first never built.
	//
	// Modelling them instead would mean a knob nobody asked for. Clearing says the
	// pipeline's scope is the WHOLE scope, which is the property the fingerprint
	// needs to be true.
	unmodelled := []string{"ETSI_INCLUDE_3GPP=", "ETSI_TYPE_DIRS=", "ETSI_INDEX="}
	with := func(scoped ...string) []string { return append(scoped, unmodelled...) }
	switch scope {
	case "", ScopeAllVersions:
		return with(all+"1", vers+"1", spec)
	case ScopeLISuite:
		return with(all, vers, spec)
	case ScopeAll:
		return with(all+"1", vers, spec)
	default:
		return with(all, vers, spec+scope)
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
