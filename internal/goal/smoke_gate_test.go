package goal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/eval"
)

// Wire lines captured on 2026-09-11 from .local/bin/server.exe serving the real
// 3gpp.duckdb with etsi.duckdb attached — the process the smoke launches, the
// bytes it reads. They are not hand-written guesses at what MCP looks like.
const (
	// get_spec on a spec the corpus does not hold. A JSON-RPC SUCCESS whose tool
	// failed: the shape the old `m["error"]` check logged as "answered".
	wireToolFailed = `{"jsonrpc":"2.0","id":6,"result":{"content":[{"type":"text","text":"no such spec/clause in corpus: 99.999"}],"isError":true}}`
	// resolve_term on a term nobody defines: a clean answer that found nothing.
	wireFoundNothing = `{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"{\n  \"count\": 0,\n  \"matches\": null,\n  \"term\": \"ZZQXNOTATERM\"\n}"}]}}`
	// resolve_term("AMF"), one of the smoke's own probes, trimmed to its first
	// match; the server answered count 22.
	wireFoundSomething = `{"jsonrpc":"2.0","id":5,"result":{"content":[{"type":"text","text":"{\n  \"count\": 22,\n  \"matches\": [\n    {\n      \"term\": \"AMF\",\n      \"expansion\": \"Access and Mobility Management Function\",\n      \"source_series\": \"33.501\",\n      \"declared_by\": 79\n    }\n  ]\n}"}]}}`
)

func decodeWire(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	return m
}

// A TOOL THAT ANSWERED WITH AN ERROR HAS NOT ANSWERED. The smoke accepted any
// response without a JSON-RPC `error` member, and MCP never puts a tool's failure
// there — search_api answered isError for essentially every query in a published
// image (8d6c657), which is the shape this check could not see.
func TestSmokeRefusesAToolThatAnsweredIsError(t *testing.T) {
	for _, tc := range []struct {
		name, tool, line, quote, why string
	}{
		{"captured from the real server", "get_spec", wireToolFailed,
			"no such spec/clause in corpus: 99.999", "isError=true"},
		// The text 8d6c657 records search_api answering, on the wire shape the
		// server gives every failing handler (mcp.NewToolResultErrorFromErr).
		{"the search_api failure that shipped", "search_api",
			`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"search_api failed: sql: Scan error on column index 9, name \"array_to_string(enum_values, '\\x1f')\": converting NULL to string is unsupported"}],"isError":true}}`,
			"converting NULL to string is unsupported", "isError=true"},
		// The protocol failure the old check did catch must stay caught.
		{"a JSON-RPC protocol error", "search_spec",
			`{"jsonrpc":"2.0","id":3,"error":{"code":-32602,"message":"invalid params"}}`,
			"invalid params", "JSON-RPC error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// mustFind both ways: server_info is judged without a count, and an
			// isError answer must not slip through that door either.
			for _, mustFind := range []bool{true, false} {
				_, _, err := judgeToolAnswer(tc.tool, decodeWire(t, tc.line), mustFind)
				if err == nil {
					t.Fatalf("%s answered with a failure and the smoke accepted it (mustFind=%v)", tc.tool, mustFind)
				}
				// A verdict that does not say WHICH tool and WHAT it said sends the
				// operator to re-run the server by hand to find out.
				if !strings.Contains(err.Error(), tc.tool) || !strings.Contains(err.Error(), tc.quote) {
					t.Errorf("the failure does not name the tool and quote it: %v", err)
				}
				// And for the right reason: refused only because the error text is
				// not a JSON count would be an accident that one probe shape undoes.
				if !strings.Contains(err.Error(), tc.why) {
					t.Errorf("refused, but not as %q (mustFind=%v): %v", tc.why, mustFind, err)
				}
			}
		})
	}
}

// AN EMPTY ANSWER TO A PROBE THAT MUST FIND SOMETHING IS A FAILURE. Every probe
// finds something on this corpus (count 10, 170, 22 on 2026-09-11), so `count 0`
// is a path that went empty instead of failing — and an answer with no content,
// or with no count to read, cannot be told apart from one.
func TestSmokeRefusesAnEmptyAnswerToAProbeThatMustFind(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{"a clean answer that found nothing", wireFoundNothing, "count 0"},
		{"no content block at all", `{"jsonrpc":"2.0","id":3,"result":{"content":[]}}`, "no content"},
		{"a block with nothing in it", `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"  "}]}}`, "no content"},
		{"a payload with no count to read", `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"ok"}]}}`, "no readable \"count\""},
		{"no result object", `{"jsonrpc":"2.0","id":3}`, "no result object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := judgeToolAnswer("resolve_term", decodeWire(t, tc.line), true)
			if err == nil {
				t.Fatalf("the smoke accepted %s as an answer", tc.line)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refused, but not for the reason that applies (want %q): %v", tc.want, err)
			}
		})
	}
}

// The control, without which the two tests above would pass for a smoke that
// refuses everything: a real answer to a real probe is accepted, and its count
// reported.
func TestSmokeAcceptsARealAnswer(t *testing.T) {
	text, count, err := judgeToolAnswer("resolve_term", decodeWire(t, wireFoundSomething), true)
	if err != nil {
		t.Fatalf("the real server's answer to resolve_term(AMF) was refused: %v", err)
	}
	if count != 22 || !strings.Contains(text, "Access and Mobility Management Function") {
		t.Errorf("accepted, but read count=%d text=%q", count, text)
	}
	// server_info carries no count and is asked for none.
	info := `{"jsonrpc":"2.0","id":6,"result":{"content":[{"type":"text","text":"{\"fts\": true, \"hnsw\": true}"}]}}`
	if _, _, err := judgeToolAnswer("server_info", decodeWire(t, info), false); err != nil {
		t.Errorf("a server_info answer was refused for lacking a count it never has: %v", err)
	}
}

// A PROBE THE SERVER DOES NOT EXPOSE IS A FAILURE. The loop used to skip it, so a
// server offering none of the four probe tools passed the smoke having called
// nothing.
func TestSmokeRefusesAProbeTheServerDoesNotExpose(t *testing.T) {
	all := []string{"search_spec", "get_spec", "list_specs", "resolve_term", "help", "server_info"}
	if got := missingProbes(all); len(got) != 0 {
		t.Fatalf("every probe is exposed and missingProbes reported %v", got)
	}
	for _, drop := range []string{"search_spec", "list_specs", "resolve_term", "server_info"} {
		names := slices.DeleteFunc(slices.Clone(all), func(n string) bool { return n == drop })
		if got := missingProbes(names); !slices.Equal(got, []string{drop}) {
			t.Errorf("the server does not expose %s and missingProbes reported %v — the smoke would "+
				"skip the probe and pass", drop, got)
		}
	}
}

// ------------------------------------------------------------ retrieval gate

// writeCommittedBaseline puts a baseline where the gate looks for it under c.Root.
func writeCommittedBaseline(t *testing.T, c *Ctx) string {
	t.Helper()
	p := filepath.Join(c.Root, filepath.FromSlash(retrievalBaseline))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := eval.WriteBaseline(p, eval.Baseline{"lexical": {NDCG10: 0.072, Recall10: 0.167, MRR: 0.042}}); err != nil {
		t.Fatal(err)
	}
	return p
}

// THE GATE FAILS WHEN BENCH DOES. bench exits 1 on a tracked metric below
// (baseline - tol) — eval.Judge and cmd/bench's own tests pin that verdict — and
// the only thing this step reads is that exit. Swallowing it, the `|| true` this
// pipeline forbids, would leave the gate measuring and never judging.
func TestTheRetrievalGateFailsWhenBenchReportsARegression(t *testing.T) {
	c, _ := newTestCtx(t)
	writeCommittedBaseline(t, c)

	var ran []Cmd
	regressed := func(cmd Cmd) error {
		ran = append(ran, cmd)
		return &ExecError{Cmd: cmd.Name, ExitCode: 1,
			Tail: "RETRIEVAL REGRESSION vs baseline.json (tol=0.020):\n  lexical  ndcg@10  baseline=0.072 current=0.000 delta=-0.072"}
	}
	err := retrievalGate(c, regressed)
	if err == nil {
		t.Fatal("bench reported a retrieval regression and the smoke passed")
	}
	if !strings.Contains(err.Error(), "RETRIEVAL QUALITY GATE FAILED") || !strings.Contains(err.Error(), "ndcg@10") {
		t.Errorf("the failure does not say it is the retrieval gate, or drops bench's verdict: %v", err)
	}
	if len(ran) != 1 || ran[0].Name != c.bin("bench") {
		t.Fatalf("the gate did not run .local/bin/bench exactly once: %+v", ran)
	}

	// The control: a bench that finds no regression passes the gate.
	if err := retrievalGate(c, func(Cmd) error { return nil }); err != nil {
		t.Errorf("bench found no regression and the gate failed anyway: %v", err)
	}
}

// THE GATE FAILS ON A MISSING BASELINE, AND SEEDS NOTHING. bench's -baseline used
// to write a missing file from the run it was judging and exit 0; the bench.exe in
// .local/bin may still be that one, so this pipeline refuses before launching it.
func TestTheRetrievalGateRefusesAMissingBaseline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, c *Ctx)
	}{
		{"absent", func(*testing.T, *Ctx) {}},
		{"empty", func(t *testing.T, c *Ctx) {
			write(t, filepath.Join(c.Root, filepath.FromSlash(retrievalBaseline)), "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCtx(t)
			tc.setup(t, c)
			calls := 0
			err := retrievalGate(c, func(Cmd) error { calls++; return nil })
			if err == nil {
				t.Fatalf("the retrieval gate passed with an %s baseline: it compared against nothing", tc.name)
			}
			if !strings.Contains(err.Error(), "no baseline") {
				t.Errorf("refused, but without saying the baseline is what is missing: %v", err)
			}
			if calls != 0 {
				t.Errorf("bench was launched %d time(s) with an %s baseline — the bench.exe built before "+
					"eval.Judge seeds it and exits 0", calls, tc.name)
			}
			if st, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(retrievalBaseline))); err == nil && st.Size() > 0 {
				t.Errorf("a baseline appeared under the repository root: the gate seeded one")
			}
		})
	}
}

// THE GATE HOLDS THE CORPUS TO THE COMMITTED BAR, not to some other file. Read as
// flags, the way bench parses them.
func TestTheRetrievalGateRunsBenchAgainstTheCommittedBaseline(t *testing.T) {
	c, _ := newTestCtx(t)
	args := retrievalGateArgs(c)
	if len(args)%2 != 0 {
		t.Fatalf("the bench command line is not flag/value pairs: %v", args)
	}
	got := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		got[args[i]] = args[i+1]
	}
	for flag, want := range map[string]string{
		"-baseline": filepath.Join(c.Root, "docs", "inputs", "eval", "baseline.json"),
		"-set":      filepath.Join(c.Root, "docs", "inputs", "eval", "li_5gc_queries.json"),
		"-db":       c.dataPath("3gpp.duckdb"),
		"-systems":  "lexical",
		"-tol":      "0.02",
		"-json":     retrievalMetricsPath(c),
	} {
		if got[flag] != want {
			t.Errorf("bench %s = %q, want %q", flag, got[flag], want)
		}
	}
}

// THE COMMITTED BASELINE MUST NAME WHAT THE GATE SCORES. eval.Judge refuses a
// scored system the baseline has no entry for, so a baseline edit that dropped it
// would fail the gate at the end of a build; this says so at test time instead.
func TestTheCommittedBaselineCoversWhatTheGateScores(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	base, err := eval.LoadBaseline(filepath.Join(root, filepath.FromSlash(retrievalBaseline)))
	if err != nil {
		t.Fatalf("the committed baseline does not load: %v", err)
	}
	for _, sys := range strings.Split(retrievalSystems, ",") {
		if _, ok := base[sys]; !ok {
			t.Errorf("the gate scores %q and %s has no entry for it — the gate would refuse every build",
				sys, retrievalBaseline)
		}
	}
}

// EDITING A JUDGEMENT OR MOVING THE BAR MUST REPLAY THE GATE, and so must the
// instrument, the verdict and the ranking it measures. A smoke that SKIPs after
// the baseline moved has gated nothing; one that SKIPs after internal/store's
// ranking changed has approved a ranking it never saw.
func TestTheRetrievalGateReplaysWhenItsJudgementMoves(t *testing.T) {
	s := stepSmoke()
	for _, rel := range []string{
		retrievalQuerySet,
		retrievalBaseline,
		"cmd/bench/main.go",
		"internal/eval/gate.go",
		"internal/store/store.go",
	} {
		root := t.TempDir()
		implFixture(t, root, s.Impl)
		before, _, err := implHash(root, s.Impl, s.ExcludeTests)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, filepath.FromSlash(rel)), "changed\n")
		after, _, err := implHash(root, s.Impl, s.ExcludeTests)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Errorf("smoke does NOT watch %s — the retrieval gate would SKIP after it changed", rel)
		}
	}

	// A deleted baseline must stop the plan, not hash as "nothing here".
	root := t.TempDir()
	implFixture(t, root, s.Impl)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(retrievalBaseline))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := implHash(root, s.Impl, s.ExcludeTests); err == nil {
		t.Error("smoke's fingerprint was computed with the baseline deleted: the plan went on without it")
	}

	// The declared paths must exist in the REAL tree, or the fixture above proves
	// a declaration nobody can satisfy.
	real, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	_, per, err := implHash(real, s.Impl, s.ExcludeTests)
	if err != nil {
		t.Fatalf("smoke's Impl does not hash on the real tree: %v", err)
	}
	for _, f := range []string{retrievalQuerySet, retrievalBaseline} {
		if _, ok := per[f]; !ok {
			t.Errorf("%s is not in smoke's recorded implementation", f)
		}
	}

	// The tolerance and the scored systems are decided in internal/goal, which
	// smoke's Impl does not cover: only Extra can make loosening them replay the
	// gate.
	if s.Extra == nil {
		t.Fatal("smoke declares no Extra: the gate's tolerance and systems are in no fingerprint")
	}
	extra, err := s.Extra(nil)
	if err != nil {
		t.Fatal(err)
	}
	if extra["retrieval_tol"] != retrievalTol || extra["retrieval_systems"] != retrievalSystems {
		t.Errorf("smoke's Extra does not carry the gate's tolerance and systems: %v", extra)
	}
}

// `--only smoke` MUST REBUILD THE BENCH IT JUDGES WITH. It force-builds a step's
// Tool deps and nothing else, so without build-go among smoke's Deps it would run
// yesterday's bench.exe — for a fix to eval.Judge, the one that seeded.
func TestOnlySmokeRebuildsTheBench(t *testing.T) {
	ctx, store := newTestCtx(t)
	r, err := NewRunner(Pipeline(), ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if sel := r.WithToolDeps(map[string]bool{"smoke": true}); !sel["build-go"] {
		t.Error("--only smoke does not rebuild bench.exe or server.exe before launching them")
	}
	if !slices.Contains(goBins, "bench") {
		t.Error("build-go no longer builds cmd/bench, and the retrieval gate launches it")
	}
}
