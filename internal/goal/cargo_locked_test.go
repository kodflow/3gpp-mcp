package goal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// cargoInvocation is one cargo command that resolves the dependency graph.
type cargoInvocation struct {
	Line int      // 1-based line it starts on
	Sub  string   // its subcommand
	Args []string // every word after `cargo`, as written
}

// isCargoWord reports whether a command word runs cargo: bare, or by path, with
// or without .exe.
func isCargoWord(w string) bool {
	w = strings.TrimSuffix(w, ".exe")
	return w == "cargo" || strings.HasSuffix(w, "/cargo")
}

// cargoSubcommand is the subcommand of `cargo <args>`: the first word that is
// neither a toolchain override (+stable) nor a global option, stepping over the
// value of a global option that takes one. ok is false for `cargo --version`,
// which names none.
func cargoSubcommand(args []string) (sub string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case strings.HasPrefix(a, "+"):
		case cargoGlobalTakesValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, true
		}
	}
	return "", false
}

// cargoGlobalTakesValue are the options cargo accepts before its subcommand whose
// value is the next word.
var cargoGlobalTakesValue = map[string]bool{"--color": true, "--config": true, "-C": true, "-Z": true}

// cargoLeavesTheLockAlone are the subcommands that never resolve the dependency
// graph, or that exist to rewrite the lockfile on purpose. EVERY OTHER subcommand
// is taken to resolve, including one this list has never heard of.
//
// That direction is the point. The list this replaces (6cafdc7) named the
// subcommands that DO resolve, so whatever it lacked was waved through: `cargo b`,
// cargo's own alias for build, and `cargo zigbuild`, the cross-linking wrapper a
// repository that already cross-links through zig (scripts/local/zigcc) would reach
// for next. Both resolve exactly as `cargo build` does. A name missing here makes a
// test demand --locked of a command that did not need it; a name missing there let
// a lockfile be rewritten unseen.
var cargoLeavesTheLockAlone = map[string]bool{
	"fmt": true, "version": true, "help": true, "new": true, "init": true,
	"search": true, "login": true, "logout": true, "owner": true, "yank": true,
	"locate-project": true,
	// These change the lockfile deliberately; --locked would refuse them.
	"update": true, "generate-lockfile": true,
}

// cargoIsLocked reports whether a cargo argument list pins the lockfile: --locked,
// or --frozen, which is --locked plus --offline. Only cargo's own arguments count;
// after `--` they belong to the program `cargo run` or `cargo test` starts.
func cargoIsLocked(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--locked" || a == "--frozen" {
			return true
		}
	}
	return false
}

// cargoInvocationsIn reads every dependency-resolving cargo command out of a bash
// script, one per command bash would run (see readShellCommands).
//
// The first cargo word of a command is taken as the program, wherever it stands,
// so `nice cargo build` and `env X=1 cargo build` are read too. `echo cargo build`
// is read as well, which only ever makes a test demand more.
func cargoInvocationsIn(src string) []cargoInvocation {
	var out []cargoInvocation
	for _, c := range readShellCommands(src) {
		for j, w := range c.Words {
			if !isCargoWord(w) {
				continue
			}
			args := c.Words[j+1:]
			if sub, ok := cargoSubcommand(args); ok && !cargoLeavesTheLockAlone[sub] {
				out = append(out, cargoInvocation{Line: c.Line, Sub: sub, Args: args})
			}
			break // one command runs one program
		}
	}
	return out
}

// TestTheCargoReaderJudgesEachInvocation holds the script reader to the shapes
// review found it blind to on 2026-09-11, each judged on its own flags.
//
// Every one of these passed TestEveryCargoBuildInTheImageIsLocked under the
// 6cafdc7 reader while running cargo unlocked: the first two because the command
// was never seen, the third because the second command was never read, the
// fourth because a comment counted as a flag.
func TestTheCargoReaderJudgesEachInvocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string // "line subcommand locked|unlocked", in order
	}{
		{"the script's own substitution idiom",
			"X=\"$(cargo metadata --format-version 1 --manifest-path a/Cargo.toml)\"\n",
			[]string{"1 metadata unlocked"}},
		{"a toolchain override",
			"cargo +stable build --manifest-path a/Cargo.toml\n",
			[]string{"1 build unlocked"}},
		{"a second cargo on the line, judged on its own flags",
			"cargo build --locked --manifest-path a/Cargo.toml; cargo test --manifest-path a/Cargo.toml\n",
			[]string{"1 build locked", "1 test unlocked"}},
		{"a comment is not a flag",
			"cargo build --manifest-path a/Cargo.toml # --locked\n",
			[]string{"1 build unlocked"}},
		{"a global option before the subcommand",
			"cargo --color never build --locked\n",
			[]string{"1 build locked"}},
		{"an alias, and a subcommand this list never heard of, both resolve",
			"cargo b\ncargo zigbuild --frozen\n",
			[]string{"1 b unlocked", "2 zigbuild locked"}},
		{"what follows -- is the program's",
			"cargo run -- --locked\n",
			[]string{"1 run unlocked"}},
		{"a cargo reached by path",
			"\"$ROOT/.local/toolchain/cargo/bin/cargo.exe\" build\n",
			[]string{"1 build unlocked"}},
		{"what never resolves is not judged",
			"cargo --version\ncargo fmt --check\ncargo update -p ort\n",
			nil},
		{"quoted text is not a command",
			"echo \"cargo build\"\n",
			nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, inv := range cargoInvocationsIn(tc.src) {
				v := "unlocked"
				if cargoIsLocked(inv.Args) {
					v = "locked"
				}
				got = append(got, fmt.Sprintf("%d %s %s", inv.Line, inv.Sub, v))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("judged %q as %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// goCargoCall is one cargo command a step of this package runs, read out of its Go
// source.
type goCargoCall struct {
	Pos  string   // file:line
	Step string   // the Name of the Step literal it runs inside, "" outside one
	Args []string // cargo's arguments; an element that is not a literal reads "<expr>"
}

// goCargoCalls reads every cargo command the non-test Go files of this package run.
//
// Three spellings reach cargo here, and each is read: Cmd{Name: "cargo", Args: …}
// with Args a literal or a local variable built by literals and append (build-rust
// builds its list that way, one --bin per binary); a list whose first element is
// "cargo" (the toolchain probe's {"cargo", "--version"}); and a call taking
// "cargo" as its program, as exec.Command does. A "cargo" string in any other
// position is an error rather than a skip: it is either a command this reader
// cannot judge or a reader gone stale, and both have to be looked at. A cargo whose
// NAME is a variable stays invisible; the count check in the callers fails if
// every call vanishes.
func goCargoCalls(t *testing.T) []goCargoCall {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var calls []goCargoCall
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || len(stack) < 2 {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err != nil || v != "cargo" {
				return true
			}
			pos := fset.Position(lit.Pos())
			where := fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
			lists, err := cargoArgLists(stack)
			if err != nil {
				t.Errorf("%s: %v", where, err)
				return true
			}
			for _, args := range lists {
				calls = append(calls, goCargoCall{Pos: where, Step: enclosingStepName(stack), Args: args})
			}
			return true
		})
	}
	return calls
}

// cargoArgLists finds the argument list of the cargo command whose "cargo" literal
// ends stack. A variable built in more than one place yields one list per place it
// is built, each judged on its own.
func cargoArgLists(stack []ast.Node) ([][]string, error) {
	lit := stack[len(stack)-1]
	switch p := stack[len(stack)-2].(type) {
	case *ast.KeyValueExpr:
		cmd, ok := stack[len(stack)-3].(*ast.CompositeLit)
		if key, isIdent := p.Key.(*ast.Ident); !ok || !isIdent || key.Name != "Name" || !isCmdType(cmd.Type) {
			return nil, fmt.Errorf("\"cargo\" is the value of a field this reader does not know as a command")
		}
		for _, e := range cmd.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Args" {
					return stringLists(kv.Value, enclosingBody(stack))
				}
			}
		}
		return [][]string{nil}, nil // Cmd{Name: "cargo"}: cargo alone, which resolves nothing
	case *ast.CompositeLit:
		if len(p.Elts) > 0 && p.Elts[0] == lit {
			return [][]string{literalStrings(p.Elts[1:])}, nil
		}
	case *ast.CallExpr:
		for i, a := range p.Args {
			if a != lit {
				continue
			}
			rest := p.Args[i+1:]
			if !p.Ellipsis.IsValid() || len(rest) == 0 {
				return [][]string{literalStrings(rest)}, nil
			}
			tails, err := stringLists(rest[len(rest)-1], enclosingBody(stack))
			if err != nil {
				return nil, err
			}
			head := literalStrings(rest[:len(rest)-1])
			var out [][]string
			for _, tail := range tails {
				out = append(out, append(slices.Clone(head), tail...))
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("a \"cargo\" this reader cannot place: if it runs cargo, teach goCargoCalls " +
		"its shape so the command is judged; if it does not, say so here")
}

// stringLists reads a []string expression: a literal, or a local variable assigned
// from literals and extended by append in the function around it.
func stringLists(e ast.Expr, body *ast.BlockStmt) ([][]string, error) {
	switch v := e.(type) {
	case *ast.CompositeLit:
		return [][]string{literalStrings(v.Elts)}, nil
	case *ast.Ident:
		if body == nil {
			return nil, fmt.Errorf("Args is %s, and no function body holds its assignments", v.Name)
		}
		var defs [][]string
		var appended []string
		var bad error
		assign := func(rhs ast.Expr) {
			switch r := rhs.(type) {
			case *ast.CompositeLit:
				defs = append(defs, literalStrings(r.Elts))
			case *ast.CallExpr:
				if fn, ok := r.Fun.(*ast.Ident); ok && fn.Name == "append" && len(r.Args) > 0 {
					if first, ok := r.Args[0].(*ast.Ident); ok && first.Name == v.Name {
						appended = append(appended, literalStrings(r.Args[1:])...)
						return
					}
				}
				bad = fmt.Errorf("%s is assigned from a call this reader cannot follow", v.Name)
			default:
				bad = fmt.Errorf("%s is assigned from an expression this reader cannot follow", v.Name)
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok && id.Name == v.Name && i < len(s.Rhs) {
						assign(s.Rhs[i])
					}
				}
			case *ast.ValueSpec:
				for i, id := range s.Names {
					if id.Name == v.Name && i < len(s.Values) {
						assign(s.Values[i])
					}
				}
			}
			return true
		})
		if bad != nil {
			return nil, bad
		}
		if len(defs) == 0 {
			return nil, fmt.Errorf("Args is %s, and nothing in the function assigns it a list", v.Name)
		}
		for i := range defs {
			defs[i] = append(defs[i], appended...)
		}
		return defs, nil
	}
	return nil, fmt.Errorf("Args is an expression this reader cannot follow")
}

// literalStrings is the string literals of a list, with "<expr>" standing for any
// element that is not one.
func literalStrings(elts []ast.Expr) []string {
	out := make([]string, 0, len(elts))
	for _, e := range elts {
		s := "<expr>"
		if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if v, err := strconv.Unquote(bl.Value); err == nil {
				s = v
			}
		}
		out = append(out, s)
	}
	return out
}

func isCmdType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "Cmd"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Cmd"
	}
	return false
}

// enclosingBody is the body of the innermost function literal or declaration on
// the stack.
func enclosingBody(stack []ast.Node) *ast.BlockStmt {
	for i := len(stack) - 1; i >= 0; i-- {
		switch f := stack[i].(type) {
		case *ast.FuncLit:
			return f.Body
		case *ast.FuncDecl:
			return f.Body
		}
	}
	return nil
}

// enclosingStepName is the literal Name of the innermost Step{} on the stack.
func enclosingStepName(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		cl, ok := stack[i].(*ast.CompositeLit)
		if !ok {
			continue
		}
		if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "Step" {
			continue
		}
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Name" {
				if bl, ok := kv.Value.(*ast.BasicLit); ok {
					if v, err := strconv.Unquote(bl.Value); err == nil {
						return v
					}
				}
			}
		}
	}
	return ""
}

// resolvingCargoCalls is goCargoCalls minus what resolves nothing, with a floor so
// the callers cannot pass by reading nothing.
func resolvingCargoCalls(t *testing.T) []goCargoCall {
	t.Helper()
	var out []goCargoCall
	for _, c := range goCargoCalls(t) {
		if sub, ok := cargoSubcommand(c.Args); ok && !cargoLeavesTheLockAlone[sub] {
			out = append(out, c)
		}
	}
	// build-rust, test, build-embedder, build-sparse, build-serve: five on
	// 2026-09-11. The floor is one, not five, so removing a step is not a test
	// failure; reading none is.
	if len(out) == 0 {
		t.Fatalf("found no dependency-resolving cargo command in internal/goal; the reader is stale " +
			"and every assertion over it would pass while checking nothing")
	}
	return out
}

// TestEveryCargoCommandThePipelineRunsIsLocked fails when a step in internal/goal
// runs a dependency-resolving cargo command without --locked.
//
// THE DEFECT THIS PINS. rust/discover/Cargo.lock was rewritten mid-step on
// 2026-09-10 by a cargo run that was free to re-resolve. #323 locked build-rust and
// test, 6cafdc7 locked build-image.sh, and three steps were left running a bare
// `cargo build`: build-embedder, build-sparse and build-serve. The last two build
// rust/embed-core against rust/embed-core/Cargo.lock, the lockfile publish has
// fingerprinted since 6cafdc7, so a re-resolution there would rewrite a file the
// image's fingerprint hashes, as a side effect of a Tool, and ship versions no
// commit names.
//
// It reads the SOURCE of every non-test file here, not a list of steps, so the
// next cargo command a step grows is judged the day it is written.
func TestEveryCargoCommandThePipelineRunsIsLocked(t *testing.T) {
	for _, c := range resolvingCargoCalls(t) {
		if !cargoIsLocked(c.Args) {
			t.Errorf("%s (step %q) runs `cargo %s` without --locked: cargo may re-resolve and rewrite the "+
				"lockfile as a side effect of the build, the drift that moved rust/discover/Cargo.lock on "+
				"2026-09-10", c.Pos, c.Step, strings.Join(c.Args, " "))
		}
	}
}

// TestEveryLockedCargoBuildDeclaresTheLockfileItObeys fails when a step runs cargo
// on a manifest whose Cargo.toml, or whose Cargo.lock, is not in that step's Impl.
//
// --locked is only loud if the step runs. build-sparse and build-serve declared
// rust/embed-core/src and neither file cargo resolves from: an ort bump in the
// manifest, or a `cargo update` that rewrote the lockfile, left both Tools
// "fingerprint unchanged" with binaries linked against the previous versions, and
// --locked never got the chance to refuse anything. The lockfile is the one cargo
// obeys: the nearest Cargo.lock above the crate (its own for embed-core, which
// rust/Cargo.toml excludes from the workspace).
//
// A manifest that is not a literal (build-rust's come from the rustBins map) is
// not judged here; build-rust declares the whole of rust/.
func TestEveryLockedCargoBuildDeclaresTheLockfileItObeys(t *testing.T) {
	root := repoRootForTest()
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	judged := 0
	for _, c := range resolvingCargoCalls(t) {
		manifest := flagValue(c.Args, "--manifest-path")
		if manifest == "" || manifest == "<expr>" {
			continue
		}
		s := byName[c.Step]
		if s == nil {
			t.Errorf("%s runs `cargo %s` inside no step this test can name (%q): it cannot tell whose "+
				"fingerprint has to cover %s", c.Pos, strings.Join(c.Args, " "), c.Step, manifest)
			continue
		}
		judged++
		want := []string{manifest}
		if lock := nearestLockfile(root, path.Dir(manifest)); lock != "" {
			want = append(want, lock)
		} else {
			t.Errorf("%s: no Cargo.lock above %s decides what `cargo %s` resolves", c.Pos, manifest, c.Sub())
		}
		for _, w := range want {
			if !declaresPath(s.Impl, w) {
				t.Errorf("%s: step %s runs `cargo %s` on %s and does not fingerprint %s: a change there leaves "+
					"it SKIPping with a binary built from the previous versions, and --locked never runs to "+
					"refuse it", c.Pos, s.Name, strings.Join(c.Args, " "), manifest, w)
			}
		}
	}
	if judged == 0 {
		t.Fatal("judged no cargo command with a literal --manifest-path; the reader is stale")
	}
}

// Sub is the call's cargo subcommand, for messages.
func (c goCargoCall) Sub() string {
	sub, _ := cargoSubcommand(c.Args)
	return sub
}
