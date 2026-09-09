package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPJsonWiresBothONNXRuntimes pins the wiring a human actually uses.
//
// `.mcp.json` is how Claude Code starts this server on a developer's machine. It
// set ORT_DYLIB_PATH alone, and the server came up with:
//
//	The requested API version [25] is not available, only API versions [1, 20]
//	are supported in this build. Current ORT Version is: 1.20.1
//	… embedder=true reranker=false
//
// TWO BINDINGS, TWO RUNTIMES, AND THEY ARE NOT INTERCHANGEABLE.
// ORT_DYLIB_PATH is the RUST crate's variable (rust/embed-core, the query
// embedder), pinned to the 1.20.1 build it was compiled against.
// ONNXRUNTIME_SHARED_LIBRARY_PATH is the GO binding's, used by the cross-encoder
// reranker, and it needs the newer pin. One file cannot satisfy both, and the
// process does not need it to: scripts/local/prove-serving.sh has set both since
// 2026-09-01 and gets semantic=true and reranker=true from the same server.
//
// The failure mode is why this is a test rather than a comment: the server does
// not refuse to start. It starts, answers every query, and silently drops one of
// the four retrieval arms — and `server_info` is the only place that says so.
// prove-serving.sh was green throughout, because it sets the variables itself.
func TestMCPJsonWiresBothONNXRuntimes(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Skipf("no .mcp.json in this tree (%v)", err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf(".mcp.json is not valid JSON: %v", err)
	}
	srv, ok := cfg.MCPServers["3gpp"]
	if !ok {
		t.Fatal(`.mcp.json declares no "3gpp" server`)
	}

	for _, k := range []string{"ORT_DYLIB_PATH", "ONNXRUNTIME_SHARED_LIBRARY_PATH"} {
		v, set := srv.Env[k]
		if !set || strings.TrimSpace(v) == "" {
			t.Errorf("%s is not set. Without it one of the two ONNX bindings falls back and the "+
				"server starts anyway with an arm missing — ORT_DYLIB_PATH feeds the Rust query "+
				"embedder, ONNXRUNTIME_SHARED_LIBRARY_PATH feeds the Go cross-encoder", k)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, v)); err != nil {
			t.Errorf("%s points at %q, which is not in the tree: %v", k, v, err)
		}
	}
	// The two must be DIFFERENT files: pointing both at one runtime is the state
	// this test exists to end, and it looks correct at a glance.
	if a, b := srv.Env["ORT_DYLIB_PATH"], srv.Env["ONNXRUNTIME_SHARED_LIBRARY_PATH"]; a != "" && a == b {
		t.Errorf("both ONNX variables point at %q — one runtime cannot satisfy both pins", a)
	}

	// The paths the server needs at all, so a missing corpus is named here rather
	// than as an opaque startup failure inside the client.
	need := []string{srv.Command}
	for i, a := range srv.Args {
		if (a == "--db" || a == "--etsi-db") && i+1 < len(srv.Args) {
			need = append(need, srv.Args[i+1])
		}
	}
	if d := srv.Env["EMBED_MODEL_DIR"]; d != "" {
		need = append(need, d)
	}
	for _, p := range need {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf(".mcp.json references %q, which is not in the tree: %v", p, err)
		}
	}
}
