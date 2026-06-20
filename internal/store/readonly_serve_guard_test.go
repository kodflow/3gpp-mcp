package store_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestServeIsCorpusWriteFree is the Phase-11a callgraph guard: it statically asserts that
// the SERVE / read-side packages never call a corpus-MUTATION method on the store. The
// write-side→Rust migration deleted every Go producer (cmd/ingest, …, cmd/embed) and made
// the Go side read-only; this test locks that contract so a future change can't quietly
// reintroduce a Go write path into serve. Index/extension LOADING (LoadFTS/LoadVSS/
// LoadSparse) and shard ATTACH are read-side setup, not corpus mutation, so they are allowed.
//
// It parses the .go sources (non-test) of the serve packages and fails on any call whose
// selector names a forbidden mutation method.
func TestServeIsCorpusWriteFree(t *testing.T) {
	// Corpus-mutation surface that serve must NEVER call (writes clauses / vectors /
	// catalogue / subject tables / indexes). Loading + attaching read shards is allowed.
	forbidden := map[string]bool{
		"InsertClauses": true, "UpsertSpec": true, "UpsertVersion": true,
		"SetEmbeddingsBatch": true, "SetEmbedding": true, "StripEmbeddings": true,
		"UpsertAcronym": true, "InsertEvolutions": true, "InsertAPIOperations": true,
		"InsertAPISchemas": true, "ClearAPITables": true, "ApplyReleaseFreeze": true,
		"UpsertReleases": true, "FoldShard": true, "BuildAndFreezeHNSW": true,
		"EnableFTS": true, "LogIngest": true, "IngestDone": true,
		"WriteLIRegistry": true, "InsertEvents": true, "InsertFields": true,
		"InsertNFClauses": true, "InsertASN1Types": true, "Overlay": true,
		"SetVersionMetadataSource": true, "UpdateSpecMeta": true,
	}
	// Serve / read-side package roots (relative to internal/store → repo).
	roots := []string{
		"../../internal/search",
		"../../internal/mcp",
		"../../internal/releaseview",
		"../../cmd/server",
	}
	fset := token.NewFileSet()
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if forbidden[sel.Sel.Name] {
					t.Errorf("%s: serve/read-side calls forbidden corpus-mutation method %q — the Go side is read-only (write-side is Rust, Phase 11b)", path, sel.Sel.Name)
				}
				return true
			})
			return nil
		})
	}
}
