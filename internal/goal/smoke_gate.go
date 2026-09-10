package goal

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// This file is what the `smoke` step ACCEPTS: the verdict on each answer the real
// server gives. runSmoke in pipeline_embed.go drives the process; the judgements
// live here so they can be tested against real wire lines without starting a
// server over a 23 GB corpus.

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
