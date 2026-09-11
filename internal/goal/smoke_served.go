package goal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/eval"
)

// ------------------------------------------------- served-path retrieval gate
//
// THE GATE ON WHAT THE USER RECEIVES. The retrieval gate in smoke_gate.go scores
// -systems lexical through bench.exe, the only system build-go's lexical bench can
// score, and the log said so. That left the path the product actually serves —
// hybrid fusion of BM25, dense HNSW and sparse, plus the cross-encoder — measured
// by nothing. Measured 2026-09-10 through bench (-tags onnx,embed_ffi, 3GPP half
// only): lexical nDCG@10 0.072, hybrid 0.101, hybrid+rerank 0.207 / MRR 0.417 —
// the served ranking is three times the lexical one, and a regression of the
// reranker, the embedder, the ONNX Runtime or the fusion passed every gate.
//
// WHY IT DRIVES server-full.exe AND NOT A SEMANTIC BENCH. A bench built with the
// right tags would score store+search, not the product: search_spec adds the
// normative doc-type default, the page size a client gets by default, and the
// server's own startup guards (the embedding identity check refuses to start on a
// mismatch). A gate that measures a proxy of that path approves the proxy. So this
// one starts the binary build-serve builds — onnx + embed_ffi, the tags the image
// compiles — with the environment .mcp.json and scripts/local/prove-serving.sh
// give it, and asks search_spec, over JSON-RPC, exactly what a client asks. Its
// first run found what no other gate could: search_spec(rerank=true) PANICKED in
// the stdio worker on a passage with an empty body and never answered (fixed in
// internal/rerank, forTokenizer).
//
// THE ONE BOUND: THE 3GPP HALF ALONE (--etsi-db off), measured, not assumed. Over
// both halves, on this 28 GB machine, 2026-09-11:
//
//	image defaults (16GB DuckDB limit per store)  39.8 GB committed on the FIRST
//	                                              hybrid query, unanswered after
//	                                              3 min 30 — killed
//	DUCKDB_MEMORY_LIMIT=6GB                       27.6 GB committed, 33 min without
//	                                              finishing the rerank arm — killed
//	DUCKDB_MEMORY_LIMIT=4GB                       the ETSI half FAILS after its
//	                                              first semantic query (its index
//	                                              no longer fits) and search_spec
//	                                              drops it without a word
//
// Each half is its own engine — its own HNSW (3.37 and 3.68 GB), its own hybrid
// pass and its own cross-encoder window per query — so dropping one halves the
// work and the index memory. What is lost is the federation MERGE, and on this
// judged set it is the part that cannot be judged: every judgement is a 3GPP
// clause, the ETSI hits are unjudged by construction, and the merge interleaves
// them 1:1 (5 of every 10 hits on every page measured with both halves). The
// embedder, the reranker, both ONNX Runtimes, the sparse arm, the fusion and the
// handler — the regressions this gate exists for — are all on the path it keeps.
//
// WHAT IT REFUSES BEFORE IT SCORES, because each is a way to record one ranking
// under another's name — the defect that kept the semantic arms out of the lexical
// gate:
//
//   - a server_info that does not say semantic=true and reranker=true (with the
//     reason it gives when it does not);
//   - an answer whose `mode` is not the one the arm asked for, or that carries
//     `mode_degraded` — Search degrades semantic to lexical on purpose, and says so
//     only there;
//   - a rerank arm that returned the hybrid order for EVERY query. Engine.rerank
//     keeps the RRF order when the cross-encoder fails, silently: this is the one
//     place the difference is observable.
//
// Baseline missing, empty or incomplete = FAILURE, never seeded: eval.Judge, the
// verdict the lexical gate and bench share.
const (
	// servedBaseline is the committed bar, per arm. It is a file of its own, not
	// three more keys in retrievalBaseline: that file is also bench's -baseline, and
	// bench scores the engine directly, without federation and with its own page
	// size — the same key holding numbers from two instruments would fail one of
	// them on every run.
	servedBaseline = "docs/inputs/eval/served_baseline.json"
	// servedTol is the absolute drop a tracked metric may take: the 0.02 of the
	// lexical gate and bench. On the two scored queries (servedQueryIDs) it reads as
	// follows. A judged clause that LEAVES the page costs recall@10 at least 0.25.
	// The first judged hit slipping one rank in one query costs MRR@10 0.25 from
	// rank 1, 0.083 from 2, 0.042 from 3 and 0.025 from 4 — all fail; from 5 to 6 it
	// costs 0.017 and passes, as does any shuffle below. It absorbs float noise
	// that does not reorder a judged hit, and nothing else.
	servedTol = "0.02"
	// servedSearchBudget disables the per-request budget in the gate's server. The
	// budget is a LATENCY guard: when it expires, Search skips the embed, the sparse
	// and the rerank passes and returns the fusion it has, without saying so. Under
	// the served default (20 s) the verdict would then depend on how loaded this
	// machine was — the first cold query took 22 s on the lexical arm alone — and a
	// flaky gate is one that gets skipped. The budget's own behaviour is pinned by
	// internal/search's tests; this gate measures the ranking.
	servedSearchBudget = "0"
	// servedCallTimeout bounds one tools/call. The first semantic query pays the
	// embedder's session start and the cold HNSW; see the measurements on
	// stepSmoke.
	servedCallTimeout = 10 * time.Minute
)

// servedArm is one retrieval configuration scored through search_spec.
type servedArm struct {
	Key    string // the baseline key
	Mode   string // search_spec's `mode`, and the mode the answer must report
	Rerank bool   // search_spec's `rerank`
}

// servedArms are scored in this order. The first two exist so a failure can be
// located: rerank down and hybrid steady is the cross-encoder; hybrid down and
// lexical steady is the embedder, the vectors or the fusion.
var servedArms = []servedArm{
	{Key: "lexical", Mode: "lexical"},
	{Key: "hybrid", Mode: "hybrid"},
	{Key: "rerank", Mode: "hybrid", Rerank: true},
}

// servedQueryIDs are the judged queries the served gate scores: the two of the
// six on which some arm ranks a judged clause at all.
//
// THE SECOND BOUND, ON TIME, AND ALSO MEASURED. All six through all three arms,
// server-full on the 3GPP half, 2026-09-11 (eight agents sharing the machine):
//
//	arm       secs   nDCG@10 per query (amf-registration, amf-location-update,
//	                 amf-section, smf-pdu-session, lexical-clause-ref, udm-events)
//	lexical      9   0.00 0.00 0.00 0.63 0.00 0.00
//	hybrid     551   0.51 0.00 0.00 0.50 0.00 0.00
//	rerank     919   0.63 0.00 0.00 0.63 0.00 0.00
//
// 25 min 15 end to end, 80-120 s per semantic call. Four queries score 0 on EVERY
// arm, and so on every tracked metric: a regression gate is one-sided, a metric
// at 0 cannot fall, and those four were two thirds of the cost of a gate that
// could fail on none of them. The two kept carry every non-zero number above —
// and are held more tightly for it: one rank lost in either moves a two-query
// mean twice as far as a six-query one.
//
// The set is a list, not "whatever scored": the baseline was measured on exactly
// these, the ids fold into smoke's Extra, and a judged query renamed away fails
// the gate (servedSubset) instead of shrinking it. When a ranking change makes
// one of the other four non-zero, add it here and re-measure; the candidate
// baseline the gate writes is how.
var servedQueryIDs = []string{"amf-registration", "smf-pdu-session"}

// servedSubset picks servedQueryIDs out of the judged set, in that order.
func servedSubset(set eval.Set) (eval.Set, error) {
	byID := map[string]eval.Query{}
	for _, q := range set {
		byID[q.ID] = q
	}
	out := make(eval.Set, 0, len(servedQueryIDs))
	for _, id := range servedQueryIDs {
		q, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("the served gate scores the judged query %q and %s has no such id — it was "+
				"renamed or removed; the baseline was measured on it", id, retrievalQuerySet)
		}
		out = append(out, q)
	}
	return out, nil
}

func servedArmKeys() string {
	keys := make([]string, 0, len(servedArms))
	for _, a := range servedArms {
		keys = append(keys, a.Key)
	}
	return strings.Join(keys, ",")
}

// servedArgs is the search_spec call for one judged query under one arm: what a
// client sends, and nothing a client would not. No top_k — the default page is
// what a client receives — and the query's release, as the judgement was made
// against it.
func servedArgs(q eval.Query, a servedArm) map[string]any {
	args := map[string]any{"query": q.Query, "mode": a.Mode}
	if q.Release != "" {
		args["release"] = q.Release
	}
	if a.Rerank {
		args["rerank"] = true
	}
	return args
}

// servedPinnedEnv are the knobs that would change the ranking this gate records,
// set to the value the IMAGE runs with (unset), so an operator's shell cannot
// leak one in: RERANK_ALL=1 would rerank the hybrid arm too, RERANK_WINDOW would
// move what the cross-encoder sees, EMBEDDER/RERANKER=off would switch an arm off
// (server_info would catch that one, the others it would not), and
// EMBED_MODELS_CONFIG would swap the registry the query embedder resolves.
var servedPinnedEnv = []string{"RERANK_ALL", "RERANK_WINDOW", "EMBEDDER", "RERANKER", "EMBED_MODELS_CONFIG"}

// servedMemoryLimit is the DUCKDB_MEMORY_LIMIT the gate's server runs with. The
// image leaves it unset, and `serve` then applies store.ServeMemoryLimit — the
// same 6GB, since the measurement below is what set it; before that the unset
// value meant the writer's 16GB per corpus (39.8 GB committed over both halves,
// above). Setting it here keeps the gate pinned whatever that default becomes.
// The limit
// caps the buffer pool, and the frozen HNSW index lives IN that pool
// (duckdb_memory(), tag ART_INDEX, after one k-NN: 3.37 GB on the 3GPP half, with
// 5.5 GB of table pages cached beside it); 6GB leaves the 3GPP index 2.6 GB to
// work in, where 4GB left the ETSI one 0.3 GB and broke it. It changes what DuckDB
// caches, not what a query returns: a query that runs out of room fails — and an
// arm that fails is what the mode check and the rerank check are for. An
// operator's own DUCKDB_MEMORY_LIMIT is overridden for the same reason the knobs
// above are.
const servedMemoryLimit = "6GB"

// servedServerEnv is the environment server-full is started with. Two ONNX
// Runtimes, and they are not interchangeable (scripts/local/prove-serving.sh has
// the measurement): ORT_DYLIB_PATH is the Rust embed-core crate's, pointed at the
// runtime the embed steps use (gpuEnv); ONNXRUNTIME_SHARED_LIBRARY_PATH is the Go
// binding's, for the cross-encoder, pointed at the pinned runtime the image
// stages under data/models/onnxruntime.
func servedServerEnv(c *Ctx) []string {
	env := gpuEnv(c)
	for _, k := range servedPinnedEnv {
		env = append(env, k+"=")
	}
	return append(append(env,
		"EMBED_MODEL="+sparseModelName,
		"EMBED_MODEL_DIR="+c.dataPath("models", sparseModelName),
		"BGE_RERANKER_DIR="+c.dataPath("models", rerankModelName),
		"ONNXRUNTIME_SHARED_LIBRARY_PATH="+c.dataPath("models", "onnxruntime", "lib", ortLibName()),
		"SEARCH_BUDGET="+servedSearchBudget,
		"DUCKDB_MEMORY_LIMIT="+servedMemoryLimit,
		"MCP3GPP_NO_UPDATE=1",
	), servedLoaderEnv(c)...)
}

// servedLoaderEnv points the dynamic loader at the embed-core library build-serve
// compiles (review of #340). On Windows build-serve stages embed_core.dll beside
// server-full, where the loader looks first; elsewhere serveDLLs stages nothing,
// and the binary's own run path names rust/embed-core/target/release, which
// build-serve does not build into — so without this, server-full on Linux or
// macOS would not start.
func servedLoaderEnv(c *Ctx) []string { return servedLoaderEnvFor(c, runtime.GOOS) }

func servedLoaderEnvFor(c *Ctx, goos string) []string {
	var key string
	switch goos {
	case "windows":
		return nil
	case "darwin":
		key = "DYLD_LIBRARY_PATH"
	default:
		key = "LD_LIBRARY_PATH"
	}
	dir := filepath.Join(c.Local, "cargo-target-embedcore", "release")
	if prev := os.Getenv(key); prev != "" {
		dir += string(os.PathListSeparator) + prev
	}
	return []string{key + "=" + dir}
}

// servedRuntimeInputs are the files the served server loads that no Impl can
// see: the Rust side's ONNX Runtime, which gpuEnv finds under .local/toolchain.
// The models and the Go side's runtime are imageModelDirs(), listed by the caller.
func servedRuntimeInputs(c *Ctx) []string {
	for _, kv := range gpuEnv(c) {
		if p, ok := strings.CutPrefix(kv, "ORT_DYLIB_PATH="); ok {
			return []string{p}
		}
	}
	return nil
}

// servedServerArgs is the command line: the 3GPP corpus, and the ETSI half
// declined explicitly — an empty --etsi-db would attach the etsi.duckdb beside it.
func servedServerArgs(c *Ctx) []string {
	return []string{"serve", "--db", c.dataPath("3gpp.duckdb"), "--etsi-db", "off"}
}

// servedMetricsPath is where the gate leaves what it measured: a CANDIDATE
// baseline, as retrievalMetricsPath is for the lexical gate.
func servedMetricsPath(c *Ctx) string { return c.statePath("served-retrieval-metrics.json") }

// toolCaller asks the served server one tools/call and returns the raw response.
type toolCaller func(tool string, args map[string]any) (map[string]any, error)

// servedHits is what one search_spec answer says, as far as the gate reads it.
type servedHits struct {
	Mode         string `json:"mode"`
	ModeDegraded string `json:"mode_degraded"`
	Hits         []struct {
		SpecID   string `json:"spec_id"`
		Clause   string `json:"clause"`
		Citation struct {
			SpecID  string `json:"spec_id"`
			Version string `json:"version"`
			URL     string `json:"url"`
		} `json:"citation"`
	} `json:"hits"`
}

// readServedHits turns one search_spec response into ranked refs, refusing an
// answer that is not the ranking the arm asked for.
func readServedHits(a servedArm, q eval.Query, m map[string]any) ([]eval.Ref, error) {
	text, _, err := judgeToolAnswer("search_spec", m, true)
	if err != nil {
		return nil, err
	}
	var h servedHits
	if err := json.Unmarshal([]byte(text), &h); err != nil {
		return nil, fmt.Errorf("search_spec answered %s/%s with a payload the gate cannot read: %w", a.Key, q.ID, err)
	}
	if h.Mode != a.Mode || h.ModeDegraded != "" {
		return nil, fmt.Errorf("the %s arm asked search_spec for mode %q on %q and was served %q (%s) — scoring "+
			"it would record one ranking under another's name", a.Key, a.Mode, q.ID, h.Mode, h.ModeDegraded)
	}
	refs := make([]eval.Ref, len(h.Hits))
	for i, x := range h.Hits {
		// CITE OR DO NOT ANSWER (CLAUDE.md §1), checked on what is scored (review of
		// #340): a hit the client cannot trace to a spec version and its URL is not
		// a result, however well it ranks.
		if x.Citation.SpecID != x.SpecID || x.Citation.Version == "" || x.Citation.URL == "" {
			return nil, fmt.Errorf("the %s arm's hit %d for %q (%s %s) carries no usable citation "+
				"(spec_id %q, version %q, url %q)", a.Key, i+1, q.ID, x.SpecID, x.Clause,
				x.Citation.SpecID, x.Citation.Version, x.Citation.URL)
		}
		refs[i] = eval.Ref{SpecID: x.SpecID, Clause: x.Clause}
	}
	return refs, nil
}

// servedRun is one scoring pass: the macro metrics per arm, and what each arm
// ranked per query (in set order), for the checks that compare arms.
type servedRun struct {
	Metrics eval.Baseline
	Ranked  map[string][][]eval.Ref
	Per     map[string][]eval.Metrics
	Secs    map[string]float64
}

// scoreServed scores every arm over the set through call. It is the whole of the
// measurement, with the server abstracted away, so a test can drive it with
// canned answers.
// logf, when not nil, receives one line per call — the arm, the query, the
// latency and the ETSI share of the page — so a slow or stuck call is visible in
// the step log while the gate runs, not only in its verdict.
func scoreServed(set eval.Set, call toolCaller, logf func(string, ...any)) (*servedRun, error) {
	if len(set) == 0 {
		return nil, errors.New("the judged query set is empty — there is nothing to score, and nothing scored " +
			"cannot regress")
	}
	run := &servedRun{Metrics: eval.Baseline{}, Ranked: map[string][][]eval.Ref{},
		Per: map[string][]eval.Metrics{}, Secs: map[string]float64{}}
	for _, a := range servedArms {
		start := time.Now()
		rank := func(_ context.Context, q eval.Query) ([]eval.Ref, error) {
			t0 := time.Now()
			m, err := call("search_spec", servedArgs(q, a))
			if err != nil {
				return nil, fmt.Errorf("%s arm, query %s: %w", a.Key, q.ID, err)
			}
			refs, err := readServedHits(a, q, m)
			if err != nil {
				return nil, err
			}
			run.Ranked[a.Key] = append(run.Ranked[a.Key], refs)
			if logf != nil {
				logf("  %-8s %-22s %6.1fs  %d hit(s)", a.Key, q.ID, time.Since(t0).Seconds(), len(refs))
			}
			return refs, nil
		}
		per, avg, err := eval.Run(context.Background(), set, rank)
		if err != nil {
			return nil, err
		}
		run.Metrics[a.Key] = avg
		run.Per[a.Key] = per
		run.Secs[a.Key] = time.Since(start).Seconds()
	}
	if err := rerankActed(set, run.Ranked["hybrid"], run.Ranked["rerank"]); err != nil {
		return nil, err
	}
	return run, nil
}

// rerankActed refuses a rerank arm that returned the hybrid order for every
// query. Engine.rerank falls back to the RRF order on any cross-encoder error,
// and server_info reports the reranker enabled whether or not its passes succeed,
// so without this the rerank row could be the hybrid ranking scored twice. ONE
// reordered query is enough: an identical page is legitimate for a query whose
// window the cross-encoder happens to agree with.
func rerankActed(set eval.Set, hybrid, rerank [][]eval.Ref) error {
	if len(hybrid) != len(set) || len(rerank) != len(set) {
		return fmt.Errorf("the arms did not rank every query (hybrid %d, rerank %d, set %d)", len(hybrid), len(rerank), len(set))
	}
	for i := range set {
		if !sameRefs(hybrid[i], rerank[i]) {
			return nil
		}
	}
	return fmt.Errorf("the rerank arm returned the hybrid order for all %d queries — the cross-encoder did not "+
		"reorder a single page. Engine.rerank keeps the fused order when it fails, silently, so this is the "+
		"hybrid ranking scored under the rerank name", len(set))
}

func sameRefs(a, b []eval.Ref) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// servedInfo is the part of server_info the gate requires before it scores.
type servedInfo struct {
	Semantic       bool   `json:"semantic"`
	Reranker       bool   `json:"reranker"`
	RerankerReason string `json:"reranker_reason"`
	Hnsw           bool   `json:"hnsw"`
	Sparse         bool   `json:"sparse"`
	SparseReason   string `json:"sparse_reason"`
	Model          string `json:"embedding_model_db"`
	Etsi           struct {
		Attached bool `json:"attached"`
	} `json:"etsi"`
}

// requireServedArms refuses a server that cannot serve the arms the gate scores.
// Every refusal names what the server said, because "reranker false" with its
// reason is a diagnosis and "gate failed" is not.
func requireServedArms(m map[string]any) (servedInfo, error) {
	var si servedInfo
	text, _, err := judgeToolAnswer("server_info", m, false)
	if err != nil {
		return si, err
	}
	if err := json.Unmarshal([]byte(text), &si); err != nil {
		return si, fmt.Errorf("server_info answered a payload the gate cannot read: %w", err)
	}
	var missing []string
	if !si.Semantic {
		missing = append(missing, "semantic=false (no query embedder: the hybrid arms would be BM25)")
	}
	if !si.Reranker {
		missing = append(missing, fmt.Sprintf("reranker=false (%s)", si.RerankerReason))
	}
	// The sparse arm too (review of #340): with it off, Search fuses BM25 and dense
	// only and still reports mode "hybrid", so the hybrid rows would compare a
	// different stack with the sparse-backed baseline.
	if !si.Sparse {
		missing = append(missing, fmt.Sprintf("sparse=false (%s)", si.SparseReason))
	}
	if si.Etsi.Attached {
		missing = append(missing, "the ETSI half is attached although the gate started the server with "+
			"--etsi-db off — the memory bound this gate is sized for does not hold")
	}
	if len(missing) > 0 {
		return si, fmt.Errorf("server-full cannot serve what the served gate scores: %s — scoring it would "+
			"record lexical numbers under semantic names:\n%s", strings.Join(missing, "; "), clipText(text, 1200))
	}
	return si, nil
}

// requireServedBaseline refuses to START without a complete committed bar
// covering every arm — the verdict at the end would refuse anyway, but only after
// minutes of scoring. It never writes the file.
func requireServedBaseline(path string) error {
	if !fileNonEmpty(path) {
		return fmt.Errorf("the served retrieval gate has no baseline at %s — it will not hold the served path "+
			"against nothing, and it will not seed one from the run it is judging; restore the committed file", path)
	}
	base, err := eval.LoadBaseline(path)
	if err != nil {
		return fmt.Errorf("the served retrieval gate cannot use %s: %w", path, err)
	}
	var uncovered []string
	for _, a := range servedArms {
		if _, ok := base[a.Key]; !ok {
			uncovered = append(uncovered, a.Key)
		}
	}
	if len(uncovered) > 0 {
		return fmt.Errorf("%s has no entry for the %s arm(s) — an arm with no bar can never regress", path,
			strings.Join(uncovered, ", "))
	}
	return nil
}

// judgeServed holds a run to the committed bar and says, per arm, what moved.
func judgeServed(c *Ctx, run *servedRun) error {
	base := filepath.Join(c.Root, filepath.FromSlash(servedBaseline))
	if err := eval.WriteBaseline(servedMetricsPath(c), run.Metrics); err != nil {
		c.Log.Printf("WARNING: could not write the candidate baseline %s: %v", servedMetricsPath(c), err)
	}
	var tol float64
	if _, err := fmt.Sscan(servedTol, &tol); err != nil {
		return fmt.Errorf("servedTol %q is not a number: %w", servedTol, err)
	}
	regs, err := eval.Judge(base, run.Metrics, tol)
	if err != nil {
		return fmt.Errorf("SERVED RETRIEVAL GATE CANNOT PASS: %w", err)
	}
	if len(regs) == 0 {
		c.Log.Printf("served retrieval gate: no tracked metric below %s (tol %s); candidate baseline in %s",
			servedBaseline, servedTol, servedMetricsPath(c))
		return nil
	}
	var lines []string
	for _, r := range regs {
		lines = append(lines, fmt.Sprintf("  %-8s %-18s baseline=%.3f current=%.3f delta=%.3f",
			r.System, r.Metric, r.Baseline, r.Current, r.Delta))
	}
	return fmt.Errorf("SERVED RETRIEVAL QUALITY GATE FAILED — the path a client receives ranks the judged queries "+
		"worse than %s allows (tol %s):\n%s\nIf the drop is deliberate, commit %s as %s in the same change",
		servedBaseline, servedTol, strings.Join(lines, "\n"), servedMetricsPath(c), servedBaseline)
}

// logServedRun prints the per-arm table and each query's rank of its best
// judgement, so a failure is read in the log rather than re-run by hand.
func logServedRun(c *Ctx, set eval.Set, run *servedRun) {
	c.Log.Printf("served retrieval (%d queries, search_spec over JSON-RPC):", len(set))
	c.Log.Printf("  %-8s %7s %7s %9s %7s %9s %7s", "arm", "nDCG@5", "nDCG@10", "Recall@10", "MRR@10", "Success@1", "secs")
	for _, a := range servedArms {
		m := run.Metrics[a.Key]
		c.Log.Printf("  %-8s %7.3f %7.3f %9.3f %7.3f %9.3f %7.1f", a.Key, m.NDCG5, m.NDCG10, m.Recall10, m.MRR,
			m.Success1, run.Secs[a.Key])
	}
	for i, q := range set {
		var cells []string
		for _, a := range servedArms {
			cells = append(cells, fmt.Sprintf("%s=%.2f", a.Key, run.Per[a.Key][i].NDCG10))
		}
		c.Log.Printf("  nDCG@10 %-20s %s", q.ID, strings.Join(cells, " "))
	}
}

// buildServeSucceeded refuses a server-full.exe that build-serve did not just
// vouch for. build-serve is Optional, so the runner continues past its failure —
// and leaves the PREVIOUS server-full.exe in .local/bin, built from another tree.
// Scoring that would gate a binary that is not the one this tree produces.
func buildServeSucceeded(c *Ctx) error {
	st, err := NewStore(c.Local)
	if err != nil {
		return err
	}
	rec, err := st.Load("build-serve")
	if err != nil {
		return err
	}
	switch {
	case rec == nil:
		return errors.New("build-serve has never run, so there is no server-full to score — the served gate " +
			"will not pass for want of the binary it measures")
	case rec.Status != StatusSuccess:
		return fmt.Errorf("build-serve's last run is %s, so %s is whatever an earlier tree left there — the "+
			"served gate will not score a binary this tree did not build", rec.Status, c.bin("server-full"))
	}
	// THE BINARY, NOT ONLY THE RECORD (review of #340). A successful record says
	// build-serve once produced a server-full; it does not say the file there now
	// is that one — a copy dropped in by hand, or a rebuild from another tree,
	// leaves the record untouched. The runner saves each output's identity (size
	// and content hash for a file this size); the gate scores only the bytes that
	// record names.
	bin := c.bin("server-full")
	fi, err := os.Stat(bin)
	if err != nil || fi.Size() == 0 {
		return fmt.Errorf("%s is missing although build-serve recorded success", bin)
	}
	key := filepath.ToSlash(rel(c.Root, bin))
	want, ok := rec.Outputs[key]
	if !ok {
		return fmt.Errorf("build-serve's record names no identity for %s — the gate cannot tell whether the "+
			"binary there is the one it built; re-run build-serve", key)
	}
	if got := outputIdentity(bin, fi); got != want {
		return fmt.Errorf("%s is not the binary build-serve recorded (%s now, %s then) — it changed after the "+
			"build, and the served gate will not score bytes this tree did not produce; re-run build-serve",
			key, got, want)
	}
	return nil
}

// runServedRetrievalGate starts server-full over both halves and holds the
// served ranking to the committed per-arm baseline.
func runServedRetrievalGate(c *Ctx) error {
	base := filepath.Join(c.Root, filepath.FromSlash(servedBaseline))
	if err := requireServedBaseline(base); err != nil {
		return err
	}
	_ = os.Remove(servedMetricsPath(c)) // a stale file is never "what this run measured"
	if err := buildServeSucceeded(c); err != nil {
		return err
	}
	all, err := eval.Load(filepath.Join(c.Root, filepath.FromSlash(retrievalQuerySet)))
	if err != nil {
		return fmt.Errorf("load the judged query set: %w", err)
	}
	set, err := servedSubset(all)
	if err != nil {
		return err
	}

	args := servedServerArgs(c)
	c.Log.Printf("served retrieval gate: %s %s (arms %s, queries %s, tol %s, SEARCH_BUDGET=%s, "+
		"DUCKDB_MEMORY_LIMIT=%s) vs %s", c.bin("server-full"), strings.Join(args, " "), servedArmKeys(),
		strings.Join(servedQueryIDs, ","), servedTol, servedSearchBudget, servedMemoryLimit, servedBaseline)
	start := time.Now()
	srv, err := startStdio(c, c.bin("server-full"), args, servedServerEnv(c))
	if err != nil {
		return err
	}
	defer srv.close()
	if err := srv.initialize("goal-served-gate"); err != nil {
		return fmt.Errorf("server-full did not initialise: %w", err)
	}
	c.Log.Printf("server-full initialised in %.1fs", time.Since(start).Seconds())
	info, err := srv.call("server_info", map[string]any{})
	if err != nil {
		return fmt.Errorf("server_info failed: %w", err)
	}
	si, err := requireServedArms(info)
	if err != nil {
		return err
	}
	c.Log.Printf("server-full serves semantic=%v reranker=%v hnsw=%v sparse=%v model=%s (3GPP half only)",
		si.Semantic, si.Reranker, si.Hnsw, si.Sparse, si.Model)

	scoring := time.Now()
	run, err := scoreServed(set, srv.call, c.Log.Printf)
	if err != nil {
		return fmt.Errorf("SERVED RETRIEVAL GATE FAILED: %w", err)
	}
	logServedRun(c, set, run)
	c.Log.Printf("served retrieval gate: scored in %.1fs, %.1fs with startup", time.Since(scoring).Seconds(),
		time.Since(start).Seconds())
	return judgeServed(c, run)
}

// ------------------------------------------------------------ stdio session

// lockedBuffer is a strings.Builder os/exec's copier can fill while a failure
// message reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// stdioSession is one MCP server driven over stdio, one request at a time — as
// a client drives it, and so the latency of each call is its own.
type stdioSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *lockedBuffer
	id     int
}

func startStdio(c *Ctx, bin string, args, env []string) (*stdioSession, error) {
	cmd := exec.CommandContext(c.Context, bin, args...)
	cmd.Dir = c.Root
	// A later duplicate wins, so the pinned values override the operator's.
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &stdioSession{cmd: cmd, stdin: stdin, lines: make(chan string, 16), stderr: &lockedBuffer{}, id: 1}
	cmd.Stderr = s.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	go func() {
		rd := bufio.NewReaderSize(stdout, 1<<20)
		for {
			l, err := rd.ReadString('\n')
			if l != "" {
				s.lines <- l
			}
			if err != nil {
				close(s.lines)
				return
			}
		}
	}()
	return s, nil
}

func (s *stdioSession) close() {
	_ = s.stdin.Close()
	_ = s.cmd.Process.Kill()
	_, _ = s.cmd.Process.Wait()
}

func (s *stdioSession) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

// request sends one JSON-RPC request and returns the response carrying its id.
// Lines without that id (a notification, a log line on stdout) are skipped.
func (s *stdioSession) request(method string, params any) (map[string]any, error) {
	s.id++
	id := s.id
	if err := s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("write %s: %w — %s", method, err, serverPostmortem(s.cmd, s.stderr))
	}
	deadline := time.After(servedCallTimeout)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return nil, fmt.Errorf("%s: the server closed its stdout — %s", method, serverPostmortem(s.cmd, s.stderr))
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				return nil, fmt.Errorf("server sent a non-JSON line: %q", clipText(l, 300))
			}
			if got, _ := m["id"].(float64); int(got) == id {
				return m, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("%s: no answer within %s (stderr: %s)", method, servedCallTimeout,
				tailString(s.stderr.String(), 12))
		}
	}
}

func (s *stdioSession) initialize(client string) error {
	if _, err := s.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": client, "version": "1"},
	}); err != nil {
		return err
	}
	return s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// call is a toolCaller over this session.
func (s *stdioSession) call(tool string, args map[string]any) (map[string]any, error) {
	return s.request("tools/call", map[string]any{"name": tool, "arguments": args})
}
