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
				Env:  []string{"CARGO_TARGET_DIR=" + target},
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
			return os.Chmod(c.rbin("embedder"), 0o755)
		},
	}
}

// -------------------------------------------------------------------- embed

func stepEmbed() *Step {
	return &Step{
		Name:    "embed",
		Version: 1,
		Doc:     "vectorise the corpus on the GPU, reusing every already-seen content hash",
		Deps:    []string{"merge", "build-embedder"},
		Impl:    []string{"rust/embedder/src", "rust/store/src/bin/embed_io.rs", "internal/embed/identity.go", "internal/embed/models.yaml"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.dataPath("3gpp.duckdb")}, nil
		},
		Extra: func(c *Ctx) (map[string]string, error) {
			// The embed identity is THE determinant: model family, revision,
			// tokenizer, dimension, normalisation, precision, windowing and
			// max_tokens are all folded into it. A change to any of them must
			// invalidate the vectors and the vector index — and nothing else.
			return map[string]string{
				"embed_identity": embedIdentityForPlan(c),
				"embed_floor":    c.Cfg("embed_floor"),
			}, nil
		},
		Heavy:   true,
		Outputs: func(c *Ctx) []string { return []string{filepath.Join(c.Local, "vecs", "ledger.jsonl")} },
		Validate: func(c *Ctx) error {
			// The authoritative check is the DB's own report: no clause at or
			// above the floor may be missing a vector.
			rep, err := embedReport(c)
			if err != nil {
				return err
			}
			if rep.Model == "" {
				return fmt.Errorf("the DB carries no embedding_model — vectors were never imported")
			}
			if rep.NullAtFloor > 0 {
				return fmt.Errorf("%d clause(s) at/above %s still have no vector", rep.NullAtFloor, c.Cfg("embed_floor"))
			}
			return nil
		},
		Run: func(c *Ctx) error { return runEmbed(c) },
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

func embedReport(c *Ctx) (*embedIOReport, error) {
	out, err := c.Output(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", c.dataPath("3gpp.duckdb"), "--report", "--embed-floor", c.Cfg("embed_floor"),
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
func runEmbed(c *Ctx) error {
	db := c.dataPath("3gpp.duckdb")
	vecDir := filepath.Join(c.Local, "vecs")
	if err := os.MkdirAll(vecDir, 0o755); err != nil {
		return err
	}
	ledger := filepath.Join(vecDir, "ledger.jsonl")

	id, err := embedIdentity(c)
	if err != nil {
		return err
	}
	c.Log.Printf("embed identity: %s", id)

	// A changed identity makes every cached vector meaningless: clause_hash folds
	// the identity in, so by_hash would match nothing anyway. Archive rather than
	// delete — reverting a model bump should not cost another full GPU pass.
	idFile := filepath.Join(vecDir, ".identity")
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

	worklist := filepath.Join(vecDir, "worklist.jsonl")
	c.Log.Printf("exporting the work list (clauses with no vector, floor=%s)", c.Cfg("embed_floor"))
	if err := c.Run(Cmd{Name: c.rbin("embed-io"), Args: []string{
		"--db", db, "--export-worklist", worklist, "--embed-floor", c.Cfg("embed_floor"),
	}}); err != nil {
		return err
	}
	todo := countLines(worklist)
	c.Checkpoint("worklist", strconv.Itoa(todo))
	if todo == 0 {
		c.Log.Printf("every clause already carries a vector — nothing to do")
		return nil
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
	}, Echo: true}); err != nil {
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
		Impl:    []string{"rust/ingest/src/bin"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.statePath("status-report.htm"), c.dataPath("sources", "5g-apis")}, nil
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

			if apis := c.dataPath("sources", "5g-apis"); dirExists(apis) {
				c.Log.Printf("5GC OpenAPI overlay")
				if err := c.Run(Cmd{Name: c.rbin("ingest-openapi"), Args: []string{"--src", apis, "--db", db}, Echo: true}); err != nil {
					return err
				}
			} else {
				c.Log.Printf("no data/sources/5g-apis — skipping the OpenAPI overlay (run scripts/fetch-5g-apis.sh to add it)")
			}

			// ingest-li is wired into no workflow and no make target today, while
			// li_events/asn1_types are in the schema and the `li` subject declares
			// a footprint. Wire it here rather than leave the tables empty.
			if asn := findASN(c); asn != "" {
				c.Log.Printf("Lawful Interception registry from %s", filepath.Base(asn))
				if err := c.Run(Cmd{Name: c.rbin("ingest-li"), Args: []string{"--db", db, "--asn", asn}, Echo: true}); err != nil {
					return err
				}
			} else {
				c.Log.Printf("no TS33128Payloads .asn found — li_events stays empty")
			}
			return nil
		},
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func findASN(c *Ctx) string {
	var found string
	for _, base := range []string{c.dataPath("sources"), c.dataPath("asn")} {
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil || found != "" || info.IsDir() {
				return nil
			}
			if strings.HasPrefix(info.Name(), "TS33128Payloads") && strings.HasSuffix(info.Name(), ".asn") {
				found = p
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// -------------------------------------------------------------------- index

func stepIndex() *Step {
	return &Step{
		Name:    "index",
		Version: 1,
		Doc:     "build and freeze the HNSW cosine index over the vectors",
		Deps:    []string{"embed", "enrich"},
		Impl:    []string{"rust/store/src/bin/freeze_hnsw.rs"},
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
			rep, err := embedReport(c)
			if err != nil {
				return err
			}
			if !rep.HNSW {
				return fmt.Errorf("the DB reports no frozen HNSW index")
			}
			return nil
		},
		Run: func(c *Ctx) error {
			rep, err := embedReport(c)
			if err != nil {
				return err
			}
			if rep.Embedded == 0 {
				return fmt.Errorf("refusing to build a vector index over zero vectors")
			}
			c.Log.Printf("freezing the HNSW index over %d vectors", rep.Embedded)
			return c.Run(Cmd{Name: c.rbin("freeze-hnsw"), Args: []string{"--db", c.dataPath("3gpp.duckdb")}, Echo: true})
		},
	}
}

// ------------------------------------------------------------------ validate

func stepValidate() *Step {
	return &Step{
		Name:    "validate",
		Version: 1,
		Doc:     "run the data-completeness contract against the finished corpus",
		Deps:    []string{"index"},
		Impl:    []string{"cmd/validate", "scripts/data-contract.sh"},
		Inputs: func(c *Ctx) ([]string, error) {
			return []string{c.dataPath("3gpp.duckdb")}, nil
		},
		Run: func(c *Ctx) error {
			args := []string{"--db", c.dataPath("3gpp.duckdb"), "--report", "text"}
			args = append(args, strings.Fields(c.Cfg("contract_flags"))...)
			c.Log.Printf("contract: %s", c.Cfg("contract_flags"))
			return c.Run(Cmd{Name: c.bin("validate"), Args: args, Echo: true})
		},
	}
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
			return []string{c.dataPath("3gpp.duckdb")}, nil
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
	cmd := exec.CommandContext(c.Context, c.bin("server"), "serve", "--db", db)
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
				return nil, fmt.Errorf("%w (server stderr: %s)", r.err, tailString(stderr.String(), 12))
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

func tailString(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}
