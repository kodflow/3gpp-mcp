// Package ingest orchestrates the offline pipeline: walk the HTML corpus,
// parse each spec into clauses + change history, embed (optional), and write a
// DuckDB snapshot. Reads the LibreOffice-converted HTML under data/sources/convert
// (DECISION 2026-05-25; see internal/htmlparse). A run is a full deterministic
// rebuild (Reset first), so a given corpus state yields the same DB.
package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/htmlparse"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/ooxml"
	"github.com/kodflow/3gpp-mcp/internal/registry"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

// parsedSpec is the parser-agnostic result; both htmlparse and ooxml produce the
// same fields, so the ingest loop is independent of which parser ran.
type parsedSpec struct {
	Spec     model.Spec
	Version  model.SpecVersion
	Clauses  []model.Clause
	Changes  []model.Change
	Degraded bool
}

// parserFor returns the (file extension, parse func) for the configured parser.
// Default "html" keeps the LibreOffice pipeline; "ooxml" parses .docx directly
// (axis #4) with merged-table + heading-depth fidelity.
func parserFor(name string) (string, func(string) (*parsedSpec, error)) {
	if name == "ooxml" {
		return ".docx", func(p string) (*parsedSpec, error) {
			ps, err := ooxml.ParseFile(p)
			if err != nil {
				return nil, err
			}
			return &parsedSpec{ps.Spec, ps.Version, ps.Clauses, ps.Changes, ps.Degraded}, nil
		}
	}
	return ".html", func(p string) (*parsedSpec, error) {
		ps, err := htmlparse.ParseFile(p)
		if err != nil {
			return nil, err
		}
		return &parsedSpec{ps.Spec, ps.Version, ps.Clauses, ps.Changes, ps.Degraded}, nil
	}
}

// Options configures a run.
type Options struct {
	ConvertDir string   // e.g. data/sources/convert
	OriginDir  string   // e.g. data/sources/origin (for ASN.1 attachments); empty = derive
	Parser     string   // "html" (default, LibreOffice HTML) | "ooxml" (direct .docx)
	Releases   []string // optional: keep only these releases ("Rel-19"); empty = all
	Series     []string // optional: keep only these series ("33"); empty = all
	SpecIDs    []string // optional: keep only these spec ids ("33.128"); empty = all
	EnableFTS  bool     // build the BM25 index after load
	EmbedFloor string   // optional: embed ONLY clauses at/above this release ("Rel-15"); empty = embed all. Lexical coverage is unaffected — every clause is still ingested.
	Embedder   embed.Embedder
	Registry   *subject.Registry // domain subjects (nil = registry.Default())
	Logf       func(string, ...any)
	// Resume keeps the existing DB at dbPath instead of Reset()-ing it, skips
	// (spec, version) tuples whose ingest_log row is 'done' under the current
	// pipeline_version, and PURGES + re-runs rows that are 'started' (a runner
	// that died mid-spec left a half-state — re-do that spec only). Stale-
	// pipeline rows are wiped first (invariant #2: algorithm bump rebuilds).
	Resume bool
}

// Stats summarises a run.
type Stats struct {
	Files, Specs, Versions, Clauses, Changes, Evolutions, Degraded int
	SubjectAdded                                                   map[string]int // subject name -> records added
	FTS, Vectors, HNSW                                             bool
}

// Matches a spec file name: NNNNN-XXX, optionally followed by a "_<section>"
// suffix for multi-part specs (e.g. 36213-j30_s10-s13). 5-digit = 3GPP specs.
// (GSM Phase 1/2 = 4-digit series 00-12, different numbering — out of scope.)
// Junk inner docs from embedded media lack the prefix and are excluded.
var reFile = regexp.MustCompile(`^([0-9]{5})-([0-9a-z]{3})(?:_.*)?$`)

// ingestJob is one (spec, version) tuple to process. Promoted to a named type
// (was anonymous inside Run) so filterResumeJobs can take it as a parameter
// without anonymous-struct identity issues.
type ingestJob struct{ path, specID, release, version string }

// Run executes the pipeline into the DuckDB file at dbPath.
func Run(ctx context.Context, dbPath string, opt Options) (Stats, error) {
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opt.Embedder == nil {
		opt.Embedder = embed.Disabled{}
	}
	reg := opt.Registry
	if reg == nil {
		reg = registry.Default()
	}
	if opt.OriginDir == "" {
		opt.OriginDir = filepath.Join(filepath.Dir(opt.ConvertDir), "origin")
	}
	st := Stats{SubjectAdded: map[string]int{}}

	ext, parse := parserFor(opt.Parser)
	files, err := filepath.Glob(filepath.Join(opt.ConvertDir, "*", "*"+ext))
	if err != nil {
		return st, err
	}
	var jobs []ingestJob
	relSet, serSet, specSet := set(opt.Releases), set(opt.Series), set(opt.SpecIDs)
	for _, p := range files {
		base := strings.TrimSuffix(filepath.Base(p), ext)
		m := reFile.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		num := m[1]
		specID := num[:2] + "." + num[2:]
		release, version, ok := model.DecodeVersionCode(m[2])
		if !ok {
			continue
		}
		if len(relSet) > 0 && !relSet[release] {
			continue
		}
		if len(serSet) > 0 && !serSet[num[:2]] {
			continue
		}
		if len(specSet) > 0 && !specSet[specID] && !specSet[num] {
			continue
		}
		jobs = append(jobs, ingestJob{p, specID, release, version})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].path < jobs[j].path })

	// Latest version per spec gets its change-history ingested (others would
	// just duplicate the cumulative history).
	latest := map[string]string{} // specID -> version
	for _, j := range jobs {
		if cur, ok := latest[j.specID]; !ok || verLess(cur, j.version) {
			latest[j.specID] = j.version
		}
	}

	// Resume mode: keep the existing DB (if any) and skip already-done specs.
	// Fresh mode (default): drop the file so the run is deterministic.
	resuming := opt.Resume && fileExists(dbPath)
	if !resuming {
		_ = os.Remove(dbPath)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return st, err
	}
	defer func() { _ = db.Close() }()
	// Only Reset() on a fresh run — resuming wipes the work we want to keep.
	if !resuming {
		if err := db.Reset(ctx); err != nil {
			return st, err
		}
	}

	// Vector floor: embed only clauses at/above this release (lexical coverage is
	// unaffected). 0 = embed everything ingested.
	floorOrd := 0
	if opt.EmbedFloor != "" {
		if o, ok := model.ReleaseOrdinal(opt.EmbedFloor); ok {
			floorOrd = o
		} else {
			logf("ignoring unparseable EmbedFloor %q (embedding all releases)", opt.EmbedFloor)
		}
	}

	// Resume orchestration (axis #15, plan §15 invariant #2):
	//   1. wipe ingest_log rows from a stale pipeline_version (algorithm bump
	//      ⇒ the partial DB is no longer comparable, redo everything),
	//   2. for each (spec, version) that's 'started' but not 'done', purge
	//      its tables and the log row so the re-run is clean,
	//   3. filter out (spec, version) tuples already 'done'.
	pipelineVersion := model.PipelineVersion(opt.Embedder.ModelID())
	jobs, skippedDone, err := filterResumeJobs(ctx, db, jobs, opt.Resume, pipelineVersion, logf)
	if err != nil {
		return st, fmt.Errorf("resume filter: %w", err)
	}
	// chunkID picks up from MAX(chunk_id) when resuming so re-runs don't
	// collide with rows already in `clauses` from earlier attempts.
	var chunkID uint64
	if resuming {
		if maxID, err := db.MaxChunkID(ctx); err == nil {
			chunkID = maxID
		}
	}
	specsSeen := map[string]bool{}
	// total = planned (after skip), N = jobs to process this run. The progress
	// counter shows "done in this run" / planned + the cumulative "already done"
	// from previous attempts so the operator can see real corpus coverage.
	total := len(jobs)
	startedAt := time.Now()
	for i, j := range jobs {
		ps, err := parse(j.path)
		if err != nil {
			logf("skip %s: %v", j.path, err)
			continue
		}
		st.Files++
		if ps.Degraded {
			st.Degraded++
		}
		if !specsSeen[ps.Spec.SpecID] {
			specsSeen[ps.Spec.SpecID] = true
			st.Specs++
		}
		// Checkpoint START: a runner kill from this point on leaves a
		// status='started' row; on resume we'll PurgeSpecScope + redo this
		// (spec, version). Stamped with the current pipeline_version so an
		// algorithm bump invalidates the log (see ResetIngestLog above).
		if err := db.MarkIngestStarted(ctx, ps.Spec.SpecID, ps.Version.Version, pipelineVersion); err != nil {
			return st, fmt.Errorf("mark ingest start %s %s: %w", ps.Spec.SpecID, ps.Version.Version, err)
		}
		if err := db.UpsertSpec(ps.Spec); err != nil {
			return st, fmt.Errorf("upsert spec %s: %w", ps.Spec.SpecID, err)
		}
		if err := db.UpsertVersion(ps.Version); err != nil {
			return st, fmt.Errorf("upsert version %s %s: %w", ps.Spec.SpecID, ps.Version.Version, err)
		}
		st.Versions++

		for i := range ps.Clauses {
			chunkID++
			ps.Clauses[i].ChunkID = chunkID
		}
		if err := db.InsertClauses(ps.Clauses); err != nil {
			return st, err
		}
		st.Clauses += len(ps.Clauses)

		if err := embedClauses(ctx, db, opt.Embedder, ps.Clauses, floorOrd, &st); err != nil {
			return st, err
		}

		if latest[j.specID] == j.version && len(ps.Changes) > 0 {
			// ReplaceChanges = DELETE WHERE spec_id=? + INSERT — keeps the
			// cumulative change history idempotent on --resume (prior attempt
			// may have inserted these rows; the changes table has no unique
			// constraint, so a plain INSERT would duplicate them).
			if err := db.ReplaceChanges(ctx, ps.Spec.SpecID, ps.Changes); err != nil {
				return st, err
			}
			st.Changes += len(ps.Changes)
		}
		// Domain subjects extend indexing on the specs they own (LI on 33.128,
		// glossary on 21.905, …). The core stays generic: no hardcoded spec ids.
		// Degrade-don't-block: a subject error is logged, never fatal.
		for _, s := range reg.Active(ps.Spec.SpecID) {
			n, serr := s.Ingest(ctx, db, subject.IngestContext{
				SpecID: ps.Spec.SpecID, Release: ps.Version.Release, Version: ps.Version.Version,
				ConvertPath: j.path, OriginDir: opt.OriginDir,
				Clauses: ps.Clauses, IsLatest: latest[j.specID] == j.version,
			})
			if serr != nil {
				logf("subject %s on %s: %v", s.Name(), ps.Spec.SpecID, serr)
				continue
			}
			if n > 0 {
				st.SubjectAdded[s.Name()] += n
				logf("subject %s: +%d on %s %s", s.Name(), n, ps.Spec.SpecID, ps.Version.Release)
			}
		}
		// Checkpoint DONE: flips status='done' so the next resume run skips
		// this (spec, version) entirely.
		if err := db.MarkIngestDone(ctx, ps.Spec.SpecID, ps.Version.Version); err != nil {
			return st, fmt.Errorf("mark ingest done %s %s: %w", ps.Spec.SpecID, ps.Version.Version, err)
		}
		// Progress + ETA. With Resume, `skippedDone` rows are real corpus
		// coverage from a previous attempt — we show both numbers so the
		// operator sees both this-run progress AND total accomplished work.
		pct := (i + 1) * 100 / max1(total)
		elapsed := time.Since(startedAt)
		var etaStr string
		if i+1 < total && i+1 > 0 {
			perJob := elapsed / time.Duration(i+1)
			eta := perJob * time.Duration(total-i-1)
			etaStr = fmt.Sprintf(" ETA %s", eta.Round(time.Second))
		}
		logf("[%3d/%d %3d%% +%d resumed]%s ingested %s %s (%s) — %d clauses%s",
			i+1, total, pct, skippedDone, etaStr,
			ps.Spec.SpecID, ps.Version.Version,
			ps.Version.Release, len(ps.Clauses), degradedTag(ps.Degraded))
	}

	// Seed the curated NE<->NF evolution edges (relational stand-in for the V2
	// graph). ReplaceEvolutions = truncate + insert, so a --resume run that
	// kept the existing DB doesn't APPEND a duplicate edge set on the table
	// (which has no uniqueness constraint).
	evos := seedEvolutions()
	if err := db.ReplaceEvolutions(ctx, evos); err != nil {
		return st, err
	}
	st.Evolutions = len(evos)

	if err := db.SetMeta("convert_dir", opt.ConvertDir); err != nil {
		return st, err
	}
	// Stamp the indexing pipeline version so a delta merge can detect an
	// incompatible base (different parser/chunking/schema/embedding model) and
	// rebuild from scratch instead of mixing mechanics (plan §15 invariant #2).
	if err := db.SetMeta("pipeline_version", model.PipelineVersion(opt.Embedder.ModelID())); err != nil {
		return st, err
	}
	// Stamp each subject's footprint so the shard self-describes which subject
	// versions produced it; merge re-stamps authoritatively and publishes the
	// subject-index.json that discover diffs to detect a changed subject (plan
	// TROU #1). subjectmeta is the CGO-free source of truth, kept in lockstep
	// with the registry by TestSubjectMetaMatchesRegistry.
	for _, m := range subjectmeta.All {
		if err := db.SetMeta("subject_fp_"+m.Name, subjectmeta.Footprint(m)); err != nil {
			return st, err
		}
	}
	if opt.EnableFTS {
		if err := db.EnableFTS(ctx); err != nil {
			logf("FTS unavailable, lexical search degrades to LIKE: %v", err)
		} else {
			st.FTS = true
		}
	}
	if st.Vectors {
		// Build-then-freeze (axis #6): CHECKPOINT-fenced build + verify + freeze
		// markers, so serve can open read-only and trust the index. Best-effort:
		// a failure leaves hnsw_state='building' and serve degrades to exact scan.
		// Stamp the embedder's canonical id (e.g. "bge-m3" | "hash-local") so the
		// serve-time coherence guard can refuse a client embedding with a different
		// model (silently-wrong cosine scores). st.Vectors ⇒ embedder enabled.
		model := opt.Embedder.ModelID()
		if err := db.BuildAndFreezeHNSW(ctx, model); err != nil {
			logf("HNSW build-then-freeze failed (vector search degrades to exact scan): %v", err)
		} else {
			st.HNSW = true
		}
	}
	return st, nil
}

// embedClauses vectorises and persists embeddings when the embedder is enabled.
// When floorOrd > 0, only clauses whose release ordinal is >= floorOrd are
// embedded (vectors for recent releases only); every clause stays ingested
// lexically. st.Vectors is set only if something was actually embedded, so an
// all-below-floor corpus doesn't trigger an empty HNSW build downstream.
func embedClauses(ctx context.Context, db *store.Store, e embed.Embedder, clauses []model.Clause, floorOrd int, st *Stats) error {
	if !e.Enabled() || len(clauses) == 0 {
		return nil
	}
	const batch = 32
	for i := 0; i < len(clauses); i += batch {
		end := min(i+batch, len(clauses))
		// Track chunk ids in parallel with texts: clauses skipped by the floor
		// break positional alignment with the returned vectors.
		texts := make([]string, 0, end-i)
		ids := make([]uint64, 0, end-i)
		for _, c := range clauses[i:end] {
			if floorOrd > 0 {
				if o, ok := model.ReleaseOrdinal(c.Release); !ok || o < floorOrd {
					continue // below the vector floor: lexical-only
				}
			}
			texts = append(texts, c.Heading+"\n"+c.Text)
			ids = append(ids, c.ChunkID)
		}
		if len(texts) == 0 {
			continue
		}
		vecs, err := e.Embed(ctx, texts)
		if err != nil {
			return err
		}
		for k, v := range vecs {
			if err := db.SetEmbedding(ctx, ids[k], v); err != nil {
				return err
			}
		}
		st.Vectors = true
	}
	return nil
}

func degradedTag(d bool) string {
	if d {
		return " [degraded]"
	}
	return ""
}

func set(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[strings.TrimSpace(x)] = true
	}
	return m
}

// verLess reports whether version a sorts before b (numeric dotted compare).
func verLess(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func splitVer(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// fileExists is the local truthiness for "the DB on disk is real" — used by
// the Resume gate. Kept separate from internal/embed's `fileExists` to avoid a
// cross-package import for one trivial stat.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// max1 guarantees the divisor of a percentage stays >= 1 so a zero-job run
// doesn't divide-by-zero the progress display.
func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// filterResumeJobs implements the three-step resume orchestration:
//  1. ResetIngestLog drops rows that don't belong to the CURRENT pipeline
//     version (invariant #2: algorithm bump rebuilds from scratch).
//  2. Every (spec, version) still 'started' (a killed runner left a half-
//     ingested spec) is PurgeSpecScope'd so the redo is clean.
//  3. (spec, version) tuples that are 'done' under the current pipeline are
//     filtered out of the job list (the actual skip).
//
// Returns the filtered job list + the count of skipped-because-done so the
// progress line can report "+N resumed" alongside this-run progress.
func filterResumeJobs(ctx context.Context, db *store.Store, jobs []ingestJob, resume bool, pipelineVersion string, logf func(string, ...any)) ([]ingestJob, int, error) {
	if !resume {
		return jobs, 0, nil
	}
	if err := db.ResetIngestLog(ctx, pipelineVersion); err != nil {
		return jobs, 0, fmt.Errorf("reset stale ingest_log: %w", err)
	}
	out := jobs[:0]
	skipped := 0
	for _, j := range jobs {
		done, err := db.IsIngestDone(ctx, j.specID, j.version, pipelineVersion)
		if err != nil {
			return jobs, 0, fmt.Errorf("check ingest_log %s %s: %w", j.specID, j.version, err)
		}
		if done {
			skipped++
			continue
		}
		// status='started' but not 'done' — half-ingested, purge then redo.
		if err := db.PurgeSpecScope(ctx, j.specID, j.version); err != nil {
			return jobs, 0, fmt.Errorf("purge half-ingested %s %s: %w", j.specID, j.version, err)
		}
		out = append(out, j)
	}
	if skipped > 0 {
		logf("resume: %d (spec, version) tuples already done, %d to process this run", skipped, len(out))
	}
	return out, skipped, nil
}
