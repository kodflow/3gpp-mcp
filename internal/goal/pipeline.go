package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
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
var goBins = []string{"server", "validate", "dbcount", "embedid", "export-delta", "split", "li-audit", "bench"}

// rustBins maps a cargo manifest to the binaries built from it. The embedder is
// deliberately absent: it pulls ONNX Runtime and CUDA, and is built by its own
// step so a machine without a GPU can still complete every other step.
var rustBins = map[string][]string{
	"rust/ingest/Cargo.toml":   {"ingest", "ingest-catalog", "ingest-openapi", "ingest-li"},
	"rust/store/Cargo.toml":    {"merge", "overlay", "freeze-hnsw", "embed-io"},
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
		stepBuildRust(),
		stepBuildEmbedder(),
		stepSeed(),
		stepDiscover(),
		stepFetch(),
		stepIngest(),
		stepMerge(),
		stepEmbed(),
		stepEnrich(),
		stepIndex(),
		stepValidate(),
		stepSmoke(),
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
		Name:      "build-go",
		Version:   1,
		Doc:       "build the Go read-side binaries (server + offline tools)",
		Deps:      []string{"toolchain"},
		Impl:      []string{"cmd", "internal", "go.mod", "go.sum"},
		Toolchain: true,
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
		Name:      "build-rust",
		Version:   1,
		Doc:       "build the Rust write-side binaries (ingest, merge, overlay, embed-io, discover)",
		Deps:      []string{"toolchain"},
		Impl:      []string{"rust", "contracts", "internal/store/schema.sql"},
		Toolchain: true,
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
			return nil
		},
	}
}

// --------------------------------------------------------------------- seed

func stepSeed() *Step {
	const (
		seedURL = "https://github.com/kodflow/3gpp-mcp/releases/download/latest/3gpp.duckdb.zst"
		idxURL  = "https://github.com/kodflow/3gpp-mcp/releases/download/latest/corpus-index.json"
	)
	return &Step{
		Name:    "seed",
		Version: 1,
		Doc:     "seed the corpus from the published lexical snapshot (skipped when a local corpus already exists)",
		Deps:    []string{"build-go"},
		Impl:    []string{"internal/goal/pipeline.go"},
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{c.dataPath("3gpp.duckdb")} },
		Validate: func(c *Ctx) error {
			// Proof that the file is a usable DuckDB, not just bytes on disk.
			out, err := c.Output(Cmd{Name: c.bin("dbcount"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}})
			if err != nil {
				return fmt.Errorf("the seeded DB does not open: %w", err)
			}
			if !strings.Contains(out, "spec_versions=") {
				return fmt.Errorf("dbcount produced no counters: %q", out)
			}
			return nil
		},
		Run: func(c *Ctx) error {
			db := c.dataPath("3gpp.duckdb")
			// NEVER clobber a corpus that is more advanced than the snapshot.
			// The snapshot is a starting point, not an authority.
			if st, err := os.Stat(db); err == nil && st.Size() > 0 {
				c.Log.Printf("a local corpus already exists (%d bytes) — not overwriting it with the published snapshot", st.Size())
				return nil
			}
			if err := os.MkdirAll(c.Data, 0o755); err != nil {
				return err
			}
			zst := db + ".zst"
			c.Log.Printf("downloading the published lexical snapshot (~670 MB)")
			if err := c.Retry(RetryNetwork, "seed download", func() error {
				return c.Run(Cmd{Name: "curl", Args: []string{"-fL", "--retry", "3", "-o", zst, seedURL}, Echo: true})
			}); err != nil {
				return err
			}
			c.Log.Printf("decompressing")
			if err := c.Run(Cmd{Name: "zstd", Args: []string{"-d", "--long=27", "-f", zst, "-o", db}}); err != nil {
				return err
			}
			_ = os.Remove(zst)

			// The published index anchors the delta. Fetching it is best-effort:
			// without it the first discover is simply a full pass.
			idx := filepath.Join(c.Local, "corpus-index.json")
			if err := c.Run(Cmd{Name: "curl", Args: []string{"-fsSL", "-o", idx, idxURL}}); err != nil {
				c.Log.Printf("published corpus-index.json unavailable — the first discover will be a FULL pass")
			}
			return nil
		},
	}
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
		Version: 1,
		Doc:     "diff the live 3GPP status report against the local corpus index",
		Deps:    []string{"build-rust", "seed"},
		Impl:    []string{"rust/discover", "scripts/lib/discover.sh"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{filepath.Join(c.Local, "corpus-index.json")}, nil
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
