package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// A PANIC MUST COME BACK AS AN ANSWER, ON THE TRANSPORT A CLIENT ACTUALLY USES.
//
// Without recover.go, mcp-go's stdio worker recovers the panic and replies with a
// JSON-RPC error whose id is NULL — a reply to nobody — so the call hangs; that is
// how search_spec(rerank=true) behaved before #340. A test that called the
// middleware function directly would pass whether or not New installs it, so these
// tests drive the REAL server through the REAL stdio transport (the one ServeStdio
// runs in the image and in .mcp.json) and wait for a reply carrying the request's
// own id.
//
// The panic is real too: every store read panics (a nil store behind the Reader
// interface, swapped in after New), so each tool fails in its own handler, on its
// own path — the 13 tools, the subject one (li_events) that `shielded` never wraps
// included, and whatever tool is added next, because the list is read from
// tools/list rather than written here.

// explodingReader is a real store until detonate() — then every method panics
// with a nil dereference, from inside whichever handler called it.
type explodingReader struct{ store.Reader }

func (e *explodingReader) detonate() { e.Reader = nil }

func explodingServer(t *testing.T) (*server.MCPServer, *explodingReader) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2",
			Heading: "Generation of xIRI over LI_X2", Text: "delivered to MDF2"},
	})
	ex := &explodingReader{Reader: st}
	srv, _ := New(ex, "test", "", nil, nil)
	return srv, ex
}

// capturePanicLog routes what the operator would read on stderr into a buffer.
func capturePanicLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := panicLog.swap(&buf)
	t.Cleanup(func() { panicLog.swap(old) })
	return &buf
}

// stdioRig is a client on the other end of the server's stdin/stdout.
type stdioRig struct {
	in    io.Writer
	lines chan []byte
	next  int
}

func startStdio(t *testing.T, srv *server.MCPServer) *stdioRig {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	s := server.NewStdioServer(srv)
	s.SetErrorLogger(log.New(io.Discard, "", 0))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Listen(ctx, inR, outW)
		_ = outW.Close()
	}()
	lines := make(chan []byte, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	r := &stdioRig{in: inW, lines: lines, next: 1}
	r.request(t, "initialize", map[string]any{
		"protocolVersion": mcpgo.LATEST_PROTOCOL_VERSION,
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
		"capabilities":    map[string]any{},
	})
	r.notify(t, "notifications/initialized")
	return r
}

func (r *stdioRig) notify(t *testing.T, method string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if _, err := r.in.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// rpcReply is one JSON-RPC response line.
type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// request sends one call and waits for the reply that carries ITS id. Lines with
// any other id — the id:null reply mcp-go sends when it recovers a panic itself —
// are kept and shown if the wait runs out, because they are the evidence.
func (r *stdioRig) request(t *testing.T, method string, params any) rpcReply {
	t.Helper()
	id := r.next
	r.next++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := r.in.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint(id)
	var other []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-r.lines:
			if !ok {
				t.Fatalf("%s (id %d): the server closed stdout without answering; other lines: %q", method, id, other)
			}
			var rep rpcReply
			if json.Unmarshal(line, &rep) == nil && string(rep.ID) == want {
				return rep
			}
			other = append(other, string(line))
		case <-deadline:
			t.Fatalf("%s (id %d): NO REPLY CARRYING THIS ID within 10 s — a client waits forever. "+
				"What the server did write: %q", method, id, other)
		}
	}
}

// toolResult decodes a tools/call reply that must be a RESULT, not a protocol error.
func toolResult(t *testing.T, name string, rep rpcReply) mcpgo.CallToolResult {
	t.Helper()
	if rep.Error != nil {
		t.Fatalf("%s: a protocol error (%d %q) instead of a tool result with isError", name, rep.Error.Code, rep.Error.Message)
	}
	var res mcpgo.CallToolResult
	if err := json.Unmarshal(rep.Result, &res); err != nil {
		t.Fatalf("%s: undecodable result %s: %v", name, rep.Result, err)
	}
	return res
}

func resultText(res mcpgo.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := mcpgo.AsTextContent(c); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// assertPanicAnswer checks the reply a client gets for a panicking tool: an
// isError result that names the tool and the fault, with no stack in it.
func assertPanicAnswer(t *testing.T, name string, res mcpgo.CallToolResult) {
	t.Helper()
	text := resultText(res)
	if !res.IsError {
		t.Errorf("%s: a panicking handler answered isError=false: %q", name, text)
	}
	if !strings.Contains(text, name) || !strings.Contains(text, "panicked") {
		t.Errorf("%s: the error must name the tool and say it panicked; got %q", name, text)
	}
	for _, leak := range []string{"goroutine ", ".go:", "runtime/debug"} {
		if strings.Contains(text, leak) {
			t.Errorf("%s: the reply leaks the stack (%q) to the client: %q", name, leak, text)
		}
	}
}

func TestEveryToolThatPanicsStillAnswersOverStdio(t *testing.T) {
	logged := capturePanicLog(t)
	srv, ex := explodingServer(t)
	rig := startStdio(t, srv)

	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	rep := rig.request(t, "tools/list", map[string]any{})
	if err := json.Unmarshal(rep.Result, &listed); err != nil {
		t.Fatal(err)
	}
	// Not vacuous: every tool of the surface CLAUDE.md §5 documents is listed, by
	// NAME — a count alone passes when one tool is swapped for another (CodeRabbit,
	// #344). Whatever else is registered is exercised too: the loop below walks the
	// list, not this set.
	names := map[string]bool{}
	for _, tl := range listed.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{
		"search_spec", "get_spec", "get_changelog", "list_releases", "resolve_term", "trace_evolution",
		"find_cross_references", "list_specs", // the eight core tools
		"search_api", "trace_clause", "help", "server_info", // the core siblings
		"li_events", // the subject tool, registered outside `shielded`
	} {
		if !names[want] {
			t.Errorf("tools/list does not list %s; the test must exercise it", want)
		}
	}
	if t.Failed() {
		t.Fatalf("tools/list returned %v", names)
	}

	t.Logf("exercising %d tools over stdio", len(names))

	ex.detonate()
	for _, tl := range listed.Tools {
		args := map[string]any{}
		for _, req := range tl.InputSchema.Required {
			args[req] = "33.128"
		}
		res := toolResult(t, tl.Name, rig.request(t, "tools/call", map[string]any{"name": tl.Name, "arguments": args}))
		assertPanicAnswer(t, tl.Name, res)
		// The operator, not the client, gets the stack.
		if !strings.Contains(logged.String(), "panic in tool "+tl.Name+":") {
			t.Errorf("%s: no stack logged for the operator; log so far:\n%s", tl.Name, logged.String())
		}
	}
	if !strings.Contains(logged.String(), "goroutine ") {
		t.Errorf("the log carries no stack trace:\n%s", logged.String())
	}

	// And the server is still serving after a dozen panics.
	if rep := rig.request(t, "tools/list", map[string]any{}); rep.Error != nil || len(rep.Result) == 0 {
		t.Errorf("the server stopped answering after the panics: %+v", rep)
	}
}

// resources/read is processed on the goroutine that reads stdin, outside the
// tool worker and its recover: a panic there used to take the process down.
func TestAResourceReadThatPanicsAnswersWithItsID(t *testing.T) {
	logged := capturePanicLog(t)
	srv, ex := explodingServer(t)
	rig := startStdio(t, srv)
	ex.detonate()

	uri := "3gpp://33.128/Rel-19/6.2.2.2"
	rep := rig.request(t, "resources/read", map[string]any{"uri": uri})
	if rep.Error == nil {
		t.Fatalf("a panicking resource read returned a result: %s", rep.Result)
	}
	if !strings.Contains(rep.Error.Message, "panicked") || strings.Contains(rep.Error.Message, "goroutine ") {
		t.Errorf("the error must say the handler panicked, without the stack: %q", rep.Error.Message)
	}
	if !strings.Contains(logged.String(), "panic reading resource "+uri) {
		t.Errorf("no stack logged for the operator:\n%s", logged.String())
	}
	if rep := rig.request(t, "tools/list", map[string]any{}); rep.Error != nil {
		t.Errorf("the server stopped answering after a resource panic: %+v", rep.Error)
	}

	// The URI is the client's text and no transport bounds it: it is quoted
	// bounded, in the reply and in the log (CodeRabbit, #344).
	long := "3gpp://33.128/Rel-19/" + strings.Repeat("9.", 5000)
	logged.Reset()
	rep = rig.request(t, "resources/read", map[string]any{"uri": long})
	if rep.Error == nil {
		t.Fatalf("a panicking read of a long URI returned a result")
	}
	if len(rep.Error.Message) > 2*errorTextLimit {
		t.Errorf("a %d-byte URI came back in a %d-byte error", len(long), len(rep.Error.Message))
	}
	if first, _, _ := strings.Cut(logged.String(), "\n"); len(first) > 2*errorTextLimit {
		t.Errorf("a %d-byte URI was logged in a %d-byte line", len(long), len(first))
	}
}

// The HTTP transport (server --http, the landing page's /mcp) runs the same
// middleware chain; without it net/http recovers the panic by DROPPING the
// connection, which a client reads as a transport failure, not a tool error.
func TestAToolThatPanicsAnswersOverStreamableHTTP(t *testing.T) {
	_ = capturePanicLog(t)
	srv, ex := explodingServer(t)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)

	c, err := client.NewStreamableHttpClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var ir mcpgo.InitializeRequest
	ir.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	ir.Params.ClientInfo = mcpgo.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, ir); err != nil {
		t.Fatal(err)
	}

	ex.detonate()
	var r mcpgo.CallToolRequest
	r.Params.Name = "search_spec"
	r.Params.Arguments = map[string]any{"query": "registration"}
	res, err := c.CallTool(ctx, r)
	if err != nil {
		t.Fatalf("search_spec over HTTP: a transport failure instead of a tool error: %v", err)
	}
	assertPanicAnswer(t, "search_spec", *res)
}
