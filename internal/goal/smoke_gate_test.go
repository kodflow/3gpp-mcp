package goal

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
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
