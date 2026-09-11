package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// This file is what the `smoke` step ACCEPTS: the verdict on each answer the real
// server gives, and the retrieval-quality gate that runs after it. runSmoke in
// pipeline_embed.go drives the process; the judgements live here so they can be
// tested against real wire lines without starting a server over a 23 GB corpus.

// smokeProbe is one call the smoke makes, and must be answered with something.
type smokeProbe struct{ tool, arg, val string }

// smokeProbes is one representative call per retrieval path the corpus can
// actually answer today, so the smoke exercises the real search code, not a stub.
//
// EACH ONE MUST FIND SOMETHING, and each was picked because this corpus cannot
// honestly answer it with nothing. Measured 2026-09-11 against .local/bin/server.exe
// over the 23.1 GB 3gpp.duckdb with etsi.duckdb attached, the way this step
// launches it:
//
//	search_spec  query="AMF registration procedure"  count 10  (TS 23.502 4.2.2.2.3 first)
//	list_specs   series="23"                         count 170
//	resolve_term term="AMF"                          count 22
//
// An empty answer to one of these is therefore not a legitimate "nothing": it is
// a retrieval path that stopped working without raising — an FTS index that did
// not load serves an empty list, a glossary the enrich step never wrote serves
// `count 0`. That is the second way a tool fails while the protocol succeeds.
var smokeProbes = []smokeProbe{
	{"search_spec", "query", "AMF registration procedure"},
	{"list_specs", "series", "23"},
	{"resolve_term", "term", "AMF"},
}

// missingProbes lists the tools the smoke must call that the server does not
// expose.
//
// A PROBE THE SERVER DOES NOT OFFER IS A FAILURE, NOT A SKIP. The loop used to
// `continue` past an absent tool, so a server that exposed only `help` passed the
// smoke having called nothing at all. All four are registered unconditionally in
// internal/mcp/server.go (search_spec, resolve_term, list_specs, server_info), so
// a server without one of them is not the product this step is proving.
func missingProbes(names []string) []string {
	var out []string
	for _, p := range smokeProbes {
		if !contains(names, p.tool) {
			out = append(out, p.tool)
		}
	}
	if !contains(names, "server_info") {
		out = append(out, "server_info")
	}
	return out
}

// judgeToolAnswer reads one tools/call response the way a client does, and
// returns the tool's text payload and its `count`, or the reason the call did NOT
// answer. count is -1 when mustFind is false.
//
// A JSON-RPC `error` member is the PROTOCOL failing — an unknown method, a
// malformed request. It is not how a tool fails. MCP reports a failing TOOL as an
// ordinary `result` carrying "isError": true, with the failure as text: that is
// what mcp.NewToolResultErrorFromErr builds, and what every handler in
// internal/mcp returns when its query fails. The smoke checked only the first, so
// it logged "answered" for a tool that answered with its own error. That shape is
// not hypothetical here:
//
//   - search_api answered `search_api failed: sql: Scan error on column index 9
//     … converting NULL to string is unsupported` for essentially every query in
//     the image published as :latest (8d6c657, 2026-09-07, 84 % of api_schemas
//     rows), and was found by hand, in a container.
//   - get_changelog scanned the nullable cr_revision into an int, so one NULL
//     turned the whole call into an error for the spec (#322, 2026-09-10).
//
// Both are an isError result on a JSON-RPC success. Captured from this corpus on
// 2026-09-11, a failing tool looks like this on the wire, and the old check let
// it through:
//
//	{"jsonrpc":"2.0","id":6,"result":{"content":[{"type":"text",
//	 "text":"no such spec/clause in corpus: 99.999"}],"isError":true}}
//
// The error names the tool and quotes what it said, because "search_spec failed"
// is not a diagnosis and the text usually is.
func judgeToolAnswer(tool string, m map[string]any, mustFind bool) (string, int, error) {
	if e, bad := m["error"]; bad {
		return "", -1, fmt.Errorf("%s returned a JSON-RPC error: %v", tool, e)
	}
	res, ok := m["result"].(map[string]any)
	if !ok {
		raw, _ := json.Marshal(m)
		return "", -1, fmt.Errorf("%s answered with no result object: %s", tool, clipText(string(raw), 300))
	}
	var texts []string
	blocks, _ := res["content"].([]any)
	for _, b := range blocks {
		if bm, ok := b.(map[string]any); ok {
			if s, ok := bm["text"].(string); ok && strings.TrimSpace(s) != "" {
				texts = append(texts, s)
			}
		}
	}
	text := strings.Join(texts, "\n")
	if failed, _ := res["isError"].(bool); failed {
		if text == "" {
			text = "(the tool gave no reason)"
		}
		return "", -1, fmt.Errorf("%s FAILED — the protocol call succeeded and the TOOL answered isError=true: %q",
			tool, clipText(text, 600))
	}
	if text == "" {
		return "", -1, fmt.Errorf("%s answered with no content at all (%d content block(s), none with text)",
			tool, len(blocks))
	}
	if !mustFind {
		return text, -1, nil
	}
	// Every probe tool answers a JSON object with a `count` (internal/mcp:
	// searchSpec, listSpecs, resolveTerm). A payload without one is refused rather
	// than waved through: "I cannot tell whether it found anything" is not a pass.
	var payload struct {
		Count *float64 `json:"count"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil || payload.Count == nil {
		return "", -1, fmt.Errorf("%s answered, but its payload carries no readable \"count\", so the smoke "+
			"cannot tell a hit from an empty answer: %q", tool, clipText(text, 300))
	}
	if *payload.Count < 1 {
		return "", 0, fmt.Errorf("%s answered with NOTHING (count %v) to a probe this corpus answers — a "+
			"retrieval path that returns empty instead of failing: %q", tool, *payload.Count, clipText(text, 300))
	}
	return text, int(*payload.Count), nil
}

// clipText bounds a quoted payload so one failing tool cannot flood the log with
// a 400-row answer, while keeping the part that says what went wrong.
func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n-- // never cut a rune in half: the payloads carry em dashes
	}
	return s[:n] + "…"
}

// ----------------------------------------------------------- retrieval gate

// THE RETRIEVAL GATE: proving that calls answer does not prove that answers are
// relevant.
//
// Until 2026-08-31 a CI job scored the judged LI/5GC query set on every PR that
// touched retrieval code, and failed the merge on a regression ("gate OK: no
// tracked-metric regression beyond tol=0.020 vs docs/inputs/eval/baseline.json").
// CI was deleted (208f23b, the image is built on this machine now) and the gate
// went with it: a change that wrecked ranking would pass `test`, `validate` and
// `smoke`, and be published.
//
// WHY IT LIVES IN SMOKE, and not in validate or a step of its own:
//
//   - The judged set is 3GPP-only: every relevant clause is TS 33.128. A new
//     per-arm step would need an -etsi twin with nothing to score — a gate that
//     cannot fail, the defect being fixed — or an armShared exception. smoke is
//     already shared, for a reason that covers this: it is the gate over the
//     PRODUCT as served, and ranking is a property of the product.
//   - It is on the path. publish depends on smoke, so a regression stops the
//     image — the property the CI job had for merges.
//   - smoke already fingerprints data/3gpp.duckdb, so a corpus rebuild (a parser
//     change, the paragraph conversion, a compaction) replays the gate — the way
//     ranking gets wrecked without a line of Go changing.
//   - validate is the data-completeness contract, runs per arm (the ETSI one
//     would carry an `if t.Suffix != ""` escape, as anchorcheck does), and costs
//     2m32 to replay; the gate costs 12-22 s (measured below).
//
// WHAT IT SCORES: -systems lexical, the only system .local/bin/bench.exe can
// score. build-go links it without `onnx` and `embed_ffi`, so its embedder and
// reranker are the noop ones, and `hybrid` and `rerank` DEGRADE TO LEXICAL — on
// 2026-09-10 all three printed the identical 0.072 / 0.167 / 0.042 row (nDCG@10 /
// Recall@10 / MRR@10) on that build. Scoring them there would record lexical
// numbers under semantic names. The real semantic scores, from a bench built
// -tags onnx,embed_ffi in the prove-serving environment, were 0.101 / 0.417 /
// 0.142 hybrid and 0.207 / 0.417 / 0.417 with the reranker — in 422 s and 18 GB
// of working set, on a build that is Optional.
//
// THE SEMANTIC ARMS ARE GATED NOW, BUT NOT HERE: smoke_served.go drives
// server-full.exe — the served path itself, federation included — rather than a
// semantic bench, and holds it to its own baseline. This gate stays as it is: it
// is the engine-level lexical measurement, cheap, and on the binary build-go
// makes.
//
// Measured on the real corpus, 2026-09-10 and -11 (lexical, 6 queries):
//
//	lexical (BM25)  nDCG@10 0.072  Recall@10 0.167  MRR@10 0.042  Success@1 0.000
//	22.0 s cold, 11.9 s warm; exit 0 against the committed baseline
//	exit 1 against a missing baseline (not seeded), exit 1 with the bar raised
//
// Success@1 is 0.000 today, so it cannot regress; the gate guards the other
// three, each more than tol above zero, and a ranking that finds nothing takes
// all three to zero.
const (
	// retrievalQuerySet and retrievalBaseline are repo files, so they sit in
	// smoke's Impl and are CONTENT-hashed: editing a judgement or moving the bar
	// replays the gate. A deleted baseline fails PLANNING, since implHash refuses
	// a missing Impl path — before the gate could be tempted to pass.
	retrievalQuerySet = "docs/inputs/eval/li_5gc_queries.json"
	retrievalBaseline = "docs/inputs/eval/baseline.json"
	// retrievalSystems and retrievalTol fold into smoke's Extra. They live in
	// internal/goal, which smoke's Impl does not cover, so without Extra a changed
	// tolerance would never replay the gate it loosens.
	retrievalSystems = "lexical"
	// retrievalTol is the absolute drop a tracked metric may take before the gate
	// fails: the default of cmd/bench and the value the CI job ran with.
	retrievalTol = "0.02"
)

// retrievalMetricsPath is where the gate leaves the metrics it measured: a
// CANDIDATE baseline. The gate is one-sided — an improvement never fails it — so
// raising the bar after one is a deliberate act: commit this file as
// retrievalBaseline, and the decision is in a diff.
func retrievalMetricsPath(c *Ctx) string { return c.statePath("retrieval-metrics.json") }

// retrievalGateArgs is the bench command line the gate runs. -baseline is what
// makes it a gate: bench exits 1 on a tracked-metric regression beyond -tol, and
// — since eval.Judge — on a missing baseline or one with no entry for a scored
// system, which it used to seed and pass.
func retrievalGateArgs(c *Ctx) []string {
	return []string{
		"-db", c.dataPath("3gpp.duckdb"),
		"-set", filepath.Join(c.Root, filepath.FromSlash(retrievalQuerySet)),
		"-systems", retrievalSystems,
		"-baseline", filepath.Join(c.Root, filepath.FromSlash(retrievalBaseline)),
		"-tol", retrievalTol,
		"-json", retrievalMetricsPath(c),
	}
}

// runRetrievalGate holds the built corpus to the committed retrieval baseline.
func runRetrievalGate(c *Ctx) error { return retrievalGate(c, c.Run) }

// retrievalGate is runRetrievalGate with the command runner passed in, so a test
// can stand in for bench without a 23 GB corpus.
//
// THE BASELINE IS CHECKED HERE TOO, not only in bench. bench is rebuilt by
// build-go, but the check that the committed bar exists is a property of THIS
// pipeline and must not depend on which bench.exe happens to sit in .local/bin:
// the one built before eval.Judge SEEDS a missing baseline — writing into the
// repository tree — and exits 0.
func retrievalGate(c *Ctx, run func(Cmd) error) error {
	base := filepath.Join(c.Root, filepath.FromSlash(retrievalBaseline))
	if !fileNonEmpty(base) {
		return fmt.Errorf("the retrieval gate has no baseline at %s — it will not hold a corpus against "+
			"nothing, and it will not seed one from the run it is judging; restore the committed file", base)
	}
	// A stale metrics file must never be offered as "what this run measured".
	_ = os.Remove(retrievalMetricsPath(c))
	c.Log.Printf("retrieval gate: %s over %s, tol %s, vs %s", retrievalSystems, retrievalQuerySet, retrievalTol, retrievalBaseline)
	if err := run(Cmd{Name: c.bin("bench"), Args: retrievalGateArgs(c), Echo: true}); err != nil {
		return fmt.Errorf("RETRIEVAL QUALITY GATE FAILED: the corpus ranks the judged queries worse than %s "+
			"allows, or no comparison could be made (the verdict is above). If the drop is deliberate, commit "+
			"%s as %s in the same change: %w", retrievalBaseline, retrievalMetricsPath(c), retrievalBaseline, err)
	}
	c.Log.Printf("retrieval gate: no tracked metric below the committed baseline (metrics in %s)", retrievalMetricsPath(c))
	return nil
}
