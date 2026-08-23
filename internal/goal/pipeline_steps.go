package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err := WriteAtomic(c.statePath("series.json"), []byte(series+"\n")); err != nil {
		return err
	}

	wlArgs := []string{"--status-file", report, "--floor", c.Cfg("floor"), "--emit-worklist"}
	if s := c.Cfg("scope"); s != "" {
		wlArgs = append(wlArgs, "--series", s)
	}
	wl, err := c.Output(Cmd{Name: c.rbin("discover"), Args: wlArgs})
	if err != nil {
		return err
	}
	if err := WriteAtomic(c.statePath("worklist.txt"), []byte(wl+"\n")); err != nil {
		return err
	}

	names := parseSeries(series)
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
			return map[string]string{"floor": c.Cfg("floor"), "jobs": c.Cfg("jobs")}, nil
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
			c.Log.Printf("converting %d series: %s", len(series), strings.Join(series, " "))
			// corpus.sh is already incremental and flock-guarded; it is the
			// per-resource checkpoint for this step (one file at a time, skipping
			// whatever is already downloaded and converted).
			if err := c.Run(Cmd{
				Name: "bash",
				Args: []string{"scripts/corpus.sh", "--set", c.Cfg("floor"), "--jobs", c.Cfg("jobs"), "--series", strings.Join(series, " ")},
				Echo: true,
			}); err != nil {
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
				if err := c.Run(Cmd{Name: c.rbin("ingest"), Args: []string{
					"--series", s,
					"--convert", c.dataPath("sources", "convert"),
					"--db", db,
					"--resume",
				}, Echo: true}); err != nil {
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
		Deps:    []string{"ingest"},
		Impl:    []string{"rust/store/src/bin/merge.rs", "rust/store/src/lib.rs"},
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
			if err := os.Rename(tmp, db); err != nil {
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
