package goal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
		Version: 1,
		Doc:     "vectorise the corpus on the GPU, reusing every already-seen content hash",
		Deps:    append([]string{"build-embedder"}, t.singleProducer()...),
		// data/3gpp.duckdb has two producers, not one: `merge` folds local shards
		// into it, `seed` downloads the published snapshot. Both are supported
		// paths to a vectorisable corpus. ETSI has a single producer, so it goes
		// through Deps instead (AnyDeps rejects a one-element set on purpose).
		AnyDeps: t.multiProducer(),
		Impl:    []string{"rust/embedder/src", "rust/store/src/bin/embed_io.rs", "internal/embed/identity.go", "internal/embed/models.yaml"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{t.dbPath(c)}, nil
		},
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
		return nil, err
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

	// THE WORKLIST READS THROUGH THE VIEW; THE WRITE-BACK CANNOT.
	//
	// On a content-addressed corpus (ADR 0004) `clauses` is a view, and DuckDB
	// answers an UPDATE against it with a hard error. Reading is fine — the
	// filters land on `clause_occ` and `bodies` before any text is rebuilt, which
	// is why the export above costs seconds and not hours — so the restore is
	// deferred to here, AFTER the count. A run with nothing to embed then pays
	// nothing at all, which is the common case once the corpus is complete.
	if err := ensureWriteShape(c, db); err != nil {
		return err
	}
	before := countLines(ledger)
	c.Log.Printf("%d clause(s) to vectorise (ledger already holds %d)", todo, before)

	if _, err := os.Stat(c.Cfg("model_dir")); err != nil {
		return fmt.Errorf("the BGE-M3 model is missing at %s: %w", c.Cfg("model_dir"), err)
	}

	c.Log.Printf("running the GPU pass — only DISTINCT texts reach the model")
	if err := c.Run(Cmd{Name: c.rbin("embedder"), Args: []string{
		"--in", worklist, "--out", ledger,
		"--model-dir", c.Cfg("model_dir"),
		"--embed-identity", id,
		"--require-cuda",
		"--vram-fraction", "0.8",
		"--max-batch", "512",
	}, Env: gpuEnv(c), Echo: true}); err != nil {
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

func stepEnrich() *Step {
	return &Step{
		Name:    "enrich",
		Version: 1,
		Doc:     "overlay the DynaReport catalogue, the 5GC OpenAPI corpus and the LI registry",
		Deps:    []string{"merge"},
		// The two fetch scripts are part of this step's implementation now that it
		// runs them: changing how an overlay is acquired must replay the overlay.
		Impl: []string{"rust/ingest/src/bin", "scripts/fetch-5g-apis.sh", "scripts/fetch-li-asn.sh"},
		Inputs: func(c *Ctx) ([]string, error) {
			// data/sources/asn joins the inputs for the same reason 5g-apis is
			// already here: acquiring the LI registry must make the overlay dirty,
			// or the corpus keeps whatever li_events it had.
			return []string{
				c.statePath("status-report.htm"),
				c.dataPath("sources", "5g-apis"),
				c.dataPath("sources", "asn"),
			}, nil
		},
		Heavy: true,
		Validate: func(c *Ctx) error {
			// doc_type is hard-coded to "TS" by the parser until the catalogue
			// overlay corrects it, so an un-enriched corpus cannot honour "filter
			// to TS by default". Prove the overlay actually landed.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}})
			if err != nil {
				return err
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
			return nil
		},
	}
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
			return map[string]string{
				"embed_identity": embedIdentityForPlan(c),
				"metric":         "cosine",
				"dim":            "1024",
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

func stepValidate() *Step {
	return &Step{
		Name:    "validate",
		Version: 1,
		Doc:     "run the data-completeness contract against the finished corpus",
		Deps:    []string{"index"},
		Impl:    []string{"cmd/validate", "cmd/anchorcheck", "scripts/data-contract.sh", "contracts/accepted-absences.txt"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.dataPath("3gpp.duckdb")}, nil
		},
		Run: func(c *Ctx) error {
			args := validateArgs(c)
			c.Log.Printf("contract: %s (embed floor %q)", c.Cfg("contract_flags"), corpus3GPP().Floor(c))
			if err := c.Run(Cmd{Name: c.bin("validate"), Args: args, Echo: true}); err != nil {
				return err
			}
			return validateAnchor(c)
		},
	}
}

// validateArgs builds the contract command line.
//
// --require-embed-complete is FLOOR-AWARE, and the only floor that makes it mean
// anything is the one `embed` actually ran with: clauses below it are deliberately
// left NULL (cmd/validate: "Below-floor/legacy clauses are intentionally NULL and
// never counted"). Leaving the floor out made the contract demand vectors for a
// population embed was never asked to cover, and it failed a corpus that was in
// fact complete — 413 pre-Rel-99 LCS clauses (GSM-era 03.71) against
// embed_floor="Rel-99", while validate counted at floor "".
//
// An explicit --embed-floor in contract_flags still wins: an operator overriding
// the contract by hand is a decision, not an accident.
func validateArgs(c *Ctx) []string {
	args := []string{"--db", c.dataPath("3gpp.duckdb"), "--report", "text"}
	flags := strings.Fields(c.Cfg("contract_flags"))
	args = append(args, flags...)
	if floor := corpus3GPP().Floor(c); floor != "" && !hasFlag(flags, "--embed-floor") {
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
		Name:    "smoke",
		Version: 1,
		Doc:     "start the real server over stdio and prove vector search stays enabled",
		Deps:    []string{"validate"},
		Impl:    []string{"cmd/server", "internal/mcp", "internal/search"},
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

	// One representative call per retrieval path that the corpus can actually
	// answer today, so the smoke exercises the real search code, not a stub.
	queries := []struct{ tool, arg, val string }{
		{"search_spec", "query", "AMF registration procedure"},
		{"list_specs", "series", "23"},
		{"resolve_term", "term", "AMF"},
	}
	id := 3
	for _, q := range queries {
		if !contains(names, q.tool) {
			continue
		}
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
		if _, bad := m["error"]; bad {
			return fmt.Errorf("%s returned an error: %v", q.tool, m["error"])
		}
		c.Log.Printf("%s(%s=%q) answered", q.tool, q.arg, q.val)
		id++
	}

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
	c.Log.Printf("vector search was NOT disabled at startup")
	return nil
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
	// Producer names the step that writes DB (for AnyDeps / Deps).
	Producers []string
}

func corpus3GPP() corpusTarget {
	return corpusTarget{
		Suffix:    "",
		DB:        "3gpp.duckdb",
		Ledger:    "ledger.jsonl",
		Floor:     func(c *Ctx) string { return c.Cfg("embed_floor") },
		Producers: []string{"merge", "seed"},
	}
}

func corpusETSI() corpusTarget {
	return corpusTarget{
		Suffix:    "-etsi",
		DB:        "etsi.duckdb",
		Ledger:    "etsi-ledger.jsonl",
		Floor:     func(c *Ctx) string { return "" },
		Producers: []string{"corpus-etsi"},
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
// `enrich` — the catalogue overlay rewrites rows, and rebuilding the index before
// it would index a corpus that is about to change. ETSI has no catalogue overlay
// (DynaReport describes 3GPP specs, not ETSI deliverables), so it depends on its
// embed alone.
func (t corpusTarget) indexDeps() []string {
	if t.Suffix == "" {
		// The 3GPP index is built AFTER the corpus is content-addressed: the
		// vectors move to `bodies` in that step, and an index built before it
		// would index the table the step is about to drop.
		// build-go because the index is now built by cmd/freeze-hnsw.
		return []string{"embed", "enrich", "paragraphs", "build-go"}
	}
	return []string{"embed" + t.Suffix, "build-go"}
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
func stepParagraphs() *Step {
	return &Step{
		Name:    "paragraphs",
		Version: 1,
		Doc:     "store each paragraph once and point at it (ADR 0004), then drop the clauses table",
		Deps:    []string{"embed", "enrich", "build-go"},
		Impl:    []string{"cmd/migrate-paragraphs"},
		Heavy:   true,
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.dataPath("3gpp.duckdb")}, nil
		},
		Outputs: func(c *Ctx) []string { return []string{c.dataPath("3gpp.duckdb")} },
		Validate: func(c *Ctx) error {
			// Prove the corpus can still produce the text it used to store,
			// rather than trusting that the conversion said so earlier.
			out, err := c.Output(Cmd{Name: c.bin("migrate-paragraphs"), Args: []string{
				"--db", c.dataPath("3gpp.duckdb"), "--verify",
			}})
			if err != nil {
				return fmt.Errorf("the converted corpus does not verify: %w", err)
			}
			if !strings.Contains(out, "clause_occ=") {
				return fmt.Errorf("verification produced no counters: %q", out)
			}
			return nil
		},
		Run: func(c *Ctx) error {
			db := c.dataPath("3gpp.duckdb")
			c.Log.Printf("converting to content-addressed storage (paragraphs, bodies, occurrences)")
			// --drop-clauses is safe to pass unconditionally: the tool refuses to
			// drop anything unless its own verification passed first, and on an
			// already-converted corpus the build is a no-op re-derivation.
			return c.Run(Cmd{Name: c.bin("migrate-paragraphs"), Args: []string{
				"--db", db, "--drop-clauses",
			}, Echo: true})
		},
	}
}
