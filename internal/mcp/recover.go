package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// A PANIC IN A HANDLER MUST STILL BE AN ANSWER.
//
// mcp-go v1.0.0 runs every tools/call on a stdio worker that recovers a panic —
// and then answers with createErrorResponse(nil, …): a JSON-RPC error whose id is
// NULL. No client can match that to the request it sent, so the call it is waiting
// on never completes. That is what search_spec(rerank=true) did on an empty passage
// (#340, fixed in internal/rerank) and what ANY other panic would do: the server
// keeps running and one client call hangs forever. A resources/read is worse: it is
// handled on the reading goroutine, outside the worker, and a panic there takes the
// whole process down.
//
// mcp-go has a native answer, server.WithRecovery, and it is NOT used here, for two
// measured reasons. It returns the panic as a Go error, which becomes a JSON-RPC
// PROTOCOL error (-32603) rather than a tool result with isError — the MCP
// convention for "this tool failed", the one a client shows the model so it can
// adapt. And it logs nothing: the stack is thrown away, so the operator is left with
// "runtime error: index out of range" and no line number. The middleware below uses
// the same native seam (server.WithToolHandlerMiddleware, applied by mcp-go at CALL
// time to every registered tool, the subject tools included) and keeps both.
//
// It is installed in New, once, rather than as a wrapper at each registration like
// `shielded`: a middleware cannot be forgotten by the next registration, and the
// subject tools (li_events) are registered by a loop that `shielded` never saw.

// panicLog is where a recovered panic's stack goes. stderr in production — stdout
// IS the protocol on the stdio transport. A variable so a test can read what the
// operator would read.
var panicLog = &lockedWriter{w: os.Stderr}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) swap(w io.Writer) io.Writer {
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.w
	l.w = w
	return old
}

// panicValueLimit bounds the panic value quoted back to the client. The value is
// a message ("runtime error: …"), not a stack, but nothing bounds what a panic
// carries, and a reply is not the place for an unbounded dump.
const panicValueLimit = 200

// panicSummary renders a recovered value for the client: its first line, bounded.
// The stack never goes here; it goes to panicLog.
func panicSummary(p any) string {
	s := fmt.Sprint(p)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > panicValueLimit {
		s = s[:panicValueLimit] + "…"
	}
	return s
}

// recoverTools turns a panic in any tool handler into a tool error the client
// receives on the request it sent.
func recoverTools(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, r mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		defer func() {
			if p := recover(); p != nil {
				fmt.Fprintf(panicLog, "[3gpp-mcp] panic in tool %s: %v\n%s\n", r.Params.Name, p, debug.Stack())
				res, err = mcp.NewToolResultError(fmt.Sprintf(
					"%s failed: internal error — the handler panicked (%s). The server recovered and is "+
						"still serving; the stack is in its stderr log. This is a server fault, not an "+
						"empty answer: it says nothing about what the corpus holds.",
					r.Params.Name, panicSummary(p))), nil
			}
		}()
		return next(ctx, r)
	}
}

// recoverResources does the same for resources/read, which has no isError: the
// failure is a JSON-RPC error, carrying the request's id — unlike the process
// abort it replaces.
func recoverResources(next server.ResourceHandlerFunc) server.ResourceHandlerFunc {
	return func(ctx context.Context, r mcp.ReadResourceRequest) (res []mcp.ResourceContents, err error) {
		defer func() {
			if p := recover(); p != nil {
				fmt.Fprintf(panicLog, "[3gpp-mcp] panic reading resource %s: %v\n%s\n", r.Params.URI, p, debug.Stack())
				res, err = nil, fmt.Errorf("reading %s failed: internal error — the handler panicked (%s); "+
					"the server recovered and the stack is in its stderr log", r.Params.URI, panicSummary(p))
			}
		}()
		return next(ctx, r)
	}
}
