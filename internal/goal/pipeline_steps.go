package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// This file holds the corpus steps proper: discover, fetch, ingest, merge,
// embed, enrich, index, validate and smoke. pipeline.go holds the DAG shape and
// the build/seed steps.

const statusReportURL = "https://www.3gpp.org/DynaReport/status-report.htm"

// runDiscover refreshes the 3GPP status report and recomputes the delta.
//
// It writes series.json ATOMICALLY and only after the discover binary succeeded,
// so a crash mid-refresh can never leave a truncated work list that a later run
// would treat as "nothing to do".
func runDiscover(c *Ctx) error {
	report := c.statePath("status-report.htm")
	if err := os.MkdirAll(filepath.Dir(report), 0o755); err != nil {
		return err
	}

	// 3gpp.org answers 403 to robot user-agents; the browser UA is required, and
	// the network is the one place where bounded retries are legitimate.
	c.Log.Printf("fetching the 3GPP status report")
	if err := c.Retry(RetryNetwork, "status report", func() error {
		return c.Run(Cmd{Name: "curl", Args: []string{
			"-fsSL", "--max-time", "180",
			"-A", "Mozilla/5.0 (X11; Linux x86_64) discover",
			statusReportURL, "-o", report + ".tmp",
		}})
	}); err != nil {
		return err
	}
	st, err := os.Stat(report + ".tmp")
	if err != nil || st.Size() < 4096 {
		return fmt.Errorf("the status report came back empty or truncated (%v bytes) — refusing to compute a delta from it", st.Size())
	}
	if err := os.Rename(report+".tmp", report); err != nil {
		return err
	}
	c.Log.Printf("status report: %d bytes", st.Size())

	args := []string{"--status-file", report, "--floor", c.Cfg("floor")}
	idx := filepath.Join(c.Local, "corpus-index.json")
	switch {
	case c.Cfg("full") == "1":
		args = append(args, "--all")
		c.Log.Printf("FULL mode: every series at/above the floor will be reindexed")
	case fileNonEmpty(idx):
		args = append(args, "--index", idx)
	default:
		c.Log.Printf("no local corpus-index.json — this pass is a FULL discover")
	}
	if s := c.Cfg("scope"); s != "" {
		args = append(args, "--series", s)
	}

	series, err := c.Output(Cmd{Name: c.rbin("discover"), Args: args})
	if err != nil {
		return err
	}
	if strings.TrimSpace(series) == "" {
		series = "[]"
	}
	// series.json is written AFTER the work list, not here: in repair mode the work
	// list can reach series the delta never flagged, and ingest walks series.json.
	// See the union below.

	// Two work-lists, and they are NOT the same set.
	//
	// `--emit-worklist` applies no version comparison at all: it emits every spec
	// of every selected series, 20 225 entries against the 986 that actually moved.
	// That 20x over-fetch is wasteful, and it is also the only reason the anchor's
	// over-claims currently get repaired — it re-acquires them by accident.
	//
	// `--repair-plan` is the proportionate set, drift UNION corpus-holes, and it is
	// only safe BECAUSE it takes the holes as an explicit input. Asking for the
	// precise set without supplying them would freeze every over-claimed spec as a
	// permanent gap, so the holes are computed here rather than left to the caller.
	wlArgs := []string{"--status-file", report, "--floor", c.Cfg("floor")}
	if s := c.Cfg("scope"); s != "" {
		wlArgs = append(wlArgs, "--series", s)
	}
	if c.Cfg("repair") == "1" {
		holes, err := repairKeys(c)
		if err != nil {
			return fmt.Errorf("repair mode needs the corpus holes and could not get them: %w", err)
		}
		wlArgs = append(wlArgs, "--repair-plan", "--holes", holes, "--index", idx)
	} else {
		wlArgs = append(wlArgs, "--emit-worklist")
	}
	wl, err := c.Output(Cmd{Name: c.rbin("discover"), Args: wlArgs})
	if err != nil {
		return err
	}
	if err := WriteAtomic(c.statePath("worklist.txt"), []byte(wl+"\n")); err != nil {
		return err
	}

	// WHAT IS ACQUIRED MUST BE WHAT IS READ.
	//
	// series.json (from the DELTA) and worklist.txt (from the repair plan) are two
	// independent computations, and `ingest` walks series.json. So a hole in a series
	// the delta did not flag was fetched, converted — and then never ingested, with
	// nothing in any log to say so. Six 34.123-1 holes survived a fully successful
	// repair exactly that way on 2026-08-25: the work list carried all six, the
	// series list carried no "34", and the converted HTML sat on disk unread.
	//
	// Union the work list's own series in. A series that appears only in the delta
	// still belongs (that is the ordinary path), and one that appears only in the
	// repair set now belongs too.
	names := parseSeries(series)
	if extra := seriesNotIn(names, seriesInWorklist(wl)); len(extra) > 0 {
		c.Log.Printf("repair reaches %d series the delta did not flag — %s", len(extra), strings.Join(extra, " "))
		names = append(names, extra...)
		sort.Strings(names)
	}
	merged, err := json.Marshal(names)
	if err != nil {
		return err
	}
	if err := WriteAtomic(c.statePath("series.json"), append(merged, '\n')); err != nil {
		return err
	}

	c.Log.Printf("delta: %d series to (re)index — %s", len(names), strings.Join(names, " "))
	c.Log.Printf("worklist: %d (spec, release) pairs", strings.Count(wl, "\n")+1)
	c.Checkpoint("series", strconv.Itoa(len(names)))
	return nil
}

func parseSeries(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// seriesRe pulls the series number out of an archive URL: the "34" of
// ".../archive/34_series/34.123-1/34123-1-a70.zip". The directory is the
// authority — deriving it from the file name means re-deciding where a spec id
// ends, which is the kind of parsing that gets 34.123-1 wrong.
var seriesRe = regexp.MustCompile(`/(\d{2})_series/`)

// seriesInWorklist returns the DISTINCT series a "<release> <url> <name>" work
// list actually reaches, sorted.
func seriesInWorklist(wl string) []string {
	seen := map[string]bool{}
	for _, m := range seriesRe.FindAllStringSubmatch(wl, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// seriesNotIn returns the members of `want` that `have` does not already carry.
func seriesNotIn(have, want []string) []string {
	in := make(map[string]bool, len(have))
	for _, s := range have {
		in[s] = true
	}
	var out []string
	for _, s := range want {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}

func seriesOf(c *Ctx) []string {
	if s := c.Cfg("scope"); s != "" {
		return strings.Fields(s)
	}
	b, err := os.ReadFile(c.statePath("series.json"))
	if err != nil {
		return nil
	}
	return parseSeries(string(b))
}

func fileNonEmpty(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// -------------------------------------------------------------------- fetch

func stepFetch() *Step {
	return &Step{
		Name:    "fetch",
		Version: 1,
		Doc:     "download and convert the delta specs to HTML (LibreOffice)",
		Deps:    []string{"discover"},
		Impl:    []string{"scripts/corpus.sh", "scripts/lib/convert.sh", "scripts/lib/retry.sh", "scripts/lib/soffice-guard.sh"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.statePath("series.json"), c.statePath("worklist.txt")}, nil
		},
		Extra: func(c *Ctx) (map[string]string, error) {
			// `floor` and `repair` belong in the fingerprint: both change WHICH
			// specs are acquired, so switching between them must replay the step
			// rather than reuse whichever one happened to run first.
			//
			// `jobs` does NOT. It changes how fast the same set is fetched, never
			// the set itself — and having it here meant that tuning a parallelism
			// knob invalidated `fetch` and cascaded through ingest, merge, embed
			// and index: half an hour of rework to answer a scheduling question.
			// A fingerprint must capture what changes the OUTPUT, not the rate at
			// which it is produced.
			return map[string]string{
				"floor":  c.Cfg("floor"),
				"repair": c.Cfg("repair"),
			}, nil
		},
		Heavy: true,
		Run: func(c *Ctx) error {
			series := seriesOf(c)
			if len(series) == 0 {
				c.Log.Printf("delta is empty — nothing to download or convert")
				return nil
			}
			if _, err := c.Output(Cmd{Name: "soffice", Args: []string{"--version"}}); err != nil {
				return fmt.Errorf("LibreOffice (soffice) is required to convert 3GPP .doc/.docx to HTML and is not on PATH: %w", err)
			}
			// corpus.sh is already incremental and flock-guarded; it is the
			// per-resource checkpoint for this step (one file at a time, skipping
			// whatever is already downloaded and converted).
			args := []string{"scripts/corpus.sh", "--set", c.Cfg("floor"), "--jobs", c.Cfg("jobs")}
			if c.Cfg("repair") == "1" {
				// discover already wrote the exact set (drift ∪ corpus holes). Hand it
				// over verbatim instead of letting corpus.sh re-enumerate the series,
				// which is what turns 1 000 specs back into 20 225.
				wl := c.statePath("worklist.txt")
				n := countLines(wl)
				c.Log.Printf("repair mode: fetching %d specs from the repair plan (not %d series wholesale)", n, len(series))
				args = append(args, "--worklist", wl)
			} else {
				c.Log.Printf("converting %d series: %s", len(series), strings.Join(series, " "))
				args = append(args, "--series", strings.Join(series, " "))
			}
			if err := c.Run(Cmd{Name: "bash", Args: args, Echo: true}); err != nil {
				return err
			}
			return purgeConvertedZips(c)
		},
		Outputs: func(c *Ctx) []string { return nil },
		Validate: func(c *Ctx) error {
			if len(seriesOf(c)) == 0 {
				return nil
			}
			n := countFiles(c.dataPath("sources", "convert"), ".html")
			if n == 0 {
				return fmt.Errorf("no converted HTML found under %s", c.dataPath("sources", "convert"))
			}
			return nil
		},
	}
}

// purgeConvertedZips reclaims the source archives whose HTML exists.
//
// The full corpus is ~37 GB of archives plus ~37 GB of converted HTML. Keeping
// both is what filled the CI runner's disk and turned every scheduled build red.
// The archive is an intermediate: once its HTML is on disk, nothing downstream
// reads it again, and corpus.sh will re-download it if it is ever needed.
func purgeConvertedZips(c *Ctx) error {
	origin := c.dataPath("sources", "origin")
	convert := c.dataPath("sources", "convert")
	if _, err := os.Stat(origin); err != nil {
		return nil
	}
	var freed int64
	var n int
	err := filepath.Walk(origin, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".zip") {
			return nil
		}
		rel := filepath.Base(filepath.Dir(path))
		base := strings.TrimSuffix(filepath.Base(path), ".zip")
		matches, _ := filepath.Glob(filepath.Join(convert, rel, base+"*.html"))
		if len(matches) == 0 {
			return nil
		}
		size := info.Size()
		if err := os.Remove(path); err == nil {
			freed += size
			n++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if n > 0 {
		c.Log.Printf("purged %d converted archives, reclaimed %.1f GB", n, float64(freed)/1e9)
	}
	return nil
}

func countFiles(dir, ext string) int {
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ext) {
			n++
		}
		return nil
	})
	return n
}

// ------------------------------------------------------------------- ingest

func stepIngest() *Step {
	return &Step{
		Name:    "ingest",
		Version: 1,
		Doc:     "parse the converted HTML into per-series DuckDB shards",
		Deps:    []string{"fetch", "build-rust"},
		Impl:    []string{"rust/parse", "rust/ingest", "rust/store/src", "internal/store/schema.sql"},
		Inputs: func(c *Ctx) ([]string, error) {
			// The converted tree is the input. Enumerating every HTML file would
			// make the fingerprint enormous; the per-series directories carry the
			// same signal at a fraction of the cost, and `ingest --resume` is the
			// real per-(spec,version) checkpoint via the ingest_log table.
			var dirs []string
			base := c.dataPath("sources", "convert")
			ents, err := os.ReadDir(base)
			if err != nil {
				return []string{base}, nil
			}
			for _, e := range ents {
				if e.IsDir() {
					p := filepath.Join(base, e.Name())
					dirs = append(dirs, p)
					if sub, err := os.ReadDir(p); err == nil {
						dirs = append(dirs, fmt.Sprintf("%s#%d", p, len(sub)))
					}
				}
			}
			return dirs, nil
		},
		Heavy: true,
		Outputs: func(c *Ctx) []string {
			var out []string
			for _, s := range seriesOf(c) {
				out = append(out, filepath.Join(c.Local, "shards", s+".duckdb"))
			}
			return out
		},
		Run: func(c *Ctx) error {
			series := seriesOf(c)
			if len(series) == 0 {
				c.Log.Printf("delta is empty — no shard to (re)build")
				return nil
			}
			shardDir := filepath.Join(c.Local, "shards")
			if err := os.MkdirAll(shardDir, 0o755); err != nil {
				return err
			}
			for i, s := range series {
				db := filepath.Join(shardDir, s+".duckdb")
				c.Log.Printf("ingest series %s (%d/%d)", s, i+1, len(series))
				// --resume consults ingest_log, stamped with PIPELINE_VERSION:
				// already-ingested (spec, version) pairs are skipped, and a parser
				// or schema change invalidates the log wholesale. That is the
				// per-unit checkpoint; we do not add a second ledger beside it.
				//
				// --corpus widens that question from "did THIS shard ingest it" to
				// "does the corpus already hold it". ingest_log lives in the shard,
				// and a shard is scratch: delete it and the ledger is empty, so a
				// series is parsed and written again in full. That re-ingested
				// ~300 000 clauses on 2026-08-25 to acquire five specs.
				ingestArgs := []string{
					"--series", s,
					"--convert", c.dataPath("sources", "convert"),
					"--db", db,
					"--resume",
				}
				if corpus := c.dataPath("3gpp.duckdb"); fileNonEmpty(corpus) {
					ingestArgs = append(ingestArgs, "--corpus", corpus)
				}
				if err := c.Run(Cmd{Name: c.rbin("ingest"), Args: ingestArgs, Echo: true}); err != nil {
					return err
				}
				c.Checkpoint("last_series", s)
				c.Checkpoint("done", fmt.Sprintf("%d/%d", i+1, len(series)))
			}
			return nil
		},
	}
}

// -------------------------------------------------------------------- merge

func stepMerge() *Step {
	return &Step{
		Name:    "merge",
		Version: 1,
		Doc:     "fold the shards into the corpus DB and rewrite the delta anchor",
		// build-go is a dependency because the fold is preceded by a restore, and
		// that restore is a Go binary. cmd/migrate-paragraphs is in Impl for the
		// same reason: what this step does to the corpus now depends on it.
		Deps: []string{"ingest", "build-go"},
		Impl: []string{"rust/store/src/bin/merge.rs", "rust/store/src/lib.rs", "cmd/migrate-paragraphs"},
		Inputs: func(c *Ctx) ([]string, error) {
			var in []string
			shardDir := filepath.Join(c.Local, "shards")
			ents, err := os.ReadDir(shardDir)
			if err == nil {
				for _, e := range ents {
					if strings.HasSuffix(e.Name(), ".duckdb") {
						in = append(in, filepath.Join(shardDir, e.Name()))
					}
				}
			}
			in = append(in, c.dataPath("3gpp.duckdb"))
			return in, nil
		},
		Heavy: true,
		Outputs: func(c *Ctx) []string {
			return []string{c.dataPath("3gpp.duckdb"), filepath.Join(c.Local, "corpus-index.json")}
		},
		Validate: func(c *Ctx) error {
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}})
			if err != nil {
				return fmt.Errorf("the merged DB does not open: %w", err)
			}
			if !strings.Contains(out, "spec_versions=") {
				return fmt.Errorf("dbcount produced no counters")
			}
			return nil
		},
		Run: func(c *Ctx) error {
			shardDir := filepath.Join(c.Local, "shards")
			var shards []string
			if ents, err := os.ReadDir(shardDir); err == nil {
				for _, e := range ents {
					if strings.HasSuffix(e.Name(), ".duckdb") {
						shards = append(shards, filepath.Join(shardDir, e.Name()))
					}
				}
			}
			db := c.dataPath("3gpp.duckdb")
			if len(shards) == 0 {
				c.Log.Printf("no shard to fold — the corpus is unchanged")
				// The anchor must still exist for the next discover; if it does
				// not, regenerate it from the current DB rather than leaving the
				// delta unanchored.
				return ensureCorpusIndex(c)
			}

			// GIVE THE WRITE SIDE BACK THE SHAPE IT KNOWS, BEFORE IT TOUCHES THE CORPUS.
			//
			// `merge --base` compact-copies the base table by table, from
			// duckdb_tables(). A converted corpus serves `clauses` as a VIEW, which
			// is not a table, so the copy leaves it behind and schema.sql recreates
			// it EMPTY in the destination. merge then folds the changed buckets into
			// that empty table, and the result is a corpus whose `clauses` holds the
			// increment while `clause_occ` still holds every occurrence — with
			// max_chunk_id() reading 0 and handing the shard chunk_ids that collide
			// with the ones already there, and changed_buckets() seeing an empty
			// table and calling every bucket changed.
			//
			// Restoring first costs one grouped reconstruction (1 m 47 for 2.87 GB,
			// measured) and keeps ADR 0004's storage layout entirely on this side of
			// the write/read split, which is where ADR 0001 put it. The `paragraphs`
			// step converts again afterwards, from a `clauses` that is once more
			// whole — so its own refusal to rebuild from a delta never has to fire.
			if fileNonEmpty(db) {
				c.Log.Printf("restoring the write-side corpus shape before folding")
				if err := c.Run(Cmd{Name: c.bin("migrate-paragraphs"), Args: []string{
					"--db", db, "--restore",
				}, Echo: true}); err != nil {
					return fmt.Errorf("restoring `clauses` before the merge: %w", err)
				}
			}
			tmp := db + ".new"
			args := []string{
				"--out", tmp,
				"--index-out", filepath.Join(c.Local, "corpus-index.json.new"),
				"--subject-index-out", filepath.Join(c.Local, "subject-index.json.new"),
				"--build-index-out", filepath.Join(c.Local, "build-index.json.new"),
				// HNSW is built by the `index` step, AFTER the vectors exist.
				"--no-hnsw",
			}
			if fileNonEmpty(db) && c.Cfg("full") != "1" {
				args = append(args, "--base", db)
				c.Log.Printf("incremental merge on the existing corpus (bucket replacement per spec+release)")
			}
			args = append(args, shards...)
			c.Log.Printf("folding %d shard(s)", len(shards))
			if err := c.Run(Cmd{Name: c.rbin("merge"), Args: args, Echo: true}); err != nil {
				return err
			}

			// Publish the new corpus and its anchor TOGETHER, and only after the
			// merge succeeded. Publishing the index first would let a crash leave
			// an anchor claiming a corpus state that was never written — the next
			// discover would then believe it is up to date and silently skip work.
			if err := publishCorpus(tmp, db); err != nil {
				return err
			}
			for _, n := range []string{"corpus-index.json", "subject-index.json", "build-index.json"} {
				src := filepath.Join(c.Local, n+".new")
				if fileNonEmpty(src) {
					if err := os.Rename(src, filepath.Join(c.Local, n)); err != nil {
						return err
					}
				}
			}
			c.Log.Printf("corpus and delta anchor published atomically")
			return nil
		},
	}
}

// publishCorpus puts the freshly merged database at its final path and DROPS THE
// WAL OF THE DATABASE IT REPLACES.
//
// DuckDB keeps <db>.wal beside the file and removes it only on a clean
// checkpoint, so a writer that was killed leaves one behind. That WAL describes
// the OLD database. Once the rename puts a DIFFERENT file at the same path, the
// next open replays it against a corpus it never belonged to and fails with
// "Failure while replaying WAL file …: Conflict on tuple deletion!". The merge
// itself is perfectly good — the corpus is simply unopenable, which is worse than
// a plain failure because it reads as corruption.
//
// Seen for real: a vector import killed at 04:59 left a 9 MB WAL; merge
// republished the corpus 19 hours later and the step failed its OWN output
// validation, with the just-written DB on disk and intact.
//
// Removing the sidecar is part of publishing, not cleanup — the freshly merged DB
// is checkpointed and has no WAL of its own to lose.
func publishCorpus(tmp, db string) error {
	if err := os.Rename(tmp, db); err != nil {
		return err
	}
	if err := os.Remove(db + ".wal"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing the replaced corpus WAL: %w", err)
	}
	return nil
}

// ensureCorpusIndex regenerates the anchor from the current DB when it is
// missing, so a fresh clone with a seeded corpus does not fall back to a full
// discover forever.
func ensureCorpusIndex(c *Ctx) error {
	idx := filepath.Join(c.Local, "corpus-index.json")
	if fileNonEmpty(idx) {
		return nil
	}
	db := c.dataPath("3gpp.duckdb")
	if !fileNonEmpty(db) {
		return nil
	}
	c.Log.Printf("regenerating the delta anchor from the current corpus")
	tmp := idx + ".new"
	if err := c.Run(Cmd{Name: c.rbin("merge"), Args: []string{
		"--out", filepath.Join(c.Local, "tmp", "index-only.duckdb"),
		"--index-out", tmp, "--no-index", "--no-hnsw", "--base", db,
	}}); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(c.Local, "tmp", "index-only.duckdb"))
	return os.Rename(tmp, idx)
}

// repairKeys asks anchorcheck for the (spec|release) keys the anchor claims and
// the corpus cannot back, and returns the path to that list.
//
// It is computed here, inside the step, rather than accepted as a caller-supplied
// file. A repair plan is only proportionate BECAUSE it carries the holes; letting
// an operator pass a stale or absent hole list would produce a plan that looks
// precise and is quietly incomplete — the same shape of failure the holes came
// from. anchorcheck exits 1 when it finds holes, which is a finding here, not an
// error: the file is written either way and only a missing file is fatal.
func repairKeys(c *Ctx) (string, error) {
	out := c.statePath("repair-keys.txt")
	idx := filepath.Join(c.Local, "corpus-index.json")
	db := c.dataPath("3gpp.duckdb")
	if !fileNonEmpty(db) {
		return "", fmt.Errorf("no corpus at %s to compute holes from", db)
	}
	_, _ = c.Output(Cmd{Name: c.bin("anchorcheck"), Args: []string{
		"--db", db, "--index", idx, "--quiet",
		"--emit-repair", out,
		"--emit-state", c.statePath("corpus-state.json"),
	}})
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("anchorcheck produced no hole list: %w", err)
	}
	c.Log.Printf("repair mode: %d corpus hole(s) folded into the work list", countLines(out))
	return out, nil
}
