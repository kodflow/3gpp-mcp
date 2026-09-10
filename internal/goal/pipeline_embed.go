package goal

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
	"github.com/kodflow/3gpp-mcp/internal/embed"
)

// ---------------------------------------------------------------- embedder

func stepBuildEmbedder() *Step {
	return &Step{
		Name:      "build-embedder",
		Version:   1,
		Doc:       "build the GPU dense embedder (ONNX Runtime + CUDA)",
		Deps:      []string{"toolchain"},
		Impl:      []string{"rust/embedder"},
		Toolchain: true,
		Tool:      true,
		Optional:  true, // a machine without a GPU still completes every other step
		Outputs:   func(c *Ctx) []string { return []string{c.rbin("embedder")} },
		Run: func(c *Ctx) error {
			target := filepath.Join(c.Local, "cargo-target")
			c.Log.Printf("cargo build rust/embedder (pulls ONNX Runtime; first build is long)")
			if err := c.Run(Cmd{
				Name: "cargo",
				Args: []string{"build", "--release", "--manifest-path", "rust/embedder/Cargo.toml", "--bin", "embedder"},
				Env:  append([]string{"CARGO_TARGET_DIR=" + target}, gpuEnv(c)...),
				Echo: true,
			}); err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(target, "release", exe("embedder")))
			if err != nil {
				return fmt.Errorf("cargo reported success but the embedder binary is missing: %w", err)
			}
			if err := WriteAtomic(c.rbin("embedder"), b); err != nil {
				return err
			}
			if err := os.Chmod(c.rbin("embedder"), 0o755); err != nil {
				return err
			}
			// Also staged by build-rust, but this step does NOT depend on it: a
			// `--only build-embedder,embed` on a clean .local/rust-bin would otherwise
			// produce a binary that dies with 0xC0000139 before printing a line.
			// stageRuntimeDLLs is idempotent, so staging twice costs a file copy.
			return stageRuntimeDLLs(c)
		},
	}
}

// -------------------------------------------------------------------- embed

func stepEmbed(t corpusTarget) *Step {
	return &Step{
		Name:    "embed" + t.Suffix,
		Version: 2,
		Doc:     "vectorise the corpus on the GPU, reusing every already-seen content hash",
		Deps:    append([]string{"build-embedder"}, t.singleProducer()...),
		// data/3gpp.duckdb has two producers, not one: `merge` folds local shards
		// into it, `seed` downloads the published snapshot. Both are supported
		// paths to a vectorisable corpus. ETSI has a single producer, so it goes
		// through Deps instead (AnyDeps rejects a one-element set on purpose).
		AnyDeps: t.multiProducer(),
		// rust/store/src/vectors.rs closes a FALSE NEGATIVE, and it is the mirror
		// image of the one this file's provenance work has been fixing all along.
		// The ledger import lived in rust/store/src/lib.rs, which `merge` declares
		// and this step did NOT — so editing the import replayed a 38-minute merge
		// that could not be affected by it, and did NOT replay the step whose entire
		// output it decides. Both halves were wrong, in opposite directions.
		Impl: []string{"rust/embedder/src", "rust/store/src/bin/embed_io.rs",
			"rust/store/src/vectors.rs", "internal/embed/identity.go", "internal/embed/models.yaml"},
		// The corpus is NOT an input, although this step reads and rewrites it.
		//
		// It is the step's own product, and compact, index and the paragraph
		// conversion all rewrite it further down the same build. A step that folds
		// it into its fingerprint therefore never sees a stable input: every build
		// changes the corpus after the step recorded it, so the next build replays
		// the step. Same shape as the one that made `merge` replay a 22 GB restore
		// on every run — measured here on 2026-09-03, where a fully VALID pipeline
		// still planned "2 certain to run (2 heavy)" on a corpus nothing had
		// touched.
		//
		// What determines this step's work is declared elsewhere and precisely: its
		// DATA dependency (merge or seed, through provenance), the embed identity
		// and floor in Extra, and — at run time — the corpus's own answer to "does
		// any clause still need one of these". That last question is the honest one,
		// and the step already asks it before declining.
		Inputs: func(c *Ctx) ([]string, error) { return nil, nil },
		Extra: func(c *Ctx) (map[string]string, error) {
			// The embed identity is THE determinant: model family, revision,
			// tokenizer, dimension, normalisation, precision, windowing and
			// max_tokens are all folded into it. A change to any of them must
			// invalidate the vectors and the vector index — and nothing else.
			return map[string]string{
				"embed_identity": embedIdentityForPlan(c),
				"embed_floor":    t.Floor(c),
			}, nil
		},
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{t.ledgerPath(c)} },
		Validate: func(c *Ctx) error {
			// The authoritative check is the DB's own report: no clause at or
			// above the floor may be missing a vector.
			rep, err := embedReport(c, t)
			if err != nil {
				return err
			}
			if rep.Model == "" {
				return fmt.Errorf("the DB carries no embedding_model — vectors were never imported")
			}
			if rep.NullAtFloor > 0 {
				return fmt.Errorf("%d clause(s) at/above %q still have no vector", rep.NullAtFloor, t.Floor(c))
			}
			return nil
		},
		Run: func(c *Ctx) error { return runEmbed(c, t) },
	}
}

// embedIdentity asks cmd/embedid, the single source of the identity that both
// the corpus stamp and the serve-side guard use.
func embedIdentity(c *Ctx) (string, error) {
	out, err := c.Output(Cmd{Name: c.bin("embedid")})
	if err != nil {
		return "", fmt.Errorf("cmd/embedid: %w", err)
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("cmd/embedid returned an empty identity")
	}
	return id, nil
}

// embedIdentityForPlan is the planning-time variant.
//
// Planning must never fail because a tool a LATER step will use has not been
// built yet: on a fresh clone cmd/embedid does not exist until build-go runs, and
// aborting the plan there would make `goal plan` unusable exactly when it is most
// useful. An unresolvable identity is reported as "unresolved", which differs
// from any real digest and therefore keeps the step dirty — the fail-safe
// direction. Once the binary exists the value becomes stable and the step can
// settle into SKIP.
func embedIdentityForPlan(c *Ctx) string {
	id, err := embedIdentity(c)
	if err != nil {
		return "unresolved"
	}
	return id
}

type embedIOReport struct {
	Model       string `json:"model"`
	HNSW        bool   `json:"hnsw"`
	Embedded    int    `json:"embedded_clauses"`
	NullAtFloor int    `json:"null_embeddings_at_floor"`
}

func embedReport(c *Ctx, t corpusTarget) (*embedIOReport, error) {
	out, err := c.Output(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", t.dbPath(c), "--report", "--embed-floor", t.Floor(c),
	}})
	if err != nil {
		// Every caller of this is a Validate, so a corpus somebody else has
		// open must not read as a corpus that needs rebuilding.
		return nil, stillOpenElsewhere(t.DB, err)
	}
	var rep embedIOReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		return nil, fmt.Errorf("embed-io --report is not JSON: %q", out)
	}
	return &rep, nil
}

// runEmbed drives the three-stage dense pass: export the work list, run the GPU,
// import the vectors.
//
// The ledger is the resume unit AND the deduplication unit. rust/embedder keeps
// two maps over it: chunk_ids already written (exact resume after an
// interruption) and content-hash → vector (a clause whose text was already
// embedded under another chunk_id is filled by copy, never by the GPU). On the
// measured corpus that is a 2.74x reduction — 833 924 distinct texts for
// 2 282 337 embeddable clauses, with 79.8% of clauses duplicated verbatim across
// releases.
// ledgerDescribesAnotherBuild answers whether the resume ledger was written against
// a DIFFERENT build of this corpus.
//
// A chunk_id is a POSITION, not an identity: ingest assigns it sequentially
// (offset = max_chunk_id), so rebuilding a corpus from scratch — a different file
// order, or one document yielding a different number of clauses — reuses the same
// numbers for different clauses. The embedder resumes on chunk_id and
// embed-io --import joins the ledger to the corpus on chunk_id, so a ledger from
// another build attaches vectors computed from unrelated text.
//
// Measured 2026-09-02 between two ETSI builds of the SAME 11 821 documents:
// chunk_id 138 was "ETSI TS 101 671 v3.15.1 §10" in one and
// "ETSI EN 300 113-1 v1.3.1 §4" in the other; 1 262 127 clauses were about to be
// skipped on that basis. Every gate passes that corpus — null_at_floor 0,
// clauses_with_text equal to vectors, identities agreeing, HNSW built. Only the
// answers are wrong.
//
// The check is cheap and needs no database: the work list already holds
// (chunk_id, heading, text) for every clause that wants a vector, so a sample of the
// ledger's OLDEST entries — the ones an incremental run never rewrites — can be
// re-hashed and compared. A handful of disagreements is normal (a document really
// was revised); a large share means the numbering itself moved.
func ledgerDescribesAnotherBuild(ledger, worklist string, hashOf func(heading, text string) string) (moved bool, disagree, checked, hashed int) {
	want := map[uint64]string{}
	f, err := os.Open(ledger)
	if err != nil {
		return false, 0, 0, 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	lines := 0
	for sc.Scan() && lines < ledgerSampleSize {
		// The dense ledger names the field "hash", the sparse one "h" — they carry
		// the same thing (the identity of the TEXT the line was computed from) and
		// this check asks the same question of both.
		var rec struct {
			ChunkID uint64 `json:"chunk_id"`
			Hash    string `json:"hash"`
			H       string `json:"h"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		lines++
		if h := firstNonEmpty(rec.Hash, rec.H); h != "" {
			want[rec.ChunkID] = h
		}
	}
	// A file whose lines carry NO hash cannot be verified at all. That is not "no
	// evidence of a problem": it is a file this build has no way to trust, and the
	// sparse postings file was exactly that until it grew an `h` field. Report it as
	// moved so the caller archives it rather than importing 510 384 lines whose ids
	// may name other clauses.
	if len(want) == 0 {
		return lines > 0, 0, 0, 0
	}

	w, err := os.Open(worklist)
	if err != nil {
		return false, 0, 0, len(want)
	}
	defer func() { _ = w.Close() }()
	ws := bufio.NewScanner(w)
	ws.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for ws.Scan() {
		var it struct {
			ChunkID uint64 `json:"chunk_id"`
			Heading string `json:"heading"`
			Text    string `json:"text"`
		}
		if json.Unmarshal(ws.Bytes(), &it) != nil {
			continue
		}
		h, ok := want[it.ChunkID]
		if !ok {
			continue
		}
		checked++
		if hashOf(it.Heading, it.Text) != h {
			disagree++
		}
	}
	// Require a real sample before drawing a conclusion: on a corpus where almost
	// everything is already embedded, the work list is short and may overlap the
	// sample barely at all.
	if checked < ledgerSampleMin {
		return false, disagree, checked, len(want)
	}
	return disagree*100 >= checked*ledgerMovedPercent, disagree, checked, len(want)
}

const (
	// Enough of the ledger's head to be representative without reading a 25 GB file.
	ledgerSampleSize = 5000
	// Below this many actual comparisons the sample says nothing.
	ledgerSampleMin = 200
	// A revised document moves a few hashes; a renumbered corpus moves most of them.
	ledgerMovedPercent = 25
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// sparseResumeHash mirrors rust/embed-core's resume_hash byte for byte: the sha256
// of the text the sparse arm embeds (heading, newline, text, NULs replaced), first
// 16 hex characters. Kept here rather than reusing embed.ClauseHash because the
// sparse ledger does not fold a model identity in — its identity lives in
// schema_meta.sparse_model and is checked by --require-sparse.
func sparseResumeHash(heading, text string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(heading+"\n"+text, "\x00", " ")))
	return hex.EncodeToString(sum[:])[:16]
}

func runEmbed(c *Ctx, t corpusTarget) error {
	db := t.dbPath(c)
	if err := os.MkdirAll(filepath.Join(c.Local, "vecs"), 0o755); err != nil {
		return err
	}
	ledger := t.ledgerPath(c)

	id, err := embedIdentity(c)
	if err != nil {
		return err
	}
	c.Log.Printf("embed identity: %s", id)

	// A changed identity makes every cached vector meaningless: clause_hash folds
	// the identity in, so by_hash would match nothing anyway. Archive rather than
	// delete — reverting a model bump should not cost another full GPU pass.
	idFile := t.ledgerPath(c) + ".identity"
	if prev, err := os.ReadFile(idFile); err == nil && strings.TrimSpace(string(prev)) != id {
		old := strings.TrimSpace(string(prev))
		c.Log.Printf("embed identity changed (%s -> %s): archiving the previous ledger", old, id)
		if fileNonEmpty(ledger) {
			if err := os.Rename(ledger, ledger+"."+old+".bak"); err != nil {
				return err
			}
		}
	}
	if err := WriteAtomic(idFile, []byte(id+"\n")); err != nil {
		return err
	}

	worklist := t.ledgerPath(c) + ".worklist"
	c.Log.Printf("exporting the work list for %s (floor=%q, identity=%s)", t.DB, t.Floor(c), id)
	if err := c.Run(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", db, "--export-worklist", worklist, "--embed-floor", t.Floor(c),
		// Without the identity the export asks "which clauses have no vector"; with it,
		// it can also answer "which vectors were made by a different embedder".
		"--embed-identity", id,
	}}); err != nil {
		return err
	}
	todo := countLines(worklist)
	c.Checkpoint("worklist", strconv.Itoa(todo))
	if todo == 0 {
		c.Log.Printf("every clause already carries a vector under %s — nothing to embed", id)
		// DECLINE rather than return nil. The ledger is a declared output of this step,
		// and with nothing to embed there is no ledger to produce — the identity switch
		// even archives the previous one. Returning nil made the runner report
		// "declared output missing after a successful run" and fail a step that had
		// correctly decided there was no work.
		return fmt.Errorf("%w: no clause needs a vector under %s", ErrDeclined, id)
	}

	// NO WRITE-SHAPE RESTORE HERE ANY MORE.
	//
	// This used to call ensureWriteShape, because the import did `UPDATE clauses` and
	// DuckDB refuses that against a view. The cost was enormous and entirely avoidable:
	// restoring rematerialises every clause's text, which took the real corpus from
	// 11.5 GB to 38.8 GB in 5m36 — before a single vector existed — and the run then
	// needed a full re-compaction to give the space back. Measured on 2026-08-29, that
	// alone is what made a re-embed need ~64 GB free and fail on a machine with 33.
	//
	// The import now writes where the vectors actually live: `bodies`, 821 146 rows
	// instead of 2 752 688, straight through the view's own join. `merge` still restores
	// (it deletes and re-inserts rows of `clauses`, which really does need the table);
	// `embed` no longer has any reason to.
	// A LEDGER FROM ANOTHER BUILD IS WORSE THAN NO LEDGER. Its (hash, vec) pairs stay
	// valuable — they cost GPU time and the identity has not changed — but its
	// chunk_ids no longer name the same clauses, and both the resume and the import
	// key on chunk_id. So archive it and hand it back as a CACHE: --resume-from
	// contributes vectors by content hash, while the fresh ledger carries only ids
	// this build assigned. Nothing is recomputed that does not have to be.
	// AN ARCHIVED LEDGER KEEPS ITS VALUE AS A CACHE beyond the run that archived it.
	// Its chunk_ids are meaningless here, but its (hash, vector) pairs cost hours of
	// GPU and the identity has not changed. Without this, a step that is re-entered —
	// which is what happens when a first pass leaves clauses behind — pays the full
	// GPU price a second time for vectors already sitting on disk.
	//
	// Only the newest archive: each costs several GB of RAM to index by hash, and the
	// newest is the one that overlaps this corpus most.
	var resumeFrom string
	if archives, _ := filepath.Glob(ledger + ".*.otherbuild.bak"); len(archives) > 0 {
		sort.Strings(archives) // the name carries a UTC timestamp, so this is chronological
		resumeFrom = archives[len(archives)-1]
		c.Log.Printf("reusing %s as a vector cache (its ids name another build; its vectors do not)",
			filepath.Base(resumeFrom))
	}
	if fileNonEmpty(ledger) {
		hashOf := func(heading, text string) string { return embed.ClauseHash(heading, text, id) }
		if moved, bad, n, _ := ledgerDescribesAnotherBuild(ledger, worklist, hashOf); moved {
			archive := fmt.Sprintf("%s.%s.otherbuild.bak", ledger, time.Now().UTC().Format("20060102T150405Z"))
			c.Log.Printf("the resume ledger describes ANOTHER build of this corpus "+
				"(%d of %d sampled chunk_ids hash differently) — archiving it to %s and "+
				"reusing it only as a vector cache",
				bad, n, filepath.Base(archive))
			if err := os.Rename(ledger, archive); err != nil {
				return err
			}
			resumeFrom = archive
		}
	}

	before := countLines(ledger)
	c.Log.Printf("%d clause(s) to vectorise (ledger already holds %d)", todo, before)

	if _, err := os.Stat(c.Cfg("model_dir")); err != nil {
		return fmt.Errorf("the BGE-M3 model is missing at %s: %w", c.Cfg("model_dir"), err)
	}

	c.Log.Printf("running the GPU pass — only DISTINCT texts reach the model")
	embedArgs := []string{
		"--in", worklist, "--out", ledger,
		"--model-dir", c.Cfg("model_dir"),
		"--embed-identity", id,
		"--require-cuda",
		"--vram-fraction", "0.8",
		"--max-batch", "512",
	}
	if resumeFrom != "" {
		embedArgs = append(embedArgs, "--resume-from", resumeFrom)
	}
	if err := c.Run(Cmd{Name: c.rbin("embedder"), Args: embedArgs,
		Env: gpuEnv(c), Echo: true}); err != nil {
		// The ledger is append-only and flushed per batch, so a failure here is
		// resumable: report where we got to rather than losing the position.
		c.Checkpoint("ledger_lines", strconv.Itoa(countLines(ledger)))
		return err
	}
	after := countLines(ledger)
	c.Checkpoint("ledger_lines", strconv.Itoa(after))
	c.Log.Printf("ledger grew from %d to %d vectors", before, after)

	c.Log.Printf("importing the vectors into the corpus")
	if err := c.Run(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", db, "--import-vectors", ledger, "--embed-identity", id,
	}, Echo: true}); err != nil {
		return err
	}
	return nil
}

func countLines(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	n := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

// ------------------------------------------------------------------- enrich

// stepEnrich is the enrichment pass of ONE half of the corpus.
//
// BOTH HALVES HAVE ONE NOW, and the parameter is what says so. The 3GPP arm
// overlays the DynaReport catalogue, the 5GC OpenAPI corpus, the LI registry and
// the two curated seeds; the ETSI arm mines the vocabulary each deliverable
// declares about itself. The two bodies share nothing — different sources,
// different binaries — and unifying them would be a false abstraction. What they
// share is the NAME, the contract and the position in the DAG, and the ETSI half
// had none of the three.
//
// WHAT THIS REPAIRS, and it is not a nicety.
// rust/ingest/src/bin/ingest_glossary.rs was written, tested, built by
// `build-rust` — and run by NO step. The 4 941 acronyms the shipped ETSI half
// carries got there from .local/resume/v3-chain2.sh: by hand, once, in a session
// nobody can replay. So a fresh clone built an ETSI corpus with an EMPTY
// glossary and reported success, and a deliverable fetched after that never
// contributed its Abbreviations clause to a corpus already built. This file
// names that failure one step away, in stepEnrich's own words — "A pipeline that
// names the command instead of running it has not built the product" — and here
// the command was not even named.
func stepEnrich(t corpusTarget) *Step {
	if t.Suffix != "" {
		return stepEnrichETSI(t)
	}
	return &Step{
		Name:    "enrich",
		Version: 1,
		Doc:     "overlay the DynaReport catalogue, the 5GC OpenAPI corpus and the LI registry",
		Deps:    []string{"merge"},
		// The two fetch scripts are part of this step's implementation now that it
		// runs them: changing how an overlay is acquired must replay the overlay.
		// internal/evolseed is implementation here because this step now APPLIES
		// the seed: editing an edge must replay the overlay, exactly as editing an
		// extractor does. Before, the seed's hash moved the published identity
		// while nothing wrote the seed — see cmd/seed-evolutions.
		// THE THREE BINARIES IT RUNS, NOT THE DIRECTORY THEY SIT IN. This used to
		// declare rust/ingest/src/bin whole, and a test recorded that as deliberate:
		// every file in there was one of this step's overlays. That stopped being
		// true the moment the ETSI arm got an enrich of its own, because
		// ingest_glossary.rs lives in the same directory and this step never runs
		// it — so editing the ETSI glossary miner would replay the 3GPP catalogue
		// overlay, and paragraphs, sparse, compact, index and publish behind it.
		// That is the shape ingest-etsi paid ~1 h and 18.8 GiB to learn on
		// 2026-09-06, in the opposite direction.
		// rust/store/src/changes.rs is named HERE and nowhere else, which is the
		// whole reason it is a file of its own. The changelog writer links the
		// store library, and `merge` declares rust/store/src/lib.rs — so leaving
		// replace_changes in lib.rs would have made every edit to the changelog
		// replay merge (34m15), then paragraphs (20m32), compact (19m22) and index
		// (8m06), and re-push 42 GB, to rewrite a table none of them read. Split
		// out, an edit to the changelog costs one replay of this step. See
		// changes.rs for why the narrow declaration is correct rather than merely
		// cheap, and TestEnrichDeclaresTheChangelogWriter for what holds it there.
		//
		// THE MANIFESTS AND THE LOCKFILE ARE PART OF THE IMPLEMENTATION, and their
		// absence here was a gap the ETSI arm did not have — stepEnrichETSI has
		// declared them since it was written. This step runs four Rust binaries and
		// declared only their sources: `cargo update` alone, or a feature change in
		// a manifest, produces a different binary from identical sources, and
		// `build-rust` is a Step.Tool that never replays a data step. The corpus
		// would then carry a catalogue, an API surface, an LI registry and a
		// changelog written by a binary that no longer exists, with nothing to show
		// it. rust/parse's manifest is already covered by the directory below.
		Impl: []string{"rust/ingest/src/bin/ingest_catalog.rs", "rust/ingest/src/bin/ingest_openapi.rs", "rust/ingest/src/bin/ingest_li.rs", "rust/ingest/src/bin/ingest_crs.rs", "rust/ingest/Cargo.toml", "rust/store/src/changes.rs", "rust/store/src/lib.rs", "rust/store/Cargo.toml", "rust/Cargo.toml", "rust/Cargo.lock", "rust/parse", "scripts/fetch-5g-apis.sh", "scripts/fetch-li-asn.sh", "scripts/fetch-crdb.sh", "internal/evolseed", "cmd/seed-evolutions", "internal/abbrev", "internal/glossaryseed", "cmd/seed-glossary"},
		// A _test.go CANNOT CHANGE WHAT THIS STEP DOES. The four Go packages above
		// are named as DIRECTORIES, so every test file in them counted toward this
		// step's fingerprint — and this step runs binaries, it does not compile
		// them. Measured on build E (2026-09-09): adding one test to
		// internal/abbrev replayed enrich (1m05), then paragraphs, sparse (138.6 s)
		// and index, on a commit whose PR body said "tests only, no production code
		// changes"; the corpus it rewrote then had to be re-pushed.
		//
		// The flag and its reasoning already exist for the build steps
		// (fingerprint.go, isTestArtefact). What was missing is that the same
		// reasoning applies to a step that RUNS a binary, not only to one that
		// links it. `test` is the only step that must count test files, and it
		// deliberately does not set this.
		ExcludeTests: true,
		Inputs: func(c *Ctx) ([]string, error) {
			// data/sources/asn joins the inputs for the same reason 5g-apis is
			// already here: acquiring the LI registry must make the overlay dirty,
			// or the corpus keeps whatever li_events it had.
			//
			// ENUMERATED FILE BY FILE, because naming the directories read as
			// watching them and watched nothing. inputsHash records a directory as
			// the constant string "dir" (fingerprint.go), so neither tree could
			// ever change this step's fingerprint: acquiring the LI registry or a
			// new 5GC API left the overlay "unchanged" — the exact opposite of what
			// the paragraph above asks for. The only real determinant here was
			// status-report.htm, and until runDiscover stopped renaming it over
			// itself, that one moved on every single run. So this step was
			// simultaneously blind to what it watched and dirty for what it did not.
			in := []string{c.statePath("status-report.htm")}
			for _, d := range []string{"5g-apis", "asn", "crdb"} {
				files, err := filesUnder(c.dataPath("sources", d))
				if err != nil {
					return nil, err
				}
				in = append(in, files...)
			}
			return in, nil
		},
		Heavy: true,
		Validate: func(c *Ctx) error {
			// doc_type is hard-coded to "TS" by the parser until the catalogue
			// overlay corrects it, so an un-enriched corpus cannot honour "filter
			// to TS by default". Prove the overlay actually landed.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}})
			if err != nil {
				return stillOpenElsewhere("3gpp.duckdb", err)
			}
			if !strings.Contains(out, "spec_versions=") {
				return fmt.Errorf("dbcount produced no counters")
			}
			return nil
		},
		Run: func(c *Ctx) error {
			db := c.dataPath("3gpp.duckdb")
			report := c.statePath("status-report.htm")

			// MANDATORY, not optional: without it doc_type stays "TS" for every
			// spec, working_group and title stay empty, and cmd/validate's
			// --max-empty-meta gate fails.
			args := []string{"--db", db}
			if fileNonEmpty(report) {
				args = append(args, "--status-report", report)
			}
			c.Log.Printf("catalogue overlay (doc_type, working_group, freeze_date)")
			if err := c.Run(Cmd{Name: c.rbin("ingest-catalog"), Args: args, Echo: true}); err != nil {
				return err
			}

			// THE CHANGELOG. `changes` had no writer between Phase 11b (c635038),
			// which deleted the Go HTML-ingest write side, and this step: the
			// table sat as a fossil of 61 321 rows over 3 452 specs, only 311 of
			// them citable and 3 026 of them the literal string "Date" — the
			// change-history table's column header, read positionally as a body
			// row. PR #311 stopped get_changelog serving that header; it could
			// not give the table a source.
			//
			// The source is the CR database, not the change-history table printed
			// in each spec. The printed table is a RENDERING of that database, and
			// parsing the documents would cover only the third of the archive that
			// happens to be on disk after a delta fetch — 1 038 specs of 3 568 on
			// this machine — and in fact none of it, because ingest --resume skips
			// a (spec, version) the corpus already holds whatever the parser
			// version says. See parse3gpp::crdb.
			if crdb := findCRDB(c); crdb == "" || refreshOverlays() {
				c.Log.Printf("no 3GPP CR database — acquiring it (scripts/fetch-crdb.sh)")
				if err := c.Run(Cmd{Name: "bash", Args: []string{"scripts/fetch-crdb.sh"}, Echo: true}); err != nil {
					c.Log.Printf("the CR database fetch failed (%v) — continuing with what is on disk", err)
				}
			}
			if crdb := findCRDB(c); crdb != "" {
				c.Log.Printf("change-request overlay from %s", filepath.Base(crdb))
				if err := c.Run(Cmd{Name: c.rbin("ingest-crs"), Args: []string{"--db", db, "--crdb", crdb}, Echo: true}); err != nil {
					return err
				}
			} else {
				c.Log.Printf("no CRDB_*.zip and none could be fetched — get_changelog keeps whatever it holds")
			}

			// The two external overlays acquire themselves when absent.
			//
			// They used to be a log line telling the operator to run a script —
			// "run scripts/fetch-5g-apis.sh to add it" — which meant a fresh clone
			// completed all 19 steps, reported success, and served an empty
			// `search_api` and an empty `li_events`. A pipeline that names the
			// command instead of running it has not built the product.
			//
			// Only when ABSENT, and never fatal. Refreshing is a separate,
			// deliberate act (OVERLAY_REFRESH=1, or run the scripts): re-fetching
			// on every enrich would re-download the release archives to discover
			// nothing changed, and an offline machine must still finish the run
			// with whatever it already has.
			apis := c.dataPath("sources", "5g-apis")
			if !dirExists(apis) || refreshOverlays() {
				c.Log.Printf("no 5GC OpenAPI corpus — acquiring it (scripts/fetch-5g-apis.sh auto)")
				if err := c.Run(Cmd{Name: "bash", Args: []string{"scripts/fetch-5g-apis.sh", "auto"}, Echo: true}); err != nil {
					c.Log.Printf("the OpenAPI fetch failed (%v) — continuing with what is on disk", err)
				}
			}
			if dirExists(apis) {
				c.Log.Printf("5GC OpenAPI overlay")
				if err := c.Run(Cmd{Name: c.rbin("ingest-openapi"), Args: []string{"--src", apis, "--db", db}, Echo: true}); err != nil {
					return err
				}
			} else {
				c.Log.Printf("no data/sources/5g-apis and none could be fetched — search_api will answer from nothing")
			}

			// The TS 33.128 ASN.1 registry is not published on its own: it rides in
			// a zip inside the zip of the spec, and this machine purges the origin
			// archives after conversion, so it has to be refetched rather than
			// found.
			if findASN(c) == "" || refreshOverlays() {
				c.Log.Printf("no TS 33.128 ASN.1 registry — acquiring it (scripts/fetch-li-asn.sh)")
				if err := c.Run(Cmd{Name: "bash", Args: []string{"scripts/fetch-li-asn.sh"}, Echo: true}); err != nil {
					c.Log.Printf("the LI registry fetch failed (%v) — continuing with what is on disk", err)
				}
			}
			if asn := findASN(c); asn != "" {
				c.Log.Printf("Lawful Interception registry from %s", filepath.Base(asn))
				if err := c.Run(Cmd{Name: c.rbin("ingest-li"), Args: []string{"--db", db, "--asn", asn}, Echo: true}); err != nil {
					return err
				}
			} else {
				c.Log.Printf("no TS33128Payloads .asn and none could be fetched — li_events stays empty")
			}

			// The curated NE->NF edge seed. It is applied LAST because it verifies
			// each citation against the clauses this corpus actually holds, so it
			// wants the catalogue overlay already in place.
			c.Log.Printf("NE->NF evolution seed (curated, citations checked against the corpus)")
			if err := c.Run(Cmd{Name: c.bin("seed-evolutions"), Args: []string{"--db", db}, Echo: true}); err != nil {
				return err
			}

			// The glossary each spec declares about itself. It runs after the
			// catalogue overlay for the same reason the edge seed does — it
			// reads clauses out of this corpus — and it is what stops
			// resolve_term answering "Authentication Management Field" when
			// asked what an AMF is. Additive and idempotent: it upserts, so the
			// TS 21.905 and ETSI entries stay exactly where they are and only
			// the ORDER a reader sees changes.
			c.Log.Printf("glossary seed (each spec's own Abbreviations clause)")
			return c.Run(Cmd{Name: c.bin("seed-glossary"), Args: []string{"--db", db}, Echo: true})
		},
	}
}

// stepEnrichETSI mines the vocabulary the ETSI half declares about itself.
//
// ETSI HAS NO TS 21.905. There is no vocabulary deliverable to ingest once: every
// TS/EN carries its own "Abbreviations" clause, so the vocabulary is spread over
// the whole archive and mining it is a pass over the converted HTML — the same
// shape as ingest-catalog / ingest-li / ingest-openapi on the other arm, which is
// why ingest-glossary was written as one.
//
// IT WRITES ONLY `acronyms`. It reads the converted HTML, never a clause, and it
// touches no table the content-addressed conversion owns — so its position
// relative to `paragraphs-etsi` is a matter of symmetry with the 3GPP arm, not a
// constraint. It declares NO Outputs for the same reason the 3GPP arm declares
// none: naming the corpus here would make every glossary refresh report
// "dependency output changed" to paragraphs-etsi and replay the conversion, the
// sparse arm, the compaction and the publish behind it — hours, to add a row to
// a table none of them read.
func stepEnrichETSI(t corpusTarget) *Step {
	return &Step{
		Name:    "enrich" + t.Suffix,
		Version: 1,
		Doc:     "mine each ETSI deliverable's own Abbreviations clause into the glossary",
		Deps:    []string{"ingest-etsi", "build-rust"},
		// NAMED FILES, NOT THE CRATE — the declaration ingest-etsi carries, for the
		// reason measured on 2026-09-06: naming rust/ingest/src/bin made a fix to
		// ingest_li.rs invalidate the whole ETSI half (~1 h of rework, 18.8 GiB
		// re-pushed) over a binary that half never runs. This step runs exactly
		// one, so it names exactly its source and the crates it links.
		Impl: []string{
			"rust/ingest/src/bin/ingest_glossary.rs", "rust/ingest/Cargo.toml",
			// The extraction rule and the ETSI provenance header it keys on — AND the
			// crate's manifest, for the reason rust/ingest/Cargo.toml is here: a
			// dependency or feature change produces a different binary from identical
			// sources, and build-rust is a Tool that never replays a data step.
			"rust/parse/src/glossary.rs", "rust/parse/src/etsi.rs", "rust/parse/Cargo.toml",
			"rust/store/src/lib.rs", "rust/store/Cargo.toml",
			// The workspace manifest and LOCKFILE: `cargo update` alone can change
			// the binary, and build-rust is a Tool that never replays a data step.
			"rust/Cargo.toml", "rust/Cargo.lock",
			"internal/store/schema.sql",
		},
		// NO Inputs, AND NAMING THE CONVERTED TREE HERE WOULD BE WORSE THAN NONE.
		//
		// This step reads data/sources/convert-etsi, so declaring that directory is
		// the obvious thing to write — and it is the exact declaration `enrich`
		// carries a comment about, forty lines up, after it cost two months:
		// inputsHash records a directory as the constant string "dir"
		// (fingerprint.go), so the tree is DECLARED and never watched. A hundred new
		// deliverables would land and the fingerprint would not move.
		//
		// Enumerating the files instead would be honest but wrong here: 11 822 paths
		// for a pass that reads the newest 5 142 of them, re-stat-ed on every plan.
		//
		// What actually determines this step's work is its DATA dependency, which is
		// what stepParagraphs says in the same situation and for the same reason:
		// ingest-etsi declares data/etsi.duckdb as its output, so a fetch that
		// brought new deliverables shows up here as "dependency output changed", and
		// an ingest-etsi that declined leaves this correctly skipped.
		Inputs: func(c *Ctx) ([]string, error) { return nil, nil },
		Heavy:  true,
		Validate: func(c *Ctx) error {
			// LOCAL and CHEAP, the doctrine validatePublished states and
			// stepParagraphs.Validate had to be taught: this runs on EVERY plan,
			// including plans where the step would be skipped. One dbcount — the
			// same call the 3GPP arm's Validate already makes on its own half, a
			// few seconds — never a re-mine of the corpus.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", t.dbPath(c)}})
			if err != nil {
				return stillOpenElsewhere(t.DB, err)
			}
			n, ok := counterValue(out, "acronyms")
			if !ok {
				return fmt.Errorf("dbcount produced no glossary counter")
			}
			// AN EMPTY GLOSSARY IS THE STATE THIS STEP EXISTS TO END, and it is
			// indistinguishable from a good run unless the counter is READ rather
			// than merely printed. Every ETSI TS/EN carries a clause 3.
			if n == 0 {
				return fmt.Errorf("the ETSI glossary is empty — resolve_term would answer from the 3GPP half alone")
			}
			c.Log.Printf("ETSI glossary: %d acronym row(s)", n)
			return nil
		},
		Run: func(c *Ctx) error {
			c.Log.Printf("mining the Abbreviations clause of each ETSI deliverable (newest version of each)")
			return c.Run(Cmd{Name: c.rbin("ingest-glossary"), Args: []string{
				"--convert", c.dataPath("sources", "convert-etsi"),
				"--db", t.dbPath(c),
			}, Echo: true})
		},
	}
}

// counterValue pulls "<key>=<n>" out of a counter-printing binary's output.
//
// strings.Contains(out, "acronyms=0") is what this replaces, and it is wrong in
// both directions: it matches the prefix of "acronyms=0" in a longer number and
// it cannot tell "absent" from "zero" — the two states a Validate most needs to
// separate, since an absent counter means the binary is older than the check
// while a zero means the corpus is empty.
func counterValue(out, key string) (int64, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, key+"=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// findASN returns the TS 33.128 payload registry of the NEWEST release on disk.
//
// filepath.Walk visits lexically, so "first hit wins" hands back Rel-17 while
// Rel-19 sits in the next directory: li_events would then describe the events of
// a spec three releases behind the text the corpus actually serves. Rank by
// release number instead, and fall back to the lexically last path for a file
// whose path carries no Rel-NN at all.
func findASN(c *Ctx) string {
	best, bestRel := "", -1
	for _, base := range []string{c.dataPath("sources"), c.dataPath("asn")} {
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasPrefix(info.Name(), "TS33128Payloads") || !strings.HasSuffix(info.Name(), ".asn") {
				return nil
			}
			if r := releaseNumber(p); r > bestRel || (r == bestRel && p > best) {
				best, bestRel = p, r
			}
			return nil
		})
	}
	return best
}

// findCRDB returns the newest CR-database export on disk, or "".
//
// Newest by NAME, because the name IS the export date (CRDB_20260715.zip) and 3GPP
// republishes under a new one rather than overwriting. Sorting by mtime instead
// would rank a re-downloaded old export above a newer one, which is exactly what
// happens when a fetch is retried — and the changelog would then quietly regress to
// an earlier state of the database with nothing to show for it.
func findCRDB(c *Ctx) string {
	matches, err := filepath.Glob(filepath.Join(c.dataPath("sources", "crdb"), "CRDB_*.zip"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

// releaseNumber pulls NN out of a "Rel-NN" path element, or -1 when there is none.
func releaseNumber(p string) int {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if !strings.HasPrefix(part, "Rel-") {
			continue
		}
		if n, err := strconv.Atoi(part[len("Rel-"):]); err == nil {
			return n
		}
	}
	return -1
}

// -------------------------------------------------------------------- index

func stepIndex(t corpusTarget) *Step {
	return &Step{
		Name:    "index" + t.Suffix,
		Version: 1,
		Doc:     "build and freeze the HNSW cosine index over the vectors of " + t.DB,
		Deps:    t.indexDeps(),
		Impl:    []string{"cmd/freeze-hnsw", "internal/store/hnsw.go"},
		Extra: func(c *Ctx) (map[string]string, error) {
			// The vector index is a DERIVED CACHE of the vectors. Its identity is
			// the embed identity plus the index parameters; anything else (the
			// server code, the docs) must not invalidate it.
			//
			// "the index parameters" has to mean ALL of them. This map listed only
			// metric and dim while the build also takes M, ef_construction and
			// ef_search, so changing the graph's shape left the fingerprint identical
			// and the step SKIPPED — a corpus served by an index built to parameters
			// nobody asked for, with the plan reporting it as up to date. The
			// build-side defaults live in internal/store (hnswM/hnswEfConstruction/
			// hnswEfSearch) and are read here through the same env overrides, so the
			// fingerprint tracks what the build will actually do.
			return map[string]string{
				"embed_identity":       embedIdentityForPlan(c),
				"metric":               "cosine",
				"dim":                  "1024",
				"hnsw_m":               envOr("HNSW_M", "32"),
				"hnsw_ef_construction": envOr("HNSW_EF_CONSTRUCTION", "128"),
				"hnsw_ef_search":       envOr("HNSW_EF_SEARCH", "128"),
			}, nil
		},
		Heavy: true,
		Validate: func(c *Ctx) error {
			rep, err := embedReport(c, t)
			if err != nil {
				return err
			}
			if !rep.HNSW {
				return fmt.Errorf("the DB reports no frozen HNSW index")
			}
			return nil
		},
		Run: func(c *Ctx) error {
			rep, err := embedReport(c, t)
			if err != nil {
				return err
			}
			if rep.Embedded == 0 {
				return fmt.Errorf("refusing to build a vector index over zero vectors in %s", t.DB)
			}
			// GIVE THE BUILD ITS CEILINGS. THE DEFAULTS ARE THE SLOW PATH.
			//
			// freeze-hnsw reads HNSW_BUILD_MEMORY_LIMIT and HNSW_BUILD_TEMP_LIMIT, and
			// the step never set either — so DuckDB used its own defaults: a buffer
			// sized from physical RAM, and a spill budget of 90% of whatever the disk
			// happened to have free. That is not a guard, it is a race between the
			// index and the file it is being written into: the 2026-08-25 run reported
			// "Espace insuffisant sur le disque" with 57 GB free, because the spill was
			// allowed to claim 51 of them while the corpus was still growing.
			//
			// The values are the ones a real build was measured at: 2 748 971 vectors
			// froze in 19m05 with a 10 GB buffer and a 16 GB spill cap.
			//
			// They were briefly 8 GB, taken from a 1m46 run — which was a no-op, since
			// that corpus already carried the index and CREATE INDEX IF NOT EXISTS did
			// nothing. Calibrating on it produced a budget too small for the real thing:
			// "could not allocate block of size 256.0 KiB (7.4 GiB/7.4 GiB used)". A
			// timing taken from a step that skipped its own work is not a measurement.
			//
			// An operator can still override either.
			env := []string{
				"HNSW_BUILD_MEMORY_LIMIT=" + envOr("HNSW_BUILD_MEMORY_LIMIT", "10GB"),
				"HNSW_BUILD_TEMP_LIMIT=" + envOr("HNSW_BUILD_TEMP_LIMIT", "16GB"),
			}
			c.Log.Printf("freezing the HNSW index over %d vectors in %s (%s)", rep.Embedded, t.DB, strings.Join(env, " "))
			// The Go builder, not the Rust one: it puts the index on whichever table
			// holds the vectors. rust/store names `clauses`, which on a converted
			// corpus is a view over 2 752 688 occurrences of 897 556 vectors — and
			// DuckDB will not index a view in any case. Both shapes work here.
			return c.Run(Cmd{Name: c.bin("freeze-hnsw"), Args: []string{"--db", t.dbPath(c)}, Env: env, Echo: true})
		},
	}
}

// envOr returns the process environment's value for `key`, or `def` when it is unset
// or empty. Used for the tuning ceilings a step supplies but an operator may override.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// ------------------------------------------------------------------ validate

// stepValidate runs the data-completeness contract against ONE corpus.
//
// IT IS PER CORPUS NOW, AND THAT IS THE HOLE IT CLOSES. There used to be a single
// `validate` step: it ran the whole contract on data/3gpp.duckdb and judged the
// ETSI half by one composite flag, --require-etsi. So the 3GPP corpus answered for
// --require-fts, --require-hnsw, --require-embed-complete and --require-sparse,
// and the ETSI corpus -- the same writers, the same freeze, a corpus of comparable
// size -- answered for none of them. An ETSI FTS index that failed to build, or a
// sparse layer that came out empty, could not fail a gate, because no gate looked
// at it. That is the same shape as the ETSI half having no enrich step: the
// tooling existed and nothing was on the path that runs.
//
// THE TWO ARMS ASK ONE FILE FOR THEIR FLAGS. scripts/data-contract.sh takes the
// arm as an argument and answers with the same checks either way, minus the one
// flag that is about the PAIR rather than about a corpus. A second copy of the
// contract would be a second contract.
//
// THE 3GPP ARM DEPENDS ON index-etsi, AND THAT IS NOT COSMETIC ORDERING. Its
// contract carries --require-etsi, which asserts the ETSI half's HNSW is FROZEN --
// and the step that freezes it is index-etsi. With only "index" declared, validate
// ran between the 3GPP index and the ETSI one and failed on a corpus that was
// perfectly fine:
//
//	[FAIL] require-etsi: … hnsw_state="building"
//	       the server would REFUSE the ETSI index: hnsw not frozen
//
// Measured on build 24 (2026-09-07), the first run of the strengthened contract.
// The ordering defect was always there; the weak contract never looked at the ETSI
// half, so nothing could see it. A gate that is not on the path proves nothing.
//
// The ETSI arm does NOT name `index` in return. It reads one corpus and compares
// it against nothing else, so declaring the other half's freeze would be an
// over-broad declaration -- the defect this file names eleven times -- and would
// replay the ETSI gate every time the 3GPP index moved.
//
// validate is the LAST gate before smoke and publish, so it must come after
// everything it inspects. Declaring the dependency rather than relying on list
// order is what makes that true of the DAG and not just of this file.
func stepValidate(t corpusTarget) *Step {
	return &Step{
		Name:    "validate" + t.Suffix,
		Version: 2,
		Doc:     "run the data-completeness contract against the finished corpus",
		Deps:    t.validateDeps(),
		Impl:    []string{"cmd/validate", "cmd/anchorcheck", "scripts/data-contract.sh", "contracts/accepted-absences.txt"},
		Inputs: func(c *Ctx) ([]string, error) {
			in := []string{t.dbPath(c)}
			// THE ETSI GATE READS TWO MORE FILES, SO IT WATCHES THEM.
			//
			// --require-worklist reconciles the work list and the absence register
			// against the corpus. Declaring only the DB would record the BUTTON and
			// not the work: excusing a deliverable in the register, or a re-discovery
			// that changes the work list, would leave this step's fingerprint
			// untouched, the gate would SKIP, and the change would land inert with
			// every gate green. That is the defect this pipeline posed on 2026-09-08
			// with etsi_scope, one step to the left.
			//
			// Absent files are skipped rather than erroring: on the 3GPP arm neither
			// exists, and on a first ETSI build the register is written by the fetch
			// that runs later in the same pipeline.
			if t.Suffix != "" {
				for _, f := range []string{
					c.statePath("etsi-worklist.tsv"),
					c.statePath("etsi-absences.tsv"),
				} {
					if fileNonEmpty(f) {
						in = append(in, f)
					}
				}
			}
			return in, nil
		},
		Run: func(c *Ctx) error {
			args := validateArgs(c, t)
			c.Log.Printf("contract: %s (embed floor %q)", c.Cfg(t.ContractKey), t.Floor(c))
			// SELECT THE SPARSE-CAPABLE REGISTRY ENTRY when the contract asks about
			// the sparse layer, exactly as runSparse does to resolve the identity it
			// stamps. cmd/validate compares schema_meta.sparse_model against
			// embed.SparseModelID(), which reads the ACTIVE model -- and the default
			// entry (bge-m3) is dense-only, so it resolves nothing and the comparison
			// cannot happen. Leaving that to the operator's environment is a footgun:
			// the same flag would check the layer on one machine and refuse to on
			// another. Selecting the dual-head entry does not move the DENSE identity
			// (38067f8c6efe under both), so this changes what is CHECKED, never what
			// is expected of the corpus.
			var env []string
			if hasFlag(strings.Fields(c.Cfg(t.ContractKey)), "--require-sparse") {
				env = append(env, "EMBED_MODEL="+sparseModelName)
			}
			if err := c.Run(Cmd{Name: c.bin("validate"), Args: args, Env: env, Echo: true}); err != nil {
				return err
			}
			// THE ANCHOR IS A 3GPP ARTEFACT, and this is not the ETSI arm being
			// treated as second class. The delta anchor is .local/corpus-index.json,
			// which `merge` writes out of the 3GPP shards; the ETSI ingest produces
			// one database directly and there is no anchor to check against. Running
			// anchorcheck here would point it at the 3GPP corpus from the ETSI gate,
			// which is the same check twice under a name that says otherwise.
			if t.Suffix != "" {
				return nil
			}
			return validateAnchor(c)
		},
	}
}

// validateArgs builds the contract command line for one corpus.
//
// --require-embed-complete is FLOOR-AWARE, and the only floor that makes it mean
// anything is the one `embed` actually ran with: clauses below it are deliberately
// left NULL (cmd/validate: "Below-floor/legacy clauses are intentionally NULL and
// never counted"). Leaving the floor out made the contract demand vectors for a
// population embed was never asked to cover, and it failed a corpus that was in
// fact complete -- 413 pre-Rel-99 LCS clauses (GSM-era 03.71) against
// embed_floor="Rel-99", while validate counted at floor "".
//
// THE FLOOR COMES FROM THE TARGET, not from corpus3GPP(). corpusETSI().Floor is
// deliberately empty, because clauses_needing_embedding skips any clause whose
// release has no ordinal once a floor is set and an ETSI release is the constant
// "ETSI" -- so a floor on this arm would select ZERO clauses and the strongest
// check in the contract would report [ok] over an unvectorised corpus. Reading the
// 3GPP floor here would have reintroduced exactly that, one gate later than the
// place it is already documented.
//
// An explicit --embed-floor in the contract flags still wins: an operator
// overriding the contract by hand is a decision, not an accident.
func validateArgs(c *Ctx, t corpusTarget) []string {
	args := []string{"--db", t.dbPath(c), "--report", "text"}
	flags := strings.Fields(c.Cfg(t.ContractKey))
	args = append(args, flags...)
	if floor := t.Floor(c); floor != "" && !hasFlag(flags, "--embed-floor") {
		args = append(args, "--embed-floor", floor)
	}
	return args
}

// hasFlag reports whether `name` (given as "--embed-floor") is already present in
// a hand-written flag list. Go's flag package accepts one dash or two, and a value
// either as the next argument or after "=", so all four spellings must match or the
// caller would silently pass the flag twice.
func hasFlag(args []string, name string) bool {
	short := strings.TrimPrefix(name, "-") // "-embed-floor" -> "embed-floor"
	for _, a := range args {
		a = strings.TrimLeft(a, "-")
		if i := strings.IndexByte(a, '='); i >= 0 {
			a = a[:i]
		}
		if a == strings.TrimLeft(short, "-") {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------------- smoke

func stepSmoke() *Step {
	return &Step{
		Name: "smoke",
		// 3: the smoke now FAILS a tool that answers isError, an empty answer to a
		// probe that must find something, and a probe the server does not expose —
		// all judged in internal/goal, which smoke's fingerprint does not cover, so
		// only the version can say the step's meaning moved.
		Version: 3,
		Doc:     "start the real server over stdio, prove every probe answers, and that vector search stays enabled",
		// BOTH GATES, because there are two now. Naming only "validate" would let
		// the smoke -- and `publish` behind it -- run while the ETSI contract had
		// not been applied, which is the state this whole split ends.
		Deps: []string{"validate", "validate-etsi"},
		Impl: []string{"cmd/server", "internal/mcp", "internal/search"},
		Inputs: func(c *Ctx) ([]string, error) {
			in := []string{c.dataPath("3gpp.duckdb")}
			// The ETSI corpus is served ALONGSIDE, so a change to it changes what
			// the smoke proves. A step that ignores an input it reads reports
			// VALID over stale evidence.
			if etsi := c.dataPath("etsi.duckdb"); fileNonEmpty(etsi) {
				in = append(in, etsi)
			}
			return in, nil
		},
		Run: func(c *Ctx) error { return runSmoke(c) },
	}
}

// runSmoke drives the shipped binary the way a client does.
//
// A green `go test ./...` proves the packages behave; it does NOT prove the
// product works. The specific failure this exists to catch is the one that
// shipped for months: a corpus full of valid vectors served as pure lexical
// because the startup coherence guard disabled vector search. That is invisible
// to unit tests and to `cmd/validate` — only a real startup shows it.
func runSmoke(c *Ctx) error {
	db := c.dataPath("3gpp.duckdb")
	args := []string{"serve", "--db", db}
	// --etsi-db is a shipped flag (cmd/server/main.go) that no end-to-end run had
	// ever exercised: the ETSI corpus was built, validated, then never served.
	// Launch the server the way the product is meant to be launched.
	etsiAttached := false
	if etsi := c.dataPath("etsi.duckdb"); fileNonEmpty(etsi) {
		args = append(args, "--etsi-db", etsi)
		etsiAttached = true
		c.Log.Printf("attaching the ETSI corpus alongside (split federation)")
	}
	cmd := exec.CommandContext(c.Context, c.bin("server"), args...)
	cmd.Dir = c.Root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	send := func(v any) error {
		b, _ := json.Marshal(v)
		_, err := stdin.Write(append(b, '\n'))
		return err
	}
	rd := bufio.NewReader(stdout)
	readOne := func() (map[string]any, error) {
		type res struct {
			line string
			err  error
		}
		ch := make(chan res, 1)
		go func() {
			l, e := rd.ReadString('\n')
			ch <- res{l, e}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, fmt.Errorf("%w — %s", r.err, serverPostmortem(cmd, &stderr))
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(r.line), &m); err != nil {
				return nil, fmt.Errorf("server sent a non-JSON line: %q", strings.TrimSpace(r.line))
			}
			return m, nil
		case <-time.After(120 * time.Second):
			return nil, fmt.Errorf("the server did not answer within 120s (stderr: %s)", tailString(stderr.String(), 12))
		}
	}

	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "goal-smoke", "version": "1"},
		},
	}); err != nil {
		return err
	}
	if _, err := readOne(); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	c.Log.Printf("server initialised")

	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}); err != nil {
		return err
	}
	toolsMsg, err := readOne()
	if err != nil {
		return fmt.Errorf("tools/list failed: %w", err)
	}
	names := toolNames(toolsMsg)
	c.Log.Printf("tools exposed: %s", strings.Join(names, ", "))
	if len(names) == 0 {
		return fmt.Errorf("the server exposes no tool")
	}

	// Every probe must be offered before any is made: an absent tool used to be
	// skipped, and a skipped probe passes. See missingProbes.
	if missing := missingProbes(names); len(missing) > 0 {
		return fmt.Errorf("the server does not expose %s — the smoke cannot probe what is not there, and "+
			"all of them are registered unconditionally by internal/mcp", strings.Join(missing, ", "))
	}

	// The probes and the verdict on each answer live in smoke_gate.go:
	// judgeToolAnswer refuses an isError result and an empty one, which the
	// `m["error"]` check this replaces let through as "answered".
	id := 3
	for _, q := range smokeProbes {
		if err := send(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": q.tool, "arguments": map[string]any{q.arg: q.val}},
		}); err != nil {
			return err
		}
		m, err := readOne()
		if err != nil {
			return fmt.Errorf("%s failed: %w", q.tool, err)
		}
		_, count, err := judgeToolAnswer(q.tool, m, true)
		if err != nil {
			return err
		}
		c.Log.Printf("%s(%s=%q) answered with %d result(s)", q.tool, q.arg, q.val, count)
		id++
	}

	// ASK THE SERVER WHAT IT CAN DO, rather than only checking that it did not
	// complain.
	//
	// The two stderr checks below are NEGATIVE assertions — "this string is
	// absent" — and one of them is weaker than it reads. "semantic disabled" is
	// printed only on the --allow-lexical-fallback path, and the coherence guard
	// that reaches it is itself behind `if emb.Enabled()`; this step runs
	// .local/bin/server.exe, the LEXICAL build, whose noop embedder reports false.
	// So on the binary this step actually drives, that assertion cannot fail, and
	// the step's own log line claimed vector search was proven.
	//
	// server_info is a positive, falsifiable answer about the corpus that does not
	// depend on which build asked: fts and hnsw are properties of the DB, and
	// internal/mcp recomputes them per store. What is deliberately NOT asserted
	// here is `semantic`, because that IS build- and environment-dependent (it
	// needs embed_ffi, EMBED_MODEL_DIR and ORT), and a gate that fails on a
	// correct corpus for want of an env var is worse than no gate — it teaches the
	// operator to skip it. `.local/resume/prove.sh` drives server-full.exe with
	// that environment set and asserts `semantic` there.
	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "server_info", "arguments": map[string]any{}},
	}); err != nil {
		return err
	}
	info, err := readOne()
	if err != nil {
		return fmt.Errorf("server_info failed: %w", err)
	}
	// A server_info that FAILED would otherwise be reported below as `"fts":true
	// 0 time(s)` — a true statement about the wrong thing. Say what the tool
	// said. It carries no count, so nothing more is asked of it here.
	if _, _, err := judgeToolAnswer("server_info", info, false); err != nil {
		return err
	}
	// server_info nests the payload as a JSON STRING inside an MCP content
	// block, so the keys arrive with their quotes: `"fts":true`, not `fts:true`.
	body := strings.ReplaceAll(fmt.Sprintf("%v", info), " ", "")
	// AND COUNT THEM. The ETSI half reports its own `"fts"`/`"hnsw"` in the same
	// payload, so a bare Contains would be satisfied by either half alone — a
	// 3GPP corpus serving without its index would pass on the strength of the
	// ETSI one. When both halves are attached, both must say true.
	wantEach := 1
	if etsiAttached {
		wantEach = 2
	}
	for _, arm := range []string{`"fts":true`, `"hnsw":true`} {
		if n := strings.Count(body, arm); n < wantEach {
			return fmt.Errorf("server_info reports %s %d time(s), want %d (halves attached: %d) — "+
				"a corpus carries the index and the server will not use it:\n%s",
				arm, n, wantEach, wantEach, tailString(body, 4))
		}
	}
	c.Log.Printf("server_info: fts and hnsw live on %d served corpus half/halves", wantEach)

	// THE assertion. The guard prints this exact prefix when it disables vector
	// search, and a corpus with vectors that is served lexically is a failed goal.
	errs := stderr.String()
	// Federation degrades silently by design (a bad etsi.duckdb leaves a 3GPP-only
	// server running), which is precisely how a corpus stops being served without
	// anyone noticing. If we asked for it, it has to be there.
	if etsiAttached && !strings.Contains(errs, "ETSI corpus attached") {
		return fmt.Errorf("--etsi-db was passed and the server did NOT attach the ETSI corpus — it degraded to 3GPP-only:\n%s", tailString(errs, 20))
	}
	if strings.Contains(errs, "semantic disabled") {
		return fmt.Errorf("the server DISABLED vector search at startup — the embed identity does not match the corpus stamp:\n%s", tailString(errs, 20))
	}
	c.Checkpoint("tools", strconv.Itoa(len(names)))
	c.Log.Printf("the server did not disable vector search at startup (see prove.sh for the semantic proof)")

	// THIS is the moment compact's own instruction points at: "once served and
	// verified, remove <db>.pre-compact". Until now nothing did, and the
	// consequence was not merely wasted disk — compact REFUSES to overwrite an
	// existing .pre-compact, so the backup left by one build blocked the next
	// one. On 2026-09-03 the ETSI half compacted and verified (14.7 GiB, 3 169 614
	// clauses) and then failed on the in-place swap because of a backup from the
	// 2nd. A step that cannot run twice without someone deleting a file by hand is
	// a step the pipeline cannot converge through.
	//
	// Removing it HERE, and nowhere earlier, is the point: the smoke has just
	// started the shipped binary against this corpus and had it answer. Before
	// that the backup is the only way back.
	releasePreCompact(c)
	return nil
}

// rotateCompactionArtefacts clears what a previous compaction left behind, so
// that this one can run at all. It keeps ONE generation of backup, not two.
//
// compact leaves two kinds of file and refuses to overwrite either:
//
//	<db>.compact      the copy. A completed run renames it into place, so a
//	                  leftover means the previous attempt died between the copy
//	                  and the swap.
//	<db>.pre-compact  the backup. It exists only BECAUSE a swap completed — that
//	                  is the step that creates it.
//
// Both refusals are right about the FILE and wrong about the RUN: they leave the
// pipeline stuck on a corpus it can never finish compacting, and the only way out
// is someone deleting a file by hand. That happened three times on 2026-09-03.
//
// The release was first attached to `smoke`, on the reasoning that a backup
// should outlive the corpus until the shipped binary has served it. That reasoning
// is sound and the placement was not: compact fails BEFORE smoke can run, so the
// release sat behind the very step it had to unblock. A deadlock, and mine.
//
// Rotating here is safe in exactly the way the refusals intend. The live corpus is
// untouched — compact writes only the copy until it verifies it — and a
// .pre-compact present at this point backs up a corpus the live one has already
// superseded. There is no instant at which nothing good exists.
func rotateCompactionArtefacts(c *Ctx, t corpusTarget) {
	for _, suffix := range []string{".compact", ".pre-compact"} {
		p := c.dataPath(t.DB + suffix)
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if err := os.Remove(p); err != nil {
			c.Log.Printf("WARNING: %s is in the way and could not be removed: %v", p, err)
			continue
		}
		c.Log.Printf("rotated %s (%.1f GiB) — superseded by the live corpus",
			p, float64(st.Size())/(1<<30))
	}
}

// releasePreCompact drops the pre-compaction backups now that the compacted
// corpora have been proven to serve.
//
// Failures are logged, never fatal: a backup that could not be deleted is a disk
// problem, not a reason to fail a smoke that passed.
func releasePreCompact(c *Ctx) {
	for _, name := range []string{"3gpp.duckdb", "etsi.duckdb"} {
		p := c.dataPath(name + ".pre-compact")
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if err := os.Remove(p); err != nil {
			c.Log.Printf("WARNING: could not remove %s (%.1f GiB): %v", p, float64(st.Size())/(1<<30), err)
			continue
		}
		c.Log.Printf("released %s — %.1f GiB reclaimed, the compacted corpus has served", p, float64(st.Size())/(1<<30))
	}
}

func toolNames(m map[string]any) []string {
	res, _ := m["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	var out []string
	for _, t := range list {
		if tm, ok := t.(map[string]any); ok {
			if n, ok := tm["name"].(string); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// serverPostmortem turns an EOF on stdout into a statement about the server.
//
// The 2026-08-26 03:50 failure read "initialize failed: EOF (server stderr: )" —
// an empty stderr, which says nothing at all. Two causes, both fixed here: the
// step never waited for the process, so it could not report an exit code; and it
// read the builder while os/exec's copier was still filling it, so whatever the
// server did print could be missing. Waiting first makes stderr complete AND
// removes the race.
//
// The doctrine this serves: never let a guard confound "I cannot measure" with
// "the condition is violated".
func serverPostmortem(cmd *exec.Cmd, stderr fmt.Stringer) string {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		how := "the server exited with status 0"
		if err != nil {
			how = "the server died: " + err.Error()
		}
		if e := strings.TrimSpace(stderr.String()); e != "" {
			return how + "; stderr:\n" + tailString(e, 12)
		}
		return how + " and wrote nothing to stderr"
	case <-time.After(10 * time.Second):
		return "the server is still alive but closed its stdout"
	}
}

func tailString(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// validateAnchor turns the runbook's "check the anchor" instruction into an
// invariant the pipeline enforces.
//
// A rule that lives only in documentation is a rule somebody will skip. The
// specific rule here — the anchor must not claim text the corpus does not hold —
// went unchecked long enough to accumulate 56 violations, every one of them
// invisible to `cmd/validate`, which asks whether the clauses that EXIST are
// complete and never whether the ones the anchor promises exist at all.
//
// Failing is the point, and it is actionable: `goal run --repair` folds these
// keys into the fetch plan. The keys that genuinely cannot be acquired — the
// status report does not list them, so there is no URL — belong in
// contracts/accepted-absences.txt with a reason, which is a decision on the
// record rather than a number that stopped mattering.
func validateAnchor(c *Ctx) error {
	idx := filepath.Join(c.Local, "corpus-index.json")
	if !fileNonEmpty(idx) {
		c.Log.Printf("no delta anchor to verify")
		return nil
	}
	out, err := c.Output(Cmd{Name: c.bin("anchorcheck"), Args: []string{
		"--db", c.dataPath("3gpp.duckdb"),
		"--index", idx,
		"--accept", filepath.Join(c.Root, "contracts", "accepted-absences.txt"),
		"--quiet",
	}})
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			c.Log.Printf("anchor: %s", line)
		}
	}
	if err != nil {
		// A TOOL THAT NEVER RAN HAS NOT ACCUSED THE CORPUS OF ANYTHING.
		//
		// This used to wrap every anchorcheck failure in the same sentence, and the
		// sentence names a data defect and prescribes `goal run --repair` — a full
		// repair fetch, hours of downloads. Measured 2026-09-08 10:26: anchorcheck
		// died with 0xC0000142 (STATUS_DLL_INIT_FAILED, a process that could not
		// start at all — this machine produced four of them in one morning under
		// commit pressure from the heavy DuckDB steps), and the build reported that
		// the corpus was missing spec text. It was not: the contract had just passed
		// all seven of its checks against that very corpus, seconds earlier.
		//
		// anchorcheck prints one line per violation, so a non-zero exit with NOTHING
		// on stdout is a tool that did not get as far as having an opinion. Saying so
		// is the whole fix: the run still fails — an unrun gate proves nothing — but
		// it fails naming what actually happened, and it does not send anyone to
		// re-download the archive over a corpus that is intact.
		if strings.TrimSpace(out) == "" {
			return fmt.Errorf("anchorcheck did not run — it exited without reporting a single "+
				"anchor line, so this says nothing about the corpus. Re-run the step; if it "+
				"persists, launch it by hand to see why the binary will not start: %w", err)
		}
		return fmt.Errorf("the delta anchor claims specs the corpus holds no text for; "+
			"run `goal run --repair` to fetch them, or record the unfetchable ones in "+
			"contracts/accepted-absences.txt: %w", err)
	}
	return nil
}

// corpusTarget is the per-corpus parameterisation of embed + index.
//
// 3GPP and ETSI get the SAME treatment — vectors and an HNSW index, not just the
// lexical FTS the ingest already builds. Anything less makes one of them a
// second-class corpus: a natural-language question would reach 3GPP clauses by
// vector and ETSI clauses only when the wording happens to match literally. For
// lawful interception, where 3GPP's 33.127/33.128 PROFILE ETSI's 103 221-1 and
// 103 280, that asymmetry is the difference between an answer and half an answer.
type corpusTarget struct {
	// Suffix is appended to the step names ("" for 3GPP, "-etsi").
	Suffix string
	// DB is the file name under data/.
	DB string
	// Ledger is the file name under .local/vecs/.
	//
	// SEPARATE PER CORPUS, and this is not a preference. The ledger keys resume on
	// chunk_id, and chunk_id is only unique WITHIN one database — both corpora
	// number their clauses from ~0. Sharing one ledger would make the second
	// corpus's clause 42 look already-embedded because the first corpus wrote a
	// clause 42, and the vector would be silently skipped. That is the same
	// collision that forced merge-before-embed on the 3GPP side.
	Ledger string
	// Floor returns the release floor to pass to embed-io. EMPTY means no floor.
	//
	// ETSI must pass empty. `clauses_needing_embedding` skips any clause whose
	// release has no ordinal when a floor is set, and `release_ordinal("ETSI")` is
	// None — so any non-empty floor would silently select ZERO ETSI clauses and the
	// step would report success over an unvectorised corpus.
	Floor func(c *Ctx) string
	// ContractKey names the Ctx config entry holding this corpus's
	// data-completeness contract flags, as scripts/data-contract.sh emits them
	// for this arm. cmd/goal asks the script once per arm and puts both answers
	// in the config, so `validate` and `validate-etsi` are held to one contract
	// expressed in one file rather than to two that drift.
	ContractKey string
	// Snapshot names the published GHCR package this arm bootstraps from.
	//
	// BOTH ARMS HAVE ONE, and for two months only one of them used it.
	// bootstrap.CorpusETSI was written, exported, covered by its own test and
	// called by cmd/server — and by NO pipeline step, because `seed` named
	// bootstrap.Corpus3GPP directly instead of asking the arm. So a fresh clone
	// pulled the 3GPP corpus from a snapshot in minutes and rebuilt the ETSI half
	// from etsi.org over many hours of download and GPU, for a package that was
	// sitting on the registry the whole time. Same shape as ingest-glossary: the
	// capability existed, the wiring did not.
	Snapshot func() bootstrap.CorpusSource
	// Producer names the step that writes DB (for AnyDeps / Deps).
	Producers []string
}

func corpus3GPP() corpusTarget {
	return corpusTarget{
		Suffix:      "",
		DB:          "3gpp.duckdb",
		Ledger:      "ledger.jsonl",
		Floor:       func(c *Ctx) string { return c.Cfg("embed_floor") },
		ContractKey: "contract_flags",
		Snapshot: func() bootstrap.CorpusSource {
			return bootstrap.Corpus3GPP(os.Getenv("MCP3GPP_GHCR_OWNER"), os.Getenv("MCP3GPP_CORPUS_TAG"))
		},
		Producers: []string{"merge", "seed"},
	}
}

func corpusETSI() corpusTarget {
	return corpusTarget{
		Suffix:      "-etsi",
		DB:          "etsi.duckdb",
		Ledger:      "etsi-ledger.jsonl",
		Floor:       func(c *Ctx) string { return "" },
		ContractKey: "contract_flags_etsi",
		Snapshot: func() bootstrap.CorpusSource {
			return bootstrap.CorpusETSI(os.Getenv("MCP3GPP_GHCR_OWNER"), os.Getenv("MCP3GPP_CORPUS_TAG"))
		},
		// seed-etsi joins ingest-etsi exactly as seed joins merge on the other arm:
		// two producers, so they land in AnyDeps and either one moving is enough.
		Producers: []string{"ingest-etsi", "seed-etsi"},
	}
}

func (t corpusTarget) dbPath(c *Ctx) string     { return c.dataPath(t.DB) }
func (t corpusTarget) ledgerPath(c *Ctx) string { return filepath.Join(c.Local, "vecs", t.Ledger) }

// singleProducer / multiProducer split the producer list between Deps and AnyDeps.
//
// AnyDeps deliberately rejects a one-element set — one alternative is a Dep in
// disguise and would lose the ordinary dependency semantics. 3GPP has two
// producers (merge OR seed) and belongs in AnyDeps; ETSI has one and belongs in
// Deps. Encoding that here keeps the rule in one place instead of at each call.
func (t corpusTarget) singleProducer() []string {
	if len(t.Producers) == 1 {
		return t.Producers
	}
	return nil
}

func (t corpusTarget) multiProducer() []string {
	if len(t.Producers) > 1 {
		return t.Producers
	}
	return nil
}

// indexDeps: the vector index needs the vectors, and for 3GPP it also waits on
// `enrich` -- the catalogue overlay rewrites rows, and rebuilding the index before
// it would index a corpus that is about to change.
//
// The ETSI arm names its own enrich TRANSITIVELY, through paragraphs-etsi: that
// step depends on enrich-etsi (see paragraphsDeps), and enrich-etsi writes only
// `acronyms`, a table no vector index reads. Listing it again here would be a
// dependency that documents nothing the DAG does not already enforce.
//
// BOTH ARMS NAME THEIR OWN COMPACTION. `COPY FROM DATABASE` does not carry custom
// indexes and the bin therefore resets hnsw_state to "building", so an index
// frozen BEFORE its corpus is compacted is thrown away by the compaction while
// schema_meta still claims the graph is frozen. That constraint is per corpus, and
// it used to be expressed by both arms naming the SAME `compact` step -- which
// worked, and coupled the ETSI freeze to the 3GPP conversion for no reason.
func (t corpusTarget) indexDeps() []string {
	deps := []string{"embed" + t.Suffix, "paragraphs" + t.Suffix, "compact" + t.Suffix, "build-go"}
	if t.Suffix == "" {
		// The 3GPP index is built AFTER the corpus is content-addressed: the
		// vectors move to `bodies` in that step, and an index built before it
		// would index the table the step is about to drop.
		// build-go because the index is now built by cmd/freeze-hnsw.
		deps = append(deps, "enrich")
	}
	return deps
}

// compactDeps: after every writer that leaves dead blocks behind in THIS corpus.
//
// THE 3GPP ARM WAS MISSING `sparse`, AND THE ETSI ARM WAS NOT. The single shared
// step declared paragraphs, paragraphs-etsi and sparse-etsi -- so the ETSI sparse
// import made the compaction dirty and the 3GPP one, which writes the same kind of
// rows into the same kind of table, did not. Nothing detected it because the step
// rewrote both files whenever it ran for any reason. Splitting the step by corpus
// makes each arm's determinants exactly the writers of its own corpus, and the
// omission cannot be spelled any more.
func (t corpusTarget) compactDeps() []string {
	return []string{"build-rust", "build-go", "paragraphs" + t.Suffix, "sparse" + t.Suffix}
}

// validateDeps: the gate comes after the last thing it inspects.
//
// The 3GPP arm inspects BOTH corpora, because its contract carries --require-etsi
// -- the one check about the pair -- so it names the ETSI freeze as well as its
// own. The ETSI arm inspects one corpus and names one freeze. See stepValidate for
// the build that measured the missing edge.
func (t corpusTarget) validateDeps() []string {
	if t.Suffix == "" {
		return []string{"index", "index-etsi"}
	}
	return []string{"index" + t.Suffix}
}

// refreshOverlays reports whether the operator asked for the external overlays
// to be re-acquired even though they are already on disk.
//
// The default is NOT to: the 5GC OpenAPI fetch pulls one archive per release to
// discover that nothing moved, and an enrich that goes to the network every run
// is an enrich that fails when the network does. Refreshing is what you do when
// a new release lands, and saying so is cheap.
func refreshOverlays() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OVERLAY_REFRESH"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// --------------------------------------------------------------- paragraphs

// stepParagraphs converts the merged corpus to the content-addressed storage of
// ADR 0004 and drops the table it replaces.
//
// It sits between enrich and index, and that position is the whole design. The
// write side still produces `clauses` with its text and its vectors; running the
// conversion AFTER embed and enrich means the vectors are simply carried across
// to the bodies that own them, so neither embed nor the Rust write side has to
// change. `index` then builds the HNSW where the vectors now live.
//
// Without this step a fresh clone would run all nineteen steps and rebuild the
// OLD shape: the conversion existed only as a tool someone had to remember to
// run, which is the same failure as the overlays printing the name of a script
// instead of running it.
func stepParagraphs(t corpusTarget) *Step {
	return &Step{
		Name:    "paragraphs" + t.Suffix,
		Version: 2,
		Doc:     "store each paragraph once and point at it (ADR 0004), then drop the clauses table",
		Deps:    t.paragraphsDeps(),
		Impl:    []string{"cmd/migrate-paragraphs"},
		Heavy:   true,
		// The corpus is NOT an input, although this step reads and rewrites it.
		//
		// It is the step's own product, and compact, index and the paragraph
		// conversion all rewrite it further down the same build. A step that folds
		// it into its fingerprint therefore never sees a stable input: every build
		// changes the corpus after the step recorded it, so the next build replays
		// the step. Same shape as the one that made `merge` replay a 22 GB restore
		// on every run — measured here on 2026-09-03, where a fully VALID pipeline
		// still planned "2 certain to run (2 heavy)" on a corpus nothing had
		// touched.
		//
		// What determines this step's work is declared elsewhere and precisely: its
		// DATA dependency (merge or seed, through provenance), the embed identity
		// and floor in Extra, and — at run time — the corpus's own answer to "does
		// any clause still need one of these". That last question is the honest one,
		// and the step already asks it before declining.
		Inputs:  func(c *Ctx) ([]string, error) { return nil, nil },
		Outputs: func(c *Ctx) []string { return []string{t.dbPath(c)} },
		Validate: func(c *Ctx) error {
			// --attested, NOT --verify. The full rebuild reads the whole corpus:
			// ~8 min on 23 GB, and the step is instantiated for both halves, so it
			// was ~15 min of tax on every plan and every build — including plans
			// where this step was going to be SKIPPED.
			//
			// That is the trade validatePublished refuses a few files away, in this
			// repository's own words: "Validate runs on EVERY plan, including for
			// steps that would be skipped … What the record claims is confirmed at
			// the moment it is written instead." The rebuild now runs where the
			// claim is made, and leaves the counters it covered in the corpus;
			// this re-counts them, which DuckDB answers from metadata.
			//
			// A corpus that was never attested fails here on purpose: the step then
			// RUNS, proves itself once, and every later plan is cheap.
			out, err := c.Output(Cmd{Name: c.bin("migrate-paragraphs"), Args: []string{
				"--db", t.dbPath(c), "--attested",
			}})
			if err != nil {
				return stillOpenElsewhere(t.DB, fmt.Errorf("the converted corpus does not verify: %w", err))
			}
			if !strings.Contains(out, "clause_occ=") {
				return fmt.Errorf("verification produced no counters: %q", out)
			}
			return nil
		},
		Run: func(c *Ctx) error {
			db := t.dbPath(c)
			c.Log.Printf("converting %s to content-addressed storage (paragraphs, bodies, occurrences)", t.DB)
			// --drop-clauses is safe to pass unconditionally, but NOT for the reason
			// this comment used to give. It claimed the build was "a no-op
			// re-derivation" on an already-converted corpus. It was the opposite: the
			// rebuild reads `clauses`, which on such a corpus is the VIEW over the
			// very tables it replaces, so re-deriving clause_occ joined freshly
			// renumbered paragraphs against the old body_ids and cut 2 752 688
			// occurrences down to 140 047 — with verify passing, because it compares
			// the rebuild against the same broken view. Measured 2026-08-29.
			// cmd/migrate-paragraphs now DECLINES that input up front
			// (alreadyConverted), so the step really is the no-op it is described as.
			return c.Run(Cmd{Name: c.bin("migrate-paragraphs"), Args: []string{
				"--db", db, "--drop-clauses",
			}, Echo: true})
		},
	}
}

// ------------------------------------------------------------ sparse arm

// stepBuildSparse builds embed-core-sparse, the bulk learned-lexical producer.
//
// SEPARATE FROM build-rust because it is a different crate with different
// features: rust/embed-core is EXCLUDED from the rust/ workspace and only grows a
// sparse head when built `--features ort` (plus `cuda` for the GPU box). build-rust
// compiles the workspace with neither, so it can never produce this binary.
func stepBuildSparse() *Step {
	return &Step{
		Name:      "build-sparse",
		Version:   1,
		Doc:       "build the learned-lexical (sparse) corpus producer",
		Deps:      []string{"toolchain"},
		Impl:      []string{"rust/embed-core/src"},
		Toolchain: true,
		Tool:      true,
		// A box without the sparse model still completes every other step: the
		// sparse arm is additive, and refusing to build without it would make the
		// whole pipeline hostage to one optional artefact.
		Optional: true,
		Outputs:  func(c *Ctx) []string { return []string{c.rbin("embed-core-sparse")} },
		Run: func(c *Ctx) error {
			target := filepath.Join(c.Local, "cargo-target-sparse")
			feats := "ort"
			if _, err := os.Stat(filepath.Join(c.Root, ".local", "toolchain", "cuda", "dll")); err == nil {
				feats = "ort,cuda"
			}
			c.Log.Printf("cargo build embed-core-sparse (--features %s)", feats)
			if err := c.Run(Cmd{
				Name: "cargo",
				Args: []string{"build", "--release",
					"--manifest-path", "rust/embed-core/Cargo.toml",
					"--features", feats, "--bin", "embed-core-sparse"},
				Env:  append([]string{"CARGO_TARGET_DIR=" + target}, gpuEnv(c)...),
				Echo: true,
			}); err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(target, "release", exe("embed-core-sparse")))
			if err != nil {
				return fmt.Errorf("cargo reported success but embed-core-sparse is missing: %w", err)
			}
			if err := WriteAtomic(c.rbin("embed-core-sparse"), b); err != nil {
				return err
			}
			if err := os.Chmod(c.rbin("embed-core-sparse"), 0o755); err != nil {
				return err
			}
			return stageRuntimeDLLs(c)
		},
	}
}

// stepSparse fills clause_sparse with the BGE-M3 learned-lexical postings.
//
// ADDITIVE, and that is the whole reason it is safe to run late: it never touches
// the dense vectors or the HNSW index, it only writes rows nothing else writes.
// A corpus without it answers exactly as before, one arm short — which is the
// state this repository shipped in for months while every piece of the machinery
// except the model file was already written.
//
// Resumable on the same terms as `embed`: the postings file is append-only and
// embed-core-sparse skips chunk_ids already in it, so a kill costs the current
// batch and nothing else.
// stepSparse builds the learned-lexical arm for one corpus.
//
// It is parameterised for the same reason stepEmbed and stepIndex are: BOTH
// corpora are served, side by side, and an arm that exists on one of them only
// is an asymmetry a caller cannot see. search.Engine federates the two stores and
// simply drops the arm the ETSI one does not have, with no error — so a query
// that the sparse arm would have answered well on 3GPP falls back to dense+BM25
// the moment it lands on an ETSI deliverable.
//
// Nothing in the machinery was 3GPP-specific; only the paths were.
func stepSparse(t corpusTarget) *Step {
	return &Step{
		Name:    "sparse" + t.Suffix,
		Version: 2,
		Doc:     "vectorise the learned-lexical (sparse) arm of " + t.DB + " on the GPU",
		Deps:    t.sparseDeps(),
		// rust/store/src/vectors.rs carries set_sparse_many, sparse_chunk_ids and
		// the term_id index handling — the import this step's whole cost lives in.
		// It was in lib.rs, which this step never declared.
		Impl: []string{"rust/embed-core/src", "rust/store/src/bin/embed_io.rs",
			"rust/store/src/vectors.rs"},
		// The corpus is not an input here either: compact rewrites it after this
		// step, so fingerprinting it guarantees a replay on the next build. The
		// sparse identity in Extra and the data dependency are what actually decide
		// this step's work, and it asks the corpus directly — "does every clause
		// already carry a posting" — before declining.
		Inputs: func(c *Ctx) ([]string, error) { return nil, nil },
		Extra: func(c *Ctx) (map[string]string, error) {
			return map[string]string{"sparse_identity": sparseIdentityForPlan(c)}, nil
		},
		Heavy:    true,
		Optional: true,
		Run:      func(c *Ctx) error { return runSparse(c, t) },
	}
}

// paragraphsDeps: the conversion runs AFTER the vectors exist, so they are simply
// carried across to the bodies that own them and neither embed nor the Rust write
// side has to change. BOTH arms also wait on their own `enrich`.
//
// The ETSI arm did not, and the comment here explained why: "ETSI has no such
// overlay (DynaReport describes 3GPP specs, not ETSI deliverables)". That was
// true of the CATALOGUE and got generalised to enrichment as a whole — while
// ingest-glossary sat in the tree, built and unrun, precisely because there was
// no ETSI enrich step to run it. The claim was about one overlay; the dependency
// it justified removed all of them.
func (t corpusTarget) paragraphsDeps() []string {
	return []string{"embed" + t.Suffix, "enrich" + t.Suffix, "build-go"}
}

// sparseDeps mirrors indexDeps: the work list is exported from the shape the
// content-addressed conversion produces, so BOTH arms wait for their own.
//
// ETSI used to wait on its embed instead, because it had no conversion. That was
// only ever true while the ETSI half held ONE version per deliverable. With every
// published version in it, an unconverted ETSI corpus takes the branch in
// Store.SearchClauses that ranks VERSIONS rather than clauses — the twelve-hit
// window that was one clause repeated across twelve releases, with the spec that
// answers the question never in it. The conversion is what makes searchClausesCA
// the path taken, so it is a dependency, not a nicety.
func (t corpusTarget) sparseDeps() []string {
	return []string{"build-sparse", "build-rust", "paragraphs" + t.Suffix}
}

// sparseFiles are the work list and postings ledger for a corpus. The 3GPP pair
// keeps its historical names verbatim: `.local/vecs/sparse.jsonl` is append-only
// and a campaign in flight resumes from it, so renaming it would silently restart
// hours of GPU.
func (t corpusTarget) sparseFiles(c *Ctx) (work, out string) {
	base := filepath.Join(c.Local, "vecs", "sparse"+t.Suffix)
	return base + ".worklist", base + ".jsonl"
}

// sparseIdentityForPlan asks cmd/embedid for the SPARSE identity, the digest
// `embed-io --import-sparse` stamps into schema_meta.sparse_model. Planning must
// not fail when the model is absent (embedid returns empty), so an unresolvable
// identity reads "unresolved" and keeps the step dirty — the fail-safe direction,
// same contract as embedIdentityForPlan.
func sparseIdentityForPlan(c *Ctx) string {
	out, err := c.Output(Cmd{
		Name: c.bin("embedid"),
		Args: []string{"--sparse"},
		Env:  []string{"EMBED_MODEL=" + sparseModelName},
	})
	if err != nil {
		return "unresolved"
	}
	if id := strings.TrimSpace(out); id != "" {
		return id
	}
	return "unresolved"
}

// sparseModelName is the registry entry that carries `sparse_output` — the one
// model whose ONNX exposes the learned-lexical head.
const sparseModelName = "bge-m3-sparse"

func runSparse(c *Ctx, t corpusTarget) error {
	db := t.dbPath(c)
	modelDir := c.dataPath("models", sparseModelName)
	if _, err := os.Stat(filepath.Join(modelDir, "model.onnx")); err != nil {
		// DECLINE, not fail. BAAI publishes no sparse ONNX; it is produced by
		// scripts/export-bge-m3-sparse.py on a box with torch. Until it exists the
		// arm simply is not there, and that must not fail a corpus that is
		// otherwise complete.
		return fmt.Errorf("%w: no sparse model at %s (run WITH_SPARSE=1 scripts/fetch-model.sh)",
			ErrDeclined, modelDir)
	}
	id := sparseIdentityForPlan(c)
	if id == "unresolved" {
		return fmt.Errorf("cmd/embedid --sparse resolved no identity for %s", sparseModelName)
	}
	c.Log.Printf("sparse identity: %s", id)

	if err := os.MkdirAll(filepath.Join(c.Local, "vecs"), 0o755); err != nil {
		return err
	}
	work, out := t.sparseFiles(c)

	c.Log.Printf("exporting the sparse work list of %s (floor=%q)", t.DB, t.Floor(c))
	if err := c.Run(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", db, "--export-sparse-worklist", work, "--embed-floor", t.Floor(c),
	}}); err != nil {
		return err
	}
	todo := countLines(work)
	c.Checkpoint("sparse_worklist", strconv.Itoa(todo))
	if todo == 0 {
		return fmt.Errorf("%w: every clause already carries a sparse posting", ErrDeclined)
	}
	// SAME HAZARD AS THE DENSE LEDGER, and worse until now: a posting line carried a
	// chunk_id and nothing else. chunk_ids are positional, so a rebuilt corpus reuses
	// them for different clauses and the sparse arm would retrieve the wrong ones,
	// confidently. Unlike the dense ledger there is nothing to salvage — the postings
	// carry no reusable per-text cache — so the archive is kept only as evidence.
	if fileNonEmpty(out) {
		if moved, bad, n, hashed := ledgerDescribesAnotherBuild(out, work, sparseResumeHash); moved {
			archive := fmt.Sprintf("%s.%s.otherbuild.bak", out, time.Now().UTC().Format("20060102T150405Z"))
			why := fmt.Sprintf("%d of %d sampled chunk_ids hash differently", bad, n)
			if hashed == 0 {
				why = "it carries no content hash at all, so nothing in it can be verified"
			}
			c.Log.Printf("the sparse postings file describes ANOTHER build of this corpus "+
				"(%s) — archiving it to %s", why, filepath.Base(archive))
			if err := os.Rename(out, archive); err != nil {
				return err
			}
		}
	}
	c.Log.Printf("%d clause(s) to embed (postings file already holds %d)", todo, countLines(out))

	if err := c.Run(Cmd{Name: c.rbin("embed-core-sparse"), Args: []string{
		"--in", work, "--out", out, "--batch", envOr("SPARSE_BATCH", "256"),
	}, Env: append([]string{"EMBED_MODEL_DIR=" + modelDir}, gpuEnv(c)...), Echo: true}); err != nil {
		c.Checkpoint("sparse_postings", strconv.Itoa(countLines(out)))
		return err
	}
	c.Checkpoint("sparse_postings", strconv.Itoa(countLines(out)))

	// --import-sparse-changed-only, for the same reason the dense arm imports the
	// changed rows only. Build 23 (2026-09-06, ETSI half): 368 clauses needed
	// postings and took 9 seconds on the GPU, then this import rewrote all
	// 2 000 182 of them and took 49 MINUTES. The cost tracked the ledger, not the
	// work.
	//
	// What it gives up is the self-repairing rewrite of every posting, and that is
	// still reachable — `embed-io --import-sparse` without the flag — for the case
	// it exists to answer: postings damaged under a chunk_id that is still present,
	// which no work list can see.
	c.Log.Printf("importing the postings into clause_sparse (stamping %s)", id)
	return c.Run(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", db, "--import-sparse", out, "--sparse-model", id,
		"--import-sparse-changed-only",
	}, Echo: true})
}

// ----------------------------------------------------------- compaction

// A rewrite has to earn its thirty minutes, and there are two ways it can: the
// dead space is a meaningful SHARE of the file, or it is a large enough
// ABSOLUTE number of bytes that everyone who pulls the image downloads them.
// Either alone is sufficient.
//
// Neither threshold is finely tuned, because the cases they separate are four
// orders of magnitude apart. A finished corpus, measured 2026-09-04: 2 free
// blocks of 87 830 — 0.002 %, 512 KiB. One that genuinely needed compacting,
// measured 2026-08-30: 182 219 free of 229 166 — 79.5 %, 43.6 GB.
const (
	compactMinDeadFraction = 0.05
	compactMinDeadBytes    = 1 << 30 // 1 GiB
)

// nothingToReclaim reads a `dbcount --blocks` transcript and reports whether the
// corpus it describes holds too little dead space to be worth rewriting, together
// with the measurement — so the log says what was found, not merely that nothing
// happened.
//
// IT FAILS OPEN, and the asymmetry is the point. A transcript this cannot parse —
// a bin predating --blocks, a reworded line, a truncated pipe — is not evidence
// that there is nothing to reclaim, it is the absence of evidence. Declining on
// it would carry the previous provenance forward, skip `index` behind it, and
// publish a corpus full of dead space with every gate green. Running a compaction
// that turns out to have been unnecessary costs half an hour and nothing else.
// That is the same reasoning as ingestedNothing's "no tally at all is not proof
// of nothing".
func nothingToReclaim(out string) (bool, string) {
	free, okFree := kvInt(out, "free_blocks")
	total, okTotal := kvInt(out, "total_blocks")
	size, okSize := kvInt(out, "block_size")
	if !okFree || !okTotal || !okSize || total <= 0 || size <= 0 || free < 0 {
		return false, ""
	}
	dead := free * size
	share := float64(free) / float64(total)
	if share >= compactMinDeadFraction || dead >= compactMinDeadBytes {
		return false, ""
	}
	return true, fmt.Sprintf(
		"%d of %d block(s) free — %.1f MiB (%.3f%%) is all a rewrite would give back",
		free, total, float64(dead)/(1<<20), 100*share)
}

// kvInt pulls one KEY=VALUE integer out of a tool's transcript. Absent, or
// present and unparseable, are the same answer to the caller: not known.
func kvInt(out, key string) (int64, bool) {
	for _, field := range strings.Fields(out) {
		v, ok := strings.CutPrefix(field, key+"=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

// stepCompact rewrites ONE corpus without its dead space, BEFORE its index.
//
// IT IS PER CORPUS NOW. There used to be a single `compact` that looped over both
// files, and the loop was not the problem -- the DECLARATION was. It named
// `paragraphs`, `paragraphs-etsi` and `sparse-etsi`, so the ETSI sparse import made
// the compaction dirty and the 3GPP sparse import, which writes the same kind of
// rows into the same kind of table, did not. Nothing ever detected the omission,
// because the step rewrote both files whenever it ran for any reason at all. Split
// by corpus, each arm's determinants are exactly the writers of its own corpus
// (see compactDeps) and the gap cannot be spelled.
//
// It also uncouples the two freezes. `index-etsi` used to wait on a step that
// depended on the 3GPP paragraph conversion, so a 3GPP-only change replayed the
// ETSI compaction and the ETSI freeze behind it -- half an hour and a rebuilt HNSW
// for a file nothing had touched.
//
// IT DECLINES WHEN THERE IS NOTHING TO RECLAIM. `COPY FROM DATABASE` costs thirty
// minutes on this corpus and doubles its disk while it runs; it is worth exactly
// the dead space it removes, and on a finished corpus that is 512 KiB. See
// nothingToReclaim for the measurement and the thresholds. Declining is now per
// corpus as well, which is what it always meant: the shared step declined only when
// NEITHER file was worth rewriting, so a build that needed to compact one of them
// reported success over the other having been examined and skipped.
//
// DuckDB never returns free blocks to the filesystem: a CHECKPOINT reclaims them
// for reuse INSIDE the file. Measured on the 2026-08-30 corpus, 46 947 of 229 166
// blocks were in use -- 12.3 GB of data in a 55.9 GB file, and DROP TABLE plus
// CHECKPOINT moved it by zero bytes. Only a full rewrite compacts, and the result
// was 10.9 GB.
//
// ORDERING IS NOT A PREFERENCE HERE. `COPY FROM DATABASE` does not carry custom
// indexes, so this must run BEFORE `index`+suffix -- which is exactly why that step
// lists this one as a dependency rather than the other way round. Running it after
// would leave a corpus whose schema_meta says "frozen" about an index that stayed
// behind.
func stepCompact(t corpusTarget) *Step {
	return &Step{
		Name:    "compact" + t.Suffix,
		Version: 6,
		Doc:     "rewrite the corpus without its dead space (COPY FROM DATABASE)",
		// After every writer of THIS corpus: the dense import, the sparse import and
		// the content-addressed conversion all leave dead blocks behind.
		//
		// build-go is an AVAILABILITY constraint, not a provenance one: this step
		// launches dbcount, both to decide whether a rewrite is worth doing and to
		// validate the one it did. build-go is a Tool step, so declaring it orders
		// and force-builds the binary without folding anything into this
		// fingerprint. It was once launched from Validate without being declared;
		// the gate is what makes an out-of-date dbcount able to FAIL the step rather
		// than merely mis-validate it.
		Deps:  t.compactDeps(),
		Impl:  []string{"rust/store/src/bin/compact.rs"},
		Heavy: true,
		// Not even compact may fingerprint the corpus, though it looked like the
		// one step that safely could.
		//
		// It is the last step to REWRITE it, so the file it records ought to be the
		// file it is judged against -- that was the reasoning, and it was wrong by
		// one step. `index`+suffix freezes the HNSW into that same file afterwards.
		// So compact recorded a corpus without an index and was re-decided against a
		// corpus with one, and planned a 30-minute rewrite on every build for ever.
		//
		// It is the eleventh instance of one defect, found only by sweeping the DAG
		// instead of fixing the step in front of me. What decides compact's work is
		// its data dependencies -- the conversion and the sparse import of its own
		// corpus -- and those already say, through provenance, whether anything was
		// written that leaves dead space behind.
		Inputs: func(c *Ctx) ([]string, error) { return nil, nil },
		Validate: func(c *Ctx) error {
			// The copy is only believable if the corpus still answers. compact
			// itself refuses to swap on a clause-count mismatch; this re-asks
			// afterwards so a swap that somehow landed wrong cannot pass silently.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", t.dbPath(c)}})
			if err != nil {
				return stillOpenElsewhere(t.DB, err)
			}
			if !strings.Contains(out, "clauses_with_vectors=") {
				return fmt.Errorf("dbcount reports no vectorised clauses in %s after compaction", t.DB)
			}
			return nil
		},
		Run: func(c *Ctx) error {
			rotateCompactionArtefacts(c, t)
			db := t.dbPath(c)
			// A MEASUREMENT BEFORE THE REWRITE.
			//
			// This step used to rewrite the file unconditionally, on the theory that
			// whatever had just written to it left dead space behind. On a finished
			// corpus that theory is simply false, and the cost of being wrong is not
			// small: measured 2026-09-04, thirty minutes to take data/3gpp.duckdb
			// from 18.2 GiB to 18.2 GiB, with a second copy of the corpus on the
			// same volume for the duration.
			//
			// `dbcount --blocks` asks DuckDB's own block accounting instead. Both
			// corpora answered TWO free blocks -- 512 KiB of the ~21 GiB and ~18 GiB
			// they occupy -- because the writers before this step append rather than
			// delete, and the one that does delete (the paragraph conversion) is
			// followed by a compaction that already reclaimed it.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{
				"--db", db, "--blocks",
			}})
			if err != nil {
				return err
			}
			if skip, why := nothingToReclaim(out); skip {
				return fmt.Errorf("%w: %s: %s", ErrDeclined, t.DB, why)
			}
			before, _ := os.Stat(db)
			if err := c.Run(Cmd{Name: c.rbin("compact"), Args: []string{
				"--db", db,
			}, Echo: true}); err != nil {
				return err
			}
			// The original is kept as <db>.pre-compact by the bin; the pipeline does
			// not delete it. A corpus is not something to drop on the strength of a
			// copy verified seconds ago -- `validate` and `smoke` still have to run,
			// and releasePreCompact runs after them.
			if after, serr := os.Stat(db); serr == nil && before != nil {
				c.Log.Printf("%s %.2f GiB -> %.2f GiB", t.DB,
					float64(before.Size())/(1<<30), float64(after.Size())/(1<<30))
			}
			return nil
		},
	}
}

// -------------------------------------------------- serveur sémantique

// ldflagsWith puts `-L<dir>` in front of whatever CGO_LDFLAGS the caller already
// exported, instead of replacing it.
//
// Ctx.Run appends Cmd.Env to os.Environ(), and a later duplicate key wins — so a
// step that sets CGO_LDFLAGS silently discards the one the environment supplied.
// This build needs BOTH: -L<cargo target>/release for the embed-core cdylib, and
// the -L<.local/toolchain/duckdb> that scripts/local/toolchain-env.sh exports
// because the Windows build links duckdb_use_lib against a supplied libduckdb
// rather than the embedded archive. Dropping the second failed the link with
// "ld.exe: cannot find -lduckdb" every single time, which is why this step's
// recorded state was "never run": it is Optional and the finish chain calls it
// with `|| echo`, so nothing ever stopped and nobody had to look.
func ldflagsWith(dir string) string {
	own := "-L" + dir
	if prev := strings.TrimSpace(os.Getenv("CGO_LDFLAGS")); prev != "" {
		return prev + " " + own
	}
	return own
}

// stepBuildServe builds the server binary that can actually answer semantically,
// and stages the DLLs it needs beside it.
//
// WHY THIS IS A SEPARATE STEP. build-go compiles cmd/server with GOTAGS alone —
// no `onnx`, no `embed_ffi` — so the binary it produces has the noop embedder
// (internal/embed/embed_noop.go) and can only answer lexically. That is the right
// default for the portable build and the tests, and it meant `make build` shipped
// a corpus full of vectors next to a server that could not use them. `.mcp.json`
// pointed at that binary, so the MCP server advertised semantic search and served
// BM25.
//
// `onnx` alone is not enough either: it gets the reranker, not the query embedder.
// The embedder comes from the rust/embed-core cdylib built `--features ort`, which
// `embed_ffi` links through cgo — the two tags are a pair, not alternatives.
//
// The DLLs are COPIED beside the binary rather than left on a PATH. Windows
// resolves a DLL from the executable's own directory first, and an MCP server is
// launched by a client that inherits none of this shell's environment: a PATH
// export here would work in every test and fail the moment a real client started it.
func stepBuildServe() *Step {
	return &Step{
		Name:    "build-serve",
		Version: 1,
		Doc:     "build the semantic server (onnx + embed_ffi) and stage its DLLs",
		Deps:    []string{"toolchain", "build-go"},
		Impl:    []string{"cmd/server", "internal", "rust/embed-core/src", "go.mod", "go.sum"},
		// It LINKS a binary, which is the case ExcludeTests was written for and the
		// one build-go and build-rust already set. It names `internal` whole, so
		// every test under it counted — and `go build` compiles none of them. The
		// one-time replay this costs is 10.7 s (build E) and touches only
		// 60-bin.tar; the corpus layers are not this step's to move.
		ExcludeTests: true,
		Toolchain:    true,
		Tool:         true,
		// A box with no ONNX Runtime still completes every other step; it just gets
		// the lexical server. Failing the whole pipeline over the optional half of
		// the search stack would be the wrong trade.
		Optional: true,
		Outputs:  func(c *Ctx) []string { return []string{c.bin("server-full")} },
		Run: func(c *Ctx) error {
			target := filepath.Join(c.Local, "cargo-target-embedcore")
			c.Log.Printf("cargo build embed-core cdylib (--features ort)")
			if err := c.Run(Cmd{
				Name: "cargo",
				Args: []string{"build", "--release",
					"--manifest-path", "rust/embed-core/Cargo.toml", "--features", "ort"},
				Env:  append([]string{"CARGO_TARGET_DIR=" + target}, gpuEnv(c)...),
				Echo: true,
			}); err != nil {
				return err
			}
			tags := os.Getenv("GOTAGS")
			if tags != "" {
				tags += ","
			}
			tags += "onnx,embed_ffi"
			c.Log.Printf("go build cmd/server -tags %s", tags)
			// APPEND to CGO_LDFLAGS, do not replace it.
			//
			// This build needs TWO link directories, and it only ever named one.
			// scripts/local/toolchain-env.sh exports -L<.local/toolchain/duckdb>
			// because the Windows build links duckdb_use_lib against a supplied
			// libduckdb rather than the embedded archive (see that file for why the
			// static path mixes 1.5.3 headers with 1.4.3 objects). Setting
			// CGO_LDFLAGS here dropped it, and the link failed with
			//
			//   ld.exe: cannot find -lduckdb: No such file or directory
			//
			// every time — which is why this step's recorded state was "never run".
			// It is Optional and the chain calls it with `|| echo`, so the failure
			// never stopped anything and never had to be looked at. The image does
			// not need this binary; the end-to-end proof does, and the proof is the
			// only thing that drives the corpus through the code the image ships.
			if err := c.Run(Cmd{Name: "go", Args: []string{
				"build", "-tags", tags, "-o", c.bin("server-full"), "./cmd/server",
			}, Env: []string{"CGO_LDFLAGS=" + ldflagsWith(filepath.Join(target, "release"))}}); err != nil {
				return err
			}
			// Stage every DLL the binary dlopens, beside the binary. Best-effort per
			// file: a missing CUDA provider is a CPU box, not a broken build.
			staged := 0
			for _, src := range serveDLLs(c, target) {
				b, err := os.ReadFile(src)
				if err != nil {
					continue
				}
				if err := WriteAtomic(filepath.Join(c.Local, "bin", filepath.Base(src)), b); err != nil {
					return err
				}
				staged++
			}
			c.Log.Printf("staged %d runtime DLL(s) beside server-full", staged)
			return nil
		},
	}
}

// serveDLLs lists what server-full dlopens at runtime: the embed-core cdylib and
// the ONNX Runtime it shares with the Go reranker (ONE onnxruntime in the process
// — two would clash on symbols, which is the whole reason embed-core is built
// load-dynamic).
func serveDLLs(c *Ctx, cargoTarget string) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	out := []string{filepath.Join(cargoTarget, "release", "embed_core.dll")}
	ort := os.Getenv("ORT_DYLIB_PATH")
	if ort == "" {
		return out
	}
	dir := filepath.Dir(ort)
	for _, n := range []string{
		"onnxruntime.dll",
		"onnxruntime_providers_shared.dll",
		"onnxruntime_providers_cuda.dll",
	} {
		out = append(out, filepath.Join(dir, n))
	}
	return out
}
