package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// This file is the SINGLE SOURCE OF TRUTH for the local pipeline: what the steps
// are, what each one depends on, what defines it, and what proves it worked.
// The Makefile, the /goal command and the status report are all thin wrappers
// over this list — the pipeline is never re-described in YAML, in shell, or in
// documentation, because every duplicate description eventually disagrees with
// the code (which is precisely what happened to the CI it replaces).
//
// # Ordering note: MERGE BEFORE EMBED
//
// The retired CI embedded each shard separately and merged afterwards. That is
// unsafe with a shared embedding ledger: `ingest` rebases chunk_id to ~0 in every
// shard, so two shards both contain a chunk_id 42, and rust/embedder's resume set
// (a HashSet<chunk_id>) would make one shard's clauses silently skipped because
// another shard already used that id. After the merge, chunk_ids are globally
// unique, so ONE ledger is both safe and optimal — and its content-hash map
// deduplicates across every release and series at once.

// exe appends the platform's executable suffix.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// goBins are the Go commands the pipeline needs on disk. cmd/server is the
// product; the others are the offline tools the steps call.
var goBins = []string{"server", "validate", "dbcount", "embedid", "export-delta", "split", "li-audit", "bench", "anchorcheck", "discover-etsi", "migrate-paragraphs", "freeze-hnsw", "seed-evolutions"}

// rustBins maps a cargo manifest to the binaries built from it. The embedder is
// deliberately absent: it pulls ONNX Runtime and CUDA, and is built by its own
// step so a machine without a GPU can still complete every other step.
var rustBins = map[string][]string{
	"rust/ingest/Cargo.toml":   {"ingest", "ingest-catalog", "ingest-openapi", "ingest-li"},
	"rust/store/Cargo.toml":    {"merge", "overlay", "freeze-hnsw", "embed-io", "compact"},
	"rust/discover/Cargo.toml": {"discover"},
}

func (c *Ctx) bin(name string) string  { return filepath.Join(c.Local, "bin", exe(name)) }
func (c *Ctx) rbin(name string) string { return filepath.Join(c.Local, "rust-bin", exe(name)) }

func (c *Ctx) dataPath(parts ...string) string {
	return filepath.Join(append([]string{c.Data}, parts...)...)
}
func (c *Ctx) statePath(parts ...string) string {
	return filepath.Join(append([]string{c.Local, "state"}, parts...)...)
}

// Pipeline returns the ordered step list.
func Pipeline() []*Step {
	return []*Step{
		stepToolchain(),
		stepBuildGo(),
		stepTest(),
		stepBuildRust(),
		stepBuildEmbedder(),
		stepBuildSparse(),
		stepBuildServe(),
		stepSeed(),
		stepDiscover(),
		stepFetch(),
		stepIngest(),
		stepMerge(),
		stepEmbed(corpus3GPP()),
		stepEnrich(),
		stepParagraphs(corpus3GPP()),
		// sparse is ADDITIVE and compact must precede the index (COPY FROM DATABASE
		// does not carry custom indexes), so both sit between the conversion and the
		// freeze rather than after it.
		stepSparse(corpus3GPP()),
		stepCompact(),
		stepIndex(corpus3GPP()),
		stepValidate(),
		// ETSI is built ALONGSIDE 3GPP, always, and gets the SAME treatment: not
		// just ingest + FTS, but vectors and an HNSW index too. An opt-in — or a
		// lexical-only ETSI — would let one corpus fall silently behind, which is
		// precisely the state the tooling was in.
		stepDiscoverETSI(),
		stepCorpusETSI(),
		stepEmbed(corpusETSI()),
		// The ETSI half gets the content-addressed conversion too. Without it
		// Store.SearchClauses takes the branch that ranks VERSIONS instead of
		// clauses, which was harmless only while ETSI held one version per
		// deliverable; with every published version in the corpus it is the
		// "CHECK_IMEI" failure — a result window filled by one clause seen from a
		// dozen versions, and the deliverable that answers never in it.
		stepParagraphs(corpusETSI()),
		stepSparse(corpusETSI()),
		stepIndex(corpusETSI()),
		stepSmoke(),
		// The image is the LAST step, and it is a step rather than a separate
		// entry point because it was the only output of this repository with no
		// determinants: nothing could say whether what consumers pull was the
		// corpus this machine had built. See pipeline_publish.go for the two
		// failures that cost.
		stepPublish(),
	}
}

// ---------------------------------------------------------------- toolchain

func stepToolchain() *Step {
	return &Step{
		Name:      "toolchain",
		Version:   1,
		Doc:       "verify the build toolchain and record its identity",
		Impl:      []string{"scripts/local/toolchain-env.sh"},
		Toolchain: true,
		Tool:      true,
		Outputs:   func(c *Ctx) []string { return []string{c.statePath("toolchain.json")} },
		Validate: func(c *Ctx) error {
			// Cheap and re-run on every plan: a toolchain that vanished (a moved
			// .local, a wiped temp dir) must invalidate the builds that used it.
			// Note `go version`, not `go --version` — the Go CLI has no such flag
			// and answers a usage dump with exit 2.
			for _, t := range [][2]string{{"go", "version"}, {"gcc", "--version"}} {
				if _, err := c.Output(Cmd{Name: t[0], Args: []string{t[1]}}); err != nil {
					return fmt.Errorf("%s is not runnable: %w", t[0], err)
				}
			}
			return nil
		},
		Run: func(c *Ctx) error {
			info := map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}
			for _, t := range [][2]string{{"go", "version"}, {"gcc", "--version"}, {"cargo", "--version"}, {"rustc", "--version"}} {
				out, err := c.Output(Cmd{Name: t[0], Args: []string{t[1]}})
				if err != nil {
					info[t[0]] = "absent"
					c.Log.Printf("%s: absent", t[0])
					continue
				}
				info[t[0]] = firstLine(out)
				c.Log.Printf("%s: %s", t[0], firstLine(out))
			}
			if info["go"] == "absent" || info["gcc"] == "absent" {
				return fmt.Errorf("Go and a C compiler are required (CGO drives DuckDB and ONNX Runtime); run scripts/local/toolchain-bootstrap.sh")
			}
			b, _ := json.MarshalIndent(info, "", "  ")
			return WriteAtomic(c.statePath("toolchain.json"), b)
		},
	}
}

// ------------------------------------------------------------------- builds

func stepBuildGo() *Step {
	return &Step{
		Name:    "build-go",
		Version: 1,
		Doc:     "build the Go read-side binaries (server + offline tools)",
		Deps:    []string{"toolchain"},
		Impl:    []string{"cmd", "internal", "go.mod", "go.sum"},
		// go build ignores _test.go and testdata, so a test edit must not relink
		// eight binaries. The `test` step deliberately does NOT set this.
		ExcludeTests: true,
		Toolchain:    true,
		Tool:         true,
		Outputs: func(c *Ctx) []string {
			out := make([]string, 0, len(goBins))
			for _, b := range goBins {
				out = append(out, c.bin(b))
			}
			return out
		},
		Run: func(c *Ctx) error {
			if err := os.MkdirAll(filepath.Join(c.Local, "bin"), 0o755); err != nil {
				return err
			}
			tags := os.Getenv("GOTAGS")
			for _, b := range goBins {
				args := []string{"build"}
				if tags != "" {
					args = append(args, "-tags", tags)
				}
				args = append(args, "-o", c.bin(b), "./cmd/"+b)
				c.Log.Printf("building cmd/%s", b)
				if err := c.Run(Cmd{Name: "go", Args: args}); err != nil {
					return err
				}
			}
			// On Windows the DuckDB DLL must sit beside the executables: the
			// loader does not read a POSIX-style PATH.
			if runtime.GOOS == "windows" {
				if dll := os.Getenv("DUCKDB_LIB_DIR"); dll != "" {
					src := filepath.Join(dll, "duckdb.dll")
					if b, err := os.ReadFile(src); err == nil {
						if err := WriteAtomic(filepath.Join(c.Local, "bin", "duckdb.dll"), b); err != nil {
							return err
						}
						c.Log.Printf("duckdb.dll staged next to the binaries")
					}
				}
			}
			return nil
		},
	}
}

func stepBuildRust() *Step {
	return &Step{
		Name:         "build-rust",
		Version:      1,
		Doc:          "build the Rust write-side binaries (ingest, merge, overlay, embed-io, discover)",
		Deps:         []string{"toolchain"},
		Impl:         []string{"rust", "contracts", "internal/store/schema.sql"},
		ExcludeTests: true,
		Toolchain:    true,
		Tool:         true,
		Outputs: func(c *Ctx) []string {
			var out []string
			for _, bins := range rustBins {
				for _, b := range bins {
					out = append(out, c.rbin(b))
				}
			}
			return out
		},
		Run: func(c *Ctx) error {
			if _, err := c.Output(Cmd{Name: "cargo", Args: []string{"--version"}}); err != nil {
				return fmt.Errorf("cargo is required to build the write-side: %w", err)
			}
			target := filepath.Join(c.Local, "cargo-target")
			dst := filepath.Join(c.Local, "rust-bin")
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			for manifest, bins := range rustBins {
				args := []string{"build", "--release", "--manifest-path", manifest}
				for _, b := range bins {
					args = append(args, "--bin", b)
				}
				c.Log.Printf("cargo build %s", manifest)
				if err := c.Run(Cmd{Name: "cargo", Args: args, Env: []string{"CARGO_TARGET_DIR=" + target}, Echo: true}); err != nil {
					return err
				}
				// Copy out of target/ so the outputs live at a stable path the
				// state machine can check, independent of cargo's layout.
				for _, b := range bins {
					src := filepath.Join(target, "release", exe(b))
					data, err := os.ReadFile(src)
					if err != nil {
						return fmt.Errorf("cargo reported success but %s is missing: %w", src, err)
					}
					if err := WriteAtomic(c.rbin(b), data); err != nil {
						return err
					}
					_ = os.Chmod(c.rbin(b), 0o755)
				}
			}
			return stageRuntimeDLLs(c)
		},
	}
}

// --------------------------------------------------------------------- seed

// stepSeed pulls the published corpus as a STARTING POINT, so a fresh machine
// does not have to re-ingest 20 163 spec versions to get going.
//
// It used to curl `releases/download/latest/3gpp.duckdb.zst`. That asset is a
// full-text DuckDB on a public repository, which DATA_NOTICE.md forbids — so the
// pipeline that builds the corpus was itself a consumer of the breach, not just
// the client binary. It now goes through the private GHCR package, exactly like
// `mcp-3gpp bootstrap` (internal/bootstrap/ghcr_corpus.go).
//
// Seeding is an OPTIMISATION, never a requirement: with no credential the step
// leaves the corpus absent and says so, and the pipeline builds it from 3gpp.org
// the licit way — slower, and the path that has to keep working regardless.
func stepSeed() *Step {
	return &Step{
		Name:    "seed",
		Version: 2, // bumped: the source changed, so a cached success must not carry over
		Doc:     "seed the corpus from the published snapshot on the private GHCR package (skipped when a local corpus already exists, or when no credential is available)",
		Deps:    []string{"build-go"},
		Impl:    []string{"internal/goal/pipeline.go"},
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{c.dataPath("3gpp.duckdb")} },
		Validate: func(c *Ctx) error {
			// Proof that the file is a usable DuckDB, not just bytes on disk.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}})
			if err != nil {
				// THE ONE THAT MATTERS MOST. The Run below downloads and REPLACES
				// the corpus, so "cannot open" must never be allowed to mean
				// "re-acquire 21 GB" on the strength of a stale file handle.
				return stillOpenElsewhere("3gpp.duckdb",
					fmt.Errorf("the seeded DB does not open: %w", err))
			}
			if !strings.Contains(out, "spec_versions=") {
				return fmt.Errorf("dbcount produced no counters: %q", out)
			}
			return nil
		},
		Run: func(c *Ctx) error {
			db := c.dataPath("3gpp.duckdb")
			// seededNow records whether THIS run produced the corpus from the
			// published package. It is what lets seedAnchor adopt the published
			// anchor without hashing 12.36 GB to re-derive a fact we already know.
			seededNow := false

			// NEVER clobber a corpus that is more advanced than the snapshot.
			// The snapshot is a starting point, not an authority.
			if st, err := os.Stat(db); err == nil && st.Size() > 0 {
				c.Log.Printf("a local corpus already exists (%d bytes) — not overwriting it with the published snapshot", st.Size())
				// And SAY so, in the one way the state machine understands. This
				// branch does no work and produces no artefact, but it used to
				// record an ordinary success — so bumping seed's Version (which the
				// move to GHCR legitimately required) republished a new identity for
				// a step that had touched nothing, and discover, fetch, ingest,
				// merge and every vector step behind them were scheduled to replay a
				// finished 22 GB corpus. A decline says "nothing to do" and carries
				// the previous provenance forward, which is the truth here.
				if err := seedAnchor(c, db, false); err != nil {
					return err
				}
				reportAnchorHoles(c, db)
				return fmt.Errorf("%w: a local corpus is already present, seeding would add nothing", ErrDeclined)
			} else {
				if err := os.MkdirAll(c.Data, 0o755); err != nil {
					return err
				}
				pat, origin, cerr := bootstrap.GHCRCredential("")
				if cerr != nil {
					c.Log.Printf("no GHCR credential (set GHCR_PAT, or write a read:packages token to .local/ghcr.pat) — "+
						"NOT seeding. %s stays absent and the pipeline will build it from 3gpp.org, which is slower and equally correct.",
						db)
					return fmt.Errorf("%w: no GHCR credential for the corpus package", ErrDeclined)
				}
				src := bootstrap.Corpus3GPP(os.Getenv("MCP3GPP_GHCR_OWNER"), os.Getenv("MCP3GPP_CORPUS_TAG"))
				c.Log.Printf("seeding from %s (credential from %s) — large, and it resumes if interrupted", src, origin)
				if err := bootstrap.FetchCorpus(c.Context, src, pat, db, c.Log.Printf); err != nil {
					return fmt.Errorf("seed from %s: %w", src, err)
				}
				seededNow = true
			}
			if err := seedAnchor(c, db, seededNow); err != nil {
				return err
			}
			reportAnchorHoles(c, db)
			return nil
		},
	}
}

// reportAnchorHoles makes the anchor's over-claims visible at the moment the
// anchor is installed, which is the only moment anyone is looking at it.
//
// It REPORTS and does not fail. The 56 known holes are a property of the
// published snapshot, not of this build, so gating `seed` on them would block the
// supported path for a defect the operator cannot fix here. But leaving them
// unmentioned is how they stayed invisible for months: discover trusts the
// anchor, and so does every step after it, so a hole has no other chance to be
// noticed. `anchorcheck` exits 1 on a hole, which is what makes it usable as a
// real gate elsewhere (CI, pre-publish) — here the count in the log is the point.
func reportAnchorHoles(c *Ctx, db string) {
	idx := filepath.Join(c.Local, "corpus-index.json")
	if !fileNonEmpty(idx) {
		return
	}
	// anchorcheck exits 1 to REPORT holes. `c.Output` discards stdout on a non-zero
	// exit, so reading its result made a successful check that found 56 holes look
	// like a check that never ran — the log said "unverified" when the answer was
	// sitting in the discarded stdout. Reading the emitted state file instead makes
	// the finding independent of the exit code.
	state := c.statePath("corpus-state.json")
	_, runErr := c.Output(Cmd{Name: c.bin("anchorcheck"), Args: []string{
		"--db", db, "--index", idx, "--quiet", "--emit-state", state,
	}})
	counts, err := readCorpusStateCounts(state)
	if err != nil {
		c.Log.Printf("anchor consistency UNVERIFIED: anchorcheck produced no state file (%v)", runErr)
		return
	}
	c.Log.Printf("anchor: indexed=%d non_content=%d missing_content=%d over_claim=%d",
		counts["indexed"], counts["non_content"], counts["missing_content"], counts["over_claim"])
	if counts["missing_content"]+counts["over_claim"] > 0 {
		c.Log.Printf("anchor: %d key(s) claim text the corpus does not hold — discover will SKIP them. "+
			"`goal run --repair` folds them into the fetch plan.",
			counts["missing_content"]+counts["over_claim"])
	}
}

// readCorpusStateCounts reads the counters anchorcheck persisted, so the finding
// survives the tool's exit code rather than depending on it.
func readCorpusStateCounts(path string) (map[string]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Counts == nil {
		return nil, fmt.Errorf("no counts in %s", path)
	}
	return st.Counts, nil
}

// ----------------------------------------------------------------- discover

// discoverTTL is how long a fetched 3GPP status report is considered current.
//
// It exists to make two requirements coexist. An immediate second /goal must be
// a no-op (so discover cannot re-run just because the network exists), yet the
// pipeline must still notice that 3GPP published something (so discover cannot
// be pinned to its inputs forever). Bucketing the report's age turns time itself
// into a determinant: within the window the fingerprint is stable and the step
// skips; past it the bucket moves and the step re-runs.
const discoverTTL = 6 * time.Hour

func stepDiscover() *Step {
	return &Step{
		Name:    "discover",
		Version: 3,
		Doc:     "diff the live 3GPP status report against the local corpus index",
		Deps:    []string{"build-rust", "seed"},
		Impl:    []string{"rust/discover", "scripts/lib/discover.sh"},
		Inputs: func(c *Ctx) ([]string, error) {
			in := []string{filepath.Join(c.Local, "corpus-index.json")}
			// The accepted-absent ledger decides as much of the work list as the
			// corpus index does — a key in it is not drift. Leaving it out would
			// mean that deleting or extending the ledger changed what discover
			// produces without changing what discover claims to depend on, and the
			// step would skip while its answer was stale.
			if a := absentIndexPath(c); fileNonEmpty(a) {
				in = append(in, a)
			}
			return in, nil
		},
		Extra: func(c *Ctx) (map[string]string, error) {
			m := map[string]string{
				"floor": c.Cfg("floor"),
				"scope": c.Cfg("scope"),
			}
			// Age bucket of the cached report (see discoverTTL).
			bucket := "none"
			if st, err := os.Stat(c.statePath("status-report.htm")); err == nil {
				bucket = strconv.FormatInt(int64(time.Since(st.ModTime())/discoverTTL), 10)
			}
			m["report_bucket"] = bucket
			return m, nil
		},
		Outputs: func(c *Ctx) []string {
			return []string{c.statePath("series.json"), c.statePath("worklist.txt")}
		},
		// runDiscover writes exactly these two files, plus status-report.htm — which
		// is its OWN HTTP cache, read by nothing downstream and already folded into
		// this step's fingerprint as an age bucket. So for a dependant, these two
		// files are the whole of what discover did: an unchanged delta must not
		// replay fetch, ingest and merge.
		OutputsComplete: true,
		Validate: func(c *Ctx) error {
			b, err := os.ReadFile(c.statePath("series.json"))
			if err != nil {
				return err
			}
			var series []string
			if err := json.Unmarshal(b, &series); err != nil {
				return fmt.Errorf("series.json is not a JSON array: %w", err)
			}
			return nil
		},
		Run: func(c *Ctx) error { return runDiscover(c) },
	}
}

// seedAnchor makes sure the delta anchor describes the corpus that is actually
// on disk.
//
// The anchor (corpus-index.json, "spec|Rel -> highest indexed version") is what
// lets discover ask for only what moved. Getting it WRONG in the optimistic
// direction is the dangerous failure: an anchor that over-claims makes discover
// skip specs that were never ingested, and no later step notices — the corpus
// simply has a hole. So the published anchor is used only when the local DB is
// PROVABLY the published snapshot, byte for byte.
//
// When it cannot be proven, the anchor is deliberately left absent: discover then
// does a full pass, which is slow but never silently incomplete. Erring towards
// "do too much" is the only acceptable direction here.
func seedAnchor(c *Ctx, db string, seededNow bool) error {
	idx := filepath.Join(c.Local, "corpus-index.json")
	if fileNonEmpty(idx) {
		c.Log.Printf("delta anchor already present")
		return nil
	}

	// corpus-index.json is "spec|release -> highest indexed version" — a version
	// list, no clause text — so it stays a public release asset. The published
	// `3gpp.duckdb.sha256` beside it does NOT: it existed only to identify the
	// full-text asset that DATA_NOTICE forbids, and it goes with it.
	const idxURL = "https://github.com/kodflow/3gpp-mcp/releases/download/latest/corpus-index.json"

	// The question this used to answer by downloading a checksum and hashing
	// 12.36 GB — "is the local corpus the published one?" — is already answered
	// by whether THIS run just seeded it. Deriving a fact we hold is how the
	// checksum became a second, drifting source of truth about the same thing.
	if !seededNow {
		c.Log.Printf("the local corpus was not seeded from the published package, so the published anchor would over-claim — not using it")
		c.Log.Printf("the next discover will be a FULL pass; the first merge then writes a correct anchor")
		return nil
	}

	c.Log.Printf("the corpus was just seeded from the published package — adopting its anchor")
	tmp := idx + ".new"
	if err := c.Run(Cmd{Name: "curl", Args: []string{"-fsSL", "--max-time", "120", "-o", tmp, idxURL}}); err != nil {
		c.Log.Printf("published anchor unavailable — leaving it absent (FULL pass)")
		return nil
	}
	// The DB was verified against its own sidecar above; the anchor had no
	// checksum at all, so a corpus proven authentic could still be paired with an
	// anchor from another generation. The manifest asserts both in one document.
	// Its absence is tolerated (older publishes predate it) but SAID, because a
	// silent fallback to the unverified path is indistinguishable from a verified
	// one in the log — and that is the whole failure pattern of this pipeline.
	if err := verifyAnchorAgainstManifest(c, tmp); err != nil {
		_ = os.Remove(tmp)
		c.Log.Printf("anchor rejected: %v", err)
		c.Log.Printf("leaving the anchor absent; the next discover is a FULL pass, which is slow and correct")
		return nil
	}
	// Publish only after it parses: a truncated anchor is worse than none.
	b, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	var probe map[string]any
	if err := json.Unmarshal(b, &probe); err != nil {
		_ = os.Remove(tmp)
		c.Log.Printf("published anchor is not valid JSON — ignored")
		return nil
	}
	c.Checkpoint("anchor_entries", strconv.Itoa(len(probe)))
	c.Log.Printf("anchor adopted: %d (spec, release) entries", len(probe))
	return os.Rename(tmp, idx)
}

// sha256File streams a file through SHA-256. The corpus is multi-gigabyte, so it
// is read in chunks rather than loaded.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stepTest keeps the unit and contract suites inside the goal, not beside it.
//
// "The tests pass" is a condition of the goal, so it belongs in the DAG with a
// fingerprint like everything else: it re-runs when any Go source OR test
// changes, and skips otherwise. Its Impl deliberately INCLUDES test files — the
// exact opposite of the build steps, and the reason ExcludeTests exists.
func stepTest() *Step {
	return &Step{
		Name:    "test",
		Version: 2,
		Doc:     "run the Go unit and contract suites, the Rust workspace suite, and the shell tests",
		Deps:    []string{"build-go"},
		// `rust` joins the fingerprint because the step now runs the Rust suite:
		// without it, editing rust/store would leave this step reporting SKIP, which
		// is how the Rust tests came to be written and never run in the first place.
		Impl:      []string{"cmd", "internal", "go.mod", "go.sum", "scripts", "rust"},
		Toolchain: true,
		Outputs:   func(c *Ctx) []string { return []string{c.statePath("test-report.txt")} },
		Run: func(c *Ctx) error {
			args := []string{"test", "-count=1"}
			if tags := os.Getenv("GOTAGS"); tags != "" {
				args = append(args, "-tags", tags)
			}
			args = append(args, "./...")
			if err := c.Run(Cmd{Name: "go", Args: args, Echo: true}); err != nil {
				return err
			}
			// THE RUST SUITE, WHICH NOTHING RAN.
			//
			// This step checked Go and the shell scripts; scripts/rust-fmt_test.sh
			// checked Rust FORMATTING. Nothing ran `cargo test`. So every test in
			// rust/store — the ones guarding the code that REWRITES THE CORPUS, which
			// are the highest-stakes tests here — existed and was invisible unless
			// somebody typed the command by hand. That is exactly the failure
			// runShellTests was written to end, one language over.
			//
			// The workspace is the right scope, and it is the project's OWN
			// definition of the core: rust/Cargo.toml lists store, parse, ingest and
			// identity, excluding embedder, embed-core and discover on purpose (heavy
			// ort/CUDA toolchain, a cdylib, a CI-matrix tool). What is left is
			// precisely the DuckDB write side. rust-fmt_test.sh still covers the
			// excluded three for formatting, so nothing loses a check.
			c.Log.Printf("cargo test --release --workspace (rust/)")
			if err := c.Run(Cmd{Name: "cargo", Args: []string{
				"test", "--release", "--manifest-path", "rust/Cargo.toml", "--workspace",
			}, Echo: true}); err != nil {
				return err
			}
			shells, err := runShellTests(c)
			if err != nil {
				return err
			}
			// Keep the evidence on disk: the final report cites this file rather
			// than asking the reader to take "tests passed" on trust.
			return WriteAtomic(c.statePath("test-report.txt"),
				[]byte(fmt.Sprintf("go test -count=1 -tags %q ./...  : PASS\ncargo test --release --workspace : PASS\n%d shell test(s): PASS\n",
					os.Getenv("GOTAGS"), shells)))
		},
	}
}

// runShellTests runs every scripts/*_test.sh and returns how many passed.
//
// They existed and nothing ran them. `scripts/etsi-corpus_test.sh` and
// `scripts/kaggle-gpu-check_test.sh` were both written, both green, and both
// invisible to `go test ./...` — which is to say they protected nothing. The
// mktemp break they now guard against is precisely the kind that only shows on
// one platform, so leaving their execution to whoever remembers is leaving it to
// nobody.
//
// A missing bash is FATAL here rather than skipped: the pipeline already requires
// bash for corpus.sh and etsi-corpus.sh, so "no bash" means the run was never
// going to work, and quietly passing a test step would say the opposite.
func runShellTests(c *Ctx) (int, error) {
	// Both levels: scripts/ and scripts/<pkg>/. A single-level glob would have
	// silently ignored scripts/lib/convert_test.sh, which is the very failure mode
	// this function exists to end.
	var files []string
	for _, pat := range []string{
		filepath.Join(c.Root, "scripts", "*_test.sh"),
		filepath.Join(c.Root, "scripts", "*", "*_test.sh"),
	} {
		found, err := filepath.Glob(pat)
		if err != nil {
			return 0, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		c.Log.Printf("no scripts/*_test.sh found")
		return 0, nil
	}
	for _, f := range files {
		rel, relErr := filepath.Rel(c.Root, f)
		if relErr != nil {
			rel = f
		}
		c.Log.Printf("shell test: %s", filepath.ToSlash(rel))
		if err := c.Run(Cmd{Name: "bash", Args: []string{filepath.ToSlash(rel)}, Echo: true}); err != nil {
			return 0, fmt.Errorf("shell test %s failed: %w", filepath.ToSlash(rel), err)
		}
	}
	c.Log.Printf("%d shell test(s) passed", len(files))
	return len(files), nil
}

// stageRuntimeDLLs copies the compiler runtime next to the Rust binaries on
// Windows.
//
// A Rust binary built for the *-pc-windows-gnu target links libstdc++ and
// libgcc DYNAMICALLY. They live in the mingw toolchain's bin directory, which is
// on PATH inside the shell that built them and absent everywhere else — so the
// binaries ran fine from the build shell and died with 0xC0000139
// (STATUS_ENTRYPOINT_NOT_FOUND) the moment anything else launched them. Windows
// searches the executable's own directory first, so staging the DLLs beside them
// makes the binaries self-contained wherever they are invoked from.
func stageRuntimeDLLs(c *Ctx) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	// The mingw bin directory sits next to the cargo home the bootstrap created.
	candidates := []string{
		filepath.Join(c.Local, "toolchain", "ucrt64", "bin"),
		filepath.Join(c.Local, "toolchain", "w64devkit", "bin"),
	}
	needed := []string{"libstdc++-6.dll", "libgcc_s_seh-1.dll", "libwinpthread-1.dll"}
	staged := 0
	for _, dll := range needed {
		for _, dir := range candidates {
			src := filepath.Join(dir, dll)
			b, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			if err := WriteAtomic(filepath.Join(c.Local, "rust-bin", dll), b); err != nil {
				return err
			}
			staged++
			break
		}
	}
	if staged > 0 {
		c.Log.Printf("staged %d mingw runtime DLL(s) next to the Rust binaries", staged)
	}
	return nil
}

// gpuEnv returns the environment additions the GPU embedder needs, and ONLY it.
//
// Two hard-won details:
//
//   - The CUDA directory must NOT be on the PATH of the other tools. Putting it
//     there made embed-io die with 0xC0000139: the loader picked a shadowing
//     export out of the CUDA set. The runtime is therefore scoped to the one
//     process that needs it.
//   - The paths must be in NATIVE Windows form. A POSIX-style PATH inherited
//     from a bash shell is invisible to the Windows loader, which is why
//     onnxruntime_providers_cuda.dll failed with error 126 ("module not found")
//     while the file was plainly there.
func gpuEnv(c *Ctx) []string {
	ort, cuda := c.Cfg("ort_dir"), c.Cfg("cuda_dir")
	if ort == "" {
		// The bootstrap unpacks one versioned ONNX Runtime under
		// .local/toolchain/ort/<pkg>/lib; glob rather than hard-code the version.
		if m, _ := filepath.Glob(filepath.Join(c.Local, "toolchain", "ort", "*", "lib")); len(m) > 0 {
			ort = m[0]
		}
	}
	if cuda == "" {
		d := filepath.Join(c.Local, "toolchain", "cuda", "dll")
		if dirExists(d) {
			cuda = d
		}
	}
	if ort == "" && cuda == "" {
		return nil
	}
	var prefix []string
	if cuda != "" {
		prefix = append(prefix, filepath.FromSlash(cuda))
	}
	if ort != "" {
		prefix = append(prefix, filepath.FromSlash(ort))
	}
	env := []string{"PATH=" + strings.Join(prefix, string(os.PathListSeparator)) + string(os.PathListSeparator) + os.Getenv("PATH")}
	if ort != "" {
		env = append(env, "ORT_DYLIB_PATH="+filepath.Join(filepath.FromSlash(ort), ortLibName()))
	}
	// rust/embedder defaults its tracing filter to "warn,ort=debug" when RUST_LOG
	// is unset. That is the right default for ONE diagnostic run — it is how a
	// silent CPU fallback becomes visible — and the wrong one for a multi-hour
	// bulk campaign, where ONNX Runtime emits DEBUG continuously and the log
	// becomes the bottleneck rather than the GPU. Keep the EP-registration
	// messages (they are INFO/ERROR) and drop the per-graph chatter. Set RUST_LOG
	// explicitly to get the verbose behaviour back.
	if os.Getenv("RUST_LOG") == "" {
		env = append(env, "RUST_LOG=warn,ort=info")
	}
	return env
}

func ortLibName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}
