package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/eval"
)

// wireServedInfo is server_info as server-full answered it on 2026-09-11, over the
// real 3gpp.duckdb with etsi.duckdb attached, in the environment servedServerEnv
// builds — the bytes the served gate reads before it scores.
const wireServedInfo = `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\n  \"baseline\": \"latest\",\n  \"embed_floor\": \"\",\n  \"embedding_model_client\": \"38067f8c6efe\",\n  \"embedding_model_db\": \"38067f8c6efe\",\n  \"etsi\": {\n    \"attached\": true,\n    \"embedding_model\": \"38067f8c6efe\",\n    \"embedding_model_ok\": true,\n    \"fts\": true,\n    \"hnsw\": true,\n    \"sparse\": true\n  },\n  \"fts\": true,\n  \"hnsw\": true,\n  \"lexical\": true,\n  \"reason\": \"\",\n  \"reranker\": true,\n  \"reranker_reason\": \"\",\n  \"semantic\": true,\n  \"sparse\": true,\n  \"sparse_model\": \"b13103bce7ae\",\n  \"sparse_reason\": \"\",\n  \"version\": \"dev\"\n}"}]}}`

// servedSet is a two-query judged set: enough for one query to differ between
// arms while the other does not.
var servedSet = eval.Set{
	{ID: "q1", Query: "events the AMF reports over X2", Release: "Rel-17",
		Relevant: []eval.Relevant{{SpecID: "33.128", Clause: "6.2.2.2", Grade: 2}}},
	{ID: "q2", Query: "SMF PDU session establishment xIRI", Release: "Rel-17",
		Relevant: []eval.Relevant{{SpecID: "33.128", Clause: "6.2.3.2", Grade: 1}}},
}

// searchAnswer is a search_spec response on the wire shape internal/mcp builds.
func searchAnswer(mode string, refs ...[2]string) map[string]any {
	type hit struct {
		SpecID string `json:"spec_id"`
		Clause string `json:"clause"`
	}
	payload := map[string]any{"mode": mode, "count": len(refs)}
	hits := []hit{}
	for _, r := range refs {
		hits = append(hits, hit{r[0], r[1]})
	}
	payload["hits"] = hits
	b, _ := json.Marshal(payload)
	return map[string]any{"jsonrpc": "2.0", "id": 9.0, "result": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": string(b)}}}}
}

// armOf names the arm a search_spec call belongs to, from its arguments.
func armOf(args map[string]any) string {
	switch {
	case args["mode"] == "lexical":
		return "lexical"
	case args["rerank"] == true:
		return "rerank"
	default:
		return "hybrid"
	}
}

// fakeServed answers each (arm, query) from a table, and records what it was
// asked.
type fakeServed struct {
	pages map[string]map[string][][2]string // arm -> query -> ranked (spec, clause)
	mode  map[string]string                 // arm -> mode reported (default: what was asked)
	asked []map[string]any
}

func (f *fakeServed) call(tool string, args map[string]any) (map[string]any, error) {
	if tool != "search_spec" {
		return nil, fmt.Errorf("unexpected tool %s", tool)
	}
	f.asked = append(f.asked, args)
	arm := armOf(args)
	mode, _ := args["mode"].(string)
	if m, ok := f.mode[arm]; ok {
		mode = m
	}
	var q string
	for _, s := range servedSet {
		if s.Query == args["query"] {
			q = s.ID
		}
	}
	return searchAnswer(mode, f.pages[arm][q]...), nil
}

// goodServed is a served path where each arm ranks better than the one before:
// the relevant clause at rank 5, 2, then 1 on q1.
func goodServed() *fakeServed {
	noise := [2]string{"ETSI EN 300 392-2", "16.8.6"}
	return &fakeServed{pages: map[string]map[string][][2]string{
		"lexical": {"q1": {noise, noise, noise, noise, {"33.128", "6.2.2.2"}}, "q2": {noise, {"33.128", "6.2.3.2"}}},
		"hybrid":  {"q1": {noise, {"33.128", "6.2.2.2"}}, "q2": {noise, {"33.128", "6.2.3.2"}}},
		"rerank":  {"q1": {{"33.128", "6.2.2.2"}, noise}, "q2": {noise, {"33.128", "6.2.3.2"}}},
	}}
}

// THE GATE SCORES WHAT A CLIENT RECEIVES: one search_spec call per query per arm,
// with the query's release, the arm's mode and rerank flag, and no page size of
// its own — and each arm's metrics come from its own answers.
func TestTheServedGateScoresEachArmThroughSearchSpec(t *testing.T) {
	f := goodServed()
	run, err := scoreServed(servedSet, f.call, true)
	if err != nil {
		t.Fatalf("a served path that ranks well was refused: %v", err)
	}
	if want := len(servedArms) * len(servedSet); len(f.asked) != want {
		t.Fatalf("the gate made %d search_spec calls, want %d (every arm, every query)", len(f.asked), want)
	}
	for _, a := range f.asked {
		if a["release"] != "Rel-17" {
			t.Errorf("the judged query's release was not passed: %v", a)
		}
		if _, ok := a["top_k"]; ok {
			t.Errorf("the gate asked for its own page size — a client's default page is what is served: %v", a)
		}
		if a["mode"] != "lexical" && a["mode"] != "hybrid" {
			t.Errorf("an arm asked for mode %v", a["mode"])
		}
	}
	// q1's relevant clause sits at rank 5, 2 and 1: MRR 0.2, 0.5, 1 on q1, and 0.5
	// on q2 for every arm.
	for arm, want := range map[string]float64{"lexical": (0.2 + 0.5) / 2, "hybrid": (0.5 + 0.5) / 2, "rerank": (1 + 0.5) / 2} {
		if got := run.Metrics[arm].MRR; fmt.Sprintf("%.4f", got) != fmt.Sprintf("%.4f", want) {
			t.Errorf("%s MRR@10 = %.4f, want %.4f — the arm was not scored from its own answers", arm, got, want)
		}
	}
}

// A MODE THE ARM DID NOT ASK FOR IS REFUSED. Search degrades semantic to lexical
// on purpose and says so only in `mode` and `mode_degraded`; scoring that answer
// would record BM25 under the hybrid name — the reason the semantic arms were
// kept out of the lexical gate.
func TestTheServedGateRefusesADegradedMode(t *testing.T) {
	f := goodServed()
	f.mode = map[string]string{"hybrid": "lexical"}
	if _, err := scoreServed(servedSet, f.call, true); err == nil || !strings.Contains(err.Error(), `was served "lexical"`) {
		t.Fatalf("a hybrid arm answered in lexical mode and the gate scored it: %v", err)
	}
	// mode_degraded alone is enough, whatever `mode` says.
	m := searchAnswer("hybrid", [2]string{"33.128", "6.2.2.2"})
	var p map[string]any
	blocks := m["result"].(map[string]any)["content"].([]any)
	_ = json.Unmarshal([]byte(blocks[0].(map[string]any)["text"].(string)), &p)
	p["mode_degraded"] = `requested "hybrid", served "lexical"`
	b, _ := json.Marshal(p)
	blocks[0].(map[string]any)["text"] = string(b)
	if _, err := readServedHits(servedArms[1], servedSet[0], m); err == nil {
		t.Fatal("an answer carrying mode_degraded was scored")
	}
}

// A RERANK ARM THAT DID NOT RERANK IS REFUSED. Engine.rerank keeps the fused
// order when the cross-encoder fails, and server_info says reranker=true either
// way; an arm identical to hybrid on every query is that failure, scored twice.
func TestTheServedGateRefusesARerankArmThatDidNotRerank(t *testing.T) {
	f := goodServed()
	f.pages["rerank"] = f.pages["hybrid"]
	_, err := scoreServed(servedSet, f.call, true)
	if err == nil || !strings.Contains(err.Error(), "cross-encoder did not reorder") {
		t.Fatalf("the rerank arm returned the hybrid pages and the gate scored it: %v", err)
	}
	// The control: ONE reordered page is a reranker that acted (goodServed differs
	// on q1 only).
	if _, err := scoreServed(servedSet, goodServed().call, true); err != nil {
		t.Errorf("a reranker that reordered one page was refused: %v", err)
	}
}

// A PAGE THE ETSI HALF DID NOT REACH IS REFUSED when etsi.duckdb is attached.
// search_spec drops a failing ETSI search silently, and a 3GPP-only page scores
// HIGHER on this 3GPP-judged set — so the metrics would have approved it.
func TestTheServedGateRefusesAnAnswerTheETSIHalfDroppedOutOf(t *testing.T) {
	f := goodServed()
	// The shape measured under DUCKDB_MEMORY_LIMIT=4GB: the ETSI half gone, and the
	// relevant clause promoted because the noise left with it.
	f.pages["hybrid"]["q2"] = [][2]string{{"33.128", "6.2.3.2"}, {"33.127", "6.2.3.3"}}
	_, err := scoreServed(servedSet, f.call, true)
	if !errors.Is(err, errETSIDropped) || !strings.Contains(err.Error(), "hybrid") || !strings.Contains(err.Error(), "q2") {
		t.Fatalf("a hybrid page with no ETSI hit was scored with etsi.duckdb attached: %v", err)
	}
	// Without the ETSI half attached, a 3GPP-only page is the product.
	if _, err := scoreServed(servedSet, f.call, false); err != nil {
		t.Errorf("a 3GPP-only page was refused with no ETSI half attached: %v", err)
	}
}

// AN EMPTY PAGE, AN isError, OR A FAILED CALL IS NOT A SCORE OF ZERO. The first
// two are judgeToolAnswer's verdicts; the third must stop the gate, not score the
// arm on the queries that did answer.
func TestTheServedGateRefusesAnAnswerItCannotScore(t *testing.T) {
	for name, call := range map[string]toolCaller{
		"empty page": func(string, map[string]any) (map[string]any, error) { return searchAnswer("lexical"), nil },
		"isError": func(string, map[string]any) (map[string]any, error) {
			return decodeWire(t, wireToolFailed), nil
		},
		"transport": func(string, map[string]any) (map[string]any, error) { return nil, fmt.Errorf("EOF") },
	} {
		if _, err := scoreServed(servedSet, call, true); err == nil {
			t.Errorf("%s: the gate scored an answer it could not read", name)
		}
	}
	if _, err := scoreServed(nil, goodServed().call, true); err == nil {
		t.Error("an empty judged set was scored — nothing scored cannot regress")
	}
}

// A SERVER THAT CANNOT SERVE THE ARMS IS REFUSED BEFORE SCORING, with its reason.
func TestTheServedGateRefusesAServerWithoutItsArms(t *testing.T) {
	if _, err := requireServedArms(decodeWire(t, wireServedInfo), true); err != nil {
		t.Fatalf("the real server-full's server_info was refused: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no reranker", `\"reranker\": true,\n  \"reranker_reason\": \"\"`,
			`\"reranker\": false,\n  \"reranker_reason\": \"model.onnx is missing at X\"`, "model.onnx is missing at X"},
		{"no embedder", `\"semantic\": true`, `\"semantic\": false`, "semantic=false"},
		{"ETSI served lexically", `\"embedding_model_ok\": true`, `\"embedding_model_ok\": false`, "ETSI half"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := strings.Replace(wireServedInfo, tc.from, tc.to, 1)
			if line == wireServedInfo {
				t.Fatalf("the fixture edit %q did not apply", tc.from)
			}
			_, err := requireServedArms(decodeWire(t, line), true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a server with %s was accepted, or refused without saying why (want %q): %v", tc.name, tc.want, err)
			}
		})
	}
}

// NO BAR, NO GATE — and no bar is ever written by the gate. Missing, empty, an
// arm with no entry, and an entry with a metric missing (which decodes as a 0
// bar) are each refused before the server is started.
func TestTheServedGateRefusesAMissingOrIncompleteBaseline(t *testing.T) {
	full := `{"lexical":{"ndcg@10":0.1,"recall@10":0.1,"mrr@10":0.1,"success@1":0},` +
		`"hybrid":{"ndcg@10":0.1,"recall@10":0.1,"mrr@10":0.1,"success@1":0},` +
		`"rerank":{"ndcg@10":0.2,"recall@10":0.2,"mrr@10":0.4,"success@1":0}}`
	for _, tc := range []struct{ name, body, want string }{
		{"absent", "", "no baseline"},
		{"empty", "", "no baseline"},
		{"no rerank arm", `{"lexical":{"ndcg@10":0.1,"recall@10":0.1,"mrr@10":0.1,"success@1":0},` +
			`"hybrid":{"ndcg@10":0.1,"recall@10":0.1,"mrr@10":0.1,"success@1":0}}`, "rerank"},
		{"a metric missing", strings.Replace(full, `"mrr@10":0.4,`, "", 1), "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "served_baseline.json")
			if tc.name != "absent" {
				write(t, p, tc.body)
			}
			err := requireServedBaseline(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the served gate accepted a %s baseline (want %q): %v", tc.name, tc.want, err)
			}
			if tc.name == "absent" {
				if _, err := os.Stat(p); err == nil {
					t.Error("the gate seeded the baseline it was refused for lacking")
				}
			}
		})
	}
	p := filepath.Join(t.TempDir(), "served_baseline.json")
	write(t, p, full)
	if err := requireServedBaseline(p); err != nil {
		t.Errorf("a complete baseline was refused: %v", err)
	}
}

// THE VERDICT: a tracked metric below (bar - tol) on any arm fails, naming the
// arm; the bar itself and anything above it passes; the measured metrics are left
// as a candidate, never as the bar.
func TestTheServedGateFailsOnARegressionAndOnlyThen(t *testing.T) {
	c, _ := newTestCtx(t)
	bar := eval.Baseline{
		"lexical": {NDCG10: 0.05, Recall10: 0.1, MRR: 0.05},
		"hybrid":  {NDCG10: 0.10, Recall10: 0.4, MRR: 0.14},
		"rerank":  {NDCG10: 0.20, Recall10: 0.4, MRR: 0.41},
	}
	base := filepath.Join(c.Root, filepath.FromSlash(servedBaseline))
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := eval.WriteBaseline(base, bar); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(base)

	if err := judgeServed(c, &servedRun{Metrics: bar}); err != nil {
		t.Fatalf("metrics equal to the bar failed the gate: %v", err)
	}
	better := eval.Baseline{"lexical": bar["lexical"], "hybrid": bar["hybrid"],
		"rerank": {NDCG10: 0.5, Recall10: 0.9, MRR: 0.9, Success1: 0.5}}
	if err := judgeServed(c, &servedRun{Metrics: better}); err != nil {
		t.Errorf("an improvement failed the gate — it is one-sided: %v", err)
	}
	// The reranker regressing to the hybrid order: rerank's MRR falls to hybrid's.
	worse := eval.Baseline{"lexical": bar["lexical"], "hybrid": bar["hybrid"],
		"rerank": {NDCG10: 0.10, Recall10: 0.4, MRR: 0.14}}
	err := judgeServed(c, &servedRun{Metrics: worse})
	if err == nil {
		t.Fatal("the rerank arm fell to the hybrid ranking and the served gate passed")
	}
	if !strings.Contains(err.Error(), "SERVED RETRIEVAL QUALITY GATE FAILED") || !strings.Contains(err.Error(), "rerank") ||
		!strings.Contains(err.Error(), "mrr@10") || strings.Contains(err.Error(), "hybrid   ") {
		t.Errorf("the failure does not name the arm and metric that regressed, and only those: %v", err)
	}
	after, _ := os.ReadFile(base)
	if string(before) != string(after) {
		t.Error("judging rewrote the committed baseline")
	}
	cand, err := eval.LoadBaseline(servedMetricsPath(c))
	if err != nil || cand["rerank"].MRR != 0.14 {
		t.Errorf("the candidate baseline is not what this run measured: %v %v", cand, err)
	}
}

// A server-full THIS TREE DID NOT BUILD IS NOT SCORED. build-serve is Optional:
// the runner continues past its failure and the previous binary stays in place.
func TestTheServedGateRefusesAServerFullBuildServeDidNotVouchFor(t *testing.T) {
	c, st := newTestCtx(t)
	if err := buildServeSucceeded(c); err == nil || !strings.Contains(err.Error(), "never run") {
		t.Errorf("no build-serve record, and the gate went on: %v", err)
	}
	if err := st.Save(&Record{Step: "build-serve", Status: StatusFailed}); err != nil {
		t.Fatal(err)
	}
	write(t, c.bin("server-full"), "an old binary")
	if err := buildServeSucceeded(c); err == nil || !strings.Contains(err.Error(), string(StatusFailed)) {
		t.Errorf("build-serve failed and the gate would score the binary an earlier tree left: %v", err)
	}
	if err := st.Save(&Record{Step: "build-serve", Status: StatusSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := buildServeSucceeded(c); err != nil {
		t.Errorf("build-serve succeeded and its binary is there, and the gate refused: %v", err)
	}
	if err := os.Remove(c.bin("server-full")); err != nil {
		t.Fatal(err)
	}
	if err := buildServeSucceeded(c); err == nil {
		t.Error("server-full is missing and the gate went on")
	}
}

// THE SERVER IS STARTED THE WAY THE PRODUCT IS: both ONNX Runtimes, each on its
// own variable and its own file; the models the image carries; the knobs that
// would change the ranking pinned to the image's (unset) values so the operator's
// shell cannot leak one in; and the latency budget off.
func TestTheServedServerEnvironment(t *testing.T) {
	c, _ := newTestCtx(t)
	ort := filepath.Join(t.TempDir(), "ort", "lib")
	c.Config["ort_dir"] = ort
	env := map[string]string{}
	for _, kv := range servedServerEnv(c) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v // a later duplicate wins, as in exec.Cmd
	}
	for k, want := range map[string]string{
		"ORT_DYLIB_PATH":                  filepath.Join(ort, ortLibName()),
		"ONNXRUNTIME_SHARED_LIBRARY_PATH": c.dataPath("models", "onnxruntime", "lib", ortLibName()),
		"EMBED_MODEL":                     sparseModelName,
		"EMBED_MODEL_DIR":                 c.dataPath("models", sparseModelName),
		"BGE_RERANKER_DIR":                c.dataPath("models", rerankModelName),
		"SEARCH_BUDGET":                   "0",
		"DUCKDB_MEMORY_LIMIT":             servedMemoryLimit,
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	for _, k := range []string{"RERANK_ALL", "RERANK_WINDOW", "RERANKER", "EMBEDDER", "EMBED_MODELS_CONFIG"} {
		if v, ok := env[k]; !ok || v != "" {
			t.Errorf("%s is not pinned to the image's unset value (%q, present=%v): an exported %s would "+
				"change the ranking this gate records", k, v, ok, k)
		}
	}
	if got := servedRuntimeInputs(c); !slices.Equal(got, []string{filepath.Join(ort, ortLibName())}) {
		t.Errorf("the Rust side's runtime is not a smoke input: %v", got)
	}
}

// --------------------------------------------------------------- declaration

// THE SERVED GATE'S DETERMINANTS REPLAY IT: its bar, the query embedder crate
// build-serve compiles (a Tool dep, which invalidates no consumer), and the knobs
// that decide what it scores.
func TestTheServedGateReplaysWhenItsJudgementMoves(t *testing.T) {
	s := stepSmoke()
	for _, rel := range []string{servedBaseline, "rust/embed-core/src/lib.rs", "rust/embed-core/Cargo.lock",
		"internal/rerank/rerank_onnx.go", "internal/search/search.go"} {
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
			t.Errorf("smoke does NOT watch %s — the served gate would SKIP after it changed", rel)
		}
	}
	// Every crate path publish declares for the image's query embedder is one the
	// served gate's server loads too.
	for _, p := range stepPublish().Impl {
		if strings.HasPrefix(p, "rust/embed-core") && !slices.Contains(s.Impl, p) {
			t.Errorf("publish ships %s and smoke, which now runs the embedder built from it, does not fingerprint it", p)
		}
	}
	extra, err := s.Extra(nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"served_arms": "lexical,hybrid,rerank", "served_tol": servedTol,
		"served_search_budget": servedSearchBudget, "served_memory_limit": servedMemoryLimit} {
		if extra[k] != want {
			t.Errorf("smoke's Extra[%s] = %q, want %q: loosening it would not replay the gate", k, extra[k], want)
		}
	}
	real, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, per, err := implHash(real, s.Impl, s.ExcludeTests); err != nil {
		t.Fatalf("smoke's Impl does not hash on the real tree: %v", err)
	} else if _, ok := per[servedBaseline]; !ok {
		t.Errorf("%s is not in smoke's recorded implementation", servedBaseline)
	}
}

// THE MODELS AND RUNTIMES ARE INPUTS, FILE BY FILE. A new reranker export is a new
// ranking; a directory would fingerprint as the constant "dir".
func TestSmokeFingerprintsTheModelsTheServedGateLoads(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["ort_dir"] = filepath.Join(c.Root, "ortlib")
	var want []string
	for _, d := range imageModelDirs() {
		p := c.dataPath("models", d, "model.onnx")
		write(t, p, "weights")
		want = append(want, p)
	}
	in, err := stepSmoke().Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(want, filepath.Join(c.Root, "ortlib", ortLibName())) {
		if !slices.Contains(in, p) {
			t.Errorf("smoke does not fingerprint %s, which the served gate's server loads: %v", p, in)
		}
	}
	for _, p := range in {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			t.Errorf("smoke declares the directory %s as an input: it fingerprints as \"dir\"", p)
		}
	}
}

// `--only smoke` MUST REBUILD THE server-full IT SCORES, as it rebuilds bench.
func TestOnlySmokeRebuildsServerFull(t *testing.T) {
	ctx, store := newTestCtx(t)
	r, err := NewRunner(Pipeline(), ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if sel := r.WithToolDeps(map[string]bool{"smoke": true}); !sel["build-serve"] {
		t.Error("--only smoke would score yesterday's server-full.exe")
	}
}

// THE COMMITTED SERVED BAR COVERS EVERY ARM, COMPLETELY. A missing arm or metric
// would fail the gate at the end of a build; this says so at test time.
func TestTheCommittedServedBaselineCoversEveryArm(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := requireServedBaseline(filepath.Join(root, filepath.FromSlash(servedBaseline))); err != nil {
		t.Fatal(err)
	}
}
