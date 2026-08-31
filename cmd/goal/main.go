// Command goal drives the local corpus pipeline as a resumable state machine.
//
//	goal plan          show what would run, and why, without running it
//	goal run           execute the plan (this is what `make goal` calls)
//	goal status        what is valid right now, from persisted state only
//	goal invalidate X  forget step X so it (and everything downstream) replays
//	goal manifest      emit the machine-readable provenance of the current build
//
// Everything it needs comes from git, .local/state and the outputs themselves —
// never from an agent's memory of a previous session. A fresh process can always
// answer "what is valid, what changed, and what is the first thing that actually
// needs doing".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kodflow/3gpp-mcp/internal/goal"
)

var Version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n\033[31mgoal: %v\033[0m\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("goal", flag.ContinueOnError)
	var (
		floor      = fs.String("floor", env("GOAL_FLOOR", "Rel-99"), "lowest 3GPP release to index (Rel-99 = every real release)")
		scope      = fs.String("scope", env("GOAL_SCOPE", ""), "explicit series scope, space separated (empty = automatic delta)")
		jobs       = fs.String("jobs", env("GOAL_JOBS", "4"), "conversion workers (LibreOffice is RAM-hungry)")
		embedFloor = fs.String("embed-floor", env("GOAL_EMBED_FLOOR", "Rel-99"), "embed clauses at or above this release")
		etsiScope  = fs.String("etsi-scope", env("GOAL_ETSI_SCOPE", ""), "ETSI deliverables to index: empty = the built-in LI suite; 'all' = the whole /deliver archive (latest version of each); 'all-versions' = the whole archive with EVERY published version, the analogue of keeping every 3GPP release; else a comma-separated id list")
		dataDir    = fs.String("data", env("GOAL_DATA", ""), "corpus/DB directory (default <repo>/data)")
		full       = fs.Bool("full", false, "ignore the delta anchor and reindex everything")
		repair     = fs.Bool("repair", false, "fetch only the repair set: upstream drift UNION corpus holes (proportionate, ~1k specs vs ~20k)")
		dry        = fs.Bool("dry-run", false, "decide but do not execute")
		only       = fs.String("only", "", "restrict to these steps, comma separated (preconditions are still checked)")
		forceOnly  = fs.Bool("force-only", false, "run the selected steps even when their preconditions are unmet — loudly, and the result is not reproducible")
		from       = fs.String("from", "", "run this step and everything after it")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "usage: goal <plan|run|status|invalidate|manifest> [flags]\n\n")
		fs.PrintDefaults()
	}
	args := os.Args[1:]
	if len(args) == 0 {
		fs.Usage()
		return fmt.Errorf("a subcommand is required")
	}
	sub := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	local := filepath.Join(root, ".local")
	data := *dataDir
	if data == "" {
		data = filepath.Join(root, "data")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Ctrl-C must stop the child cleanly so the running step records its
	// checkpoint instead of leaving a half-written state file behind.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "\ngoal: interrupted — the current step will record its checkpoint")
		cancel()
	}()

	store, err := goal.NewStore(local)
	if err != nil {
		return err
	}

	gctx := &goal.Ctx{
		Context: ctx,
		Root:    root,
		Local:   local,
		Data:    data,
		Config: map[string]string{
			"floor":          *floor,
			"scope":          *scope,
			"jobs":           *jobs,
			"embed_floor":    *embedFloor,
			"etsi_scope":     *etsiScope,
			"model_dir":      filepath.Join(data, "models", "bge-m3"),
			"contract_flags": dataContractFlags(root),
			"full":           boolStr(*full),
			"repair":         boolStr(*repair),
		},
	}

	steps := goal.Pipeline()
	runner, err := goal.NewRunner(steps, gctx, store, func() string { return toolchainIdentity(root) })
	if err != nil {
		return err
	}

	runner.ForceOnly = *forceOnly
	selection, err := selectSteps(runner, *only, *from)
	if err != nil {
		return err
	}

	switch sub {
	case "plan":
		decisions, err := runner.Plan(selection)
		if err != nil {
			return err
		}
		printPlan(decisions)
		return nil

	case "run":
		lock, err := goal.AcquireLock(local, "goal run")
		if err != nil {
			return err
		}
		defer lock.Release()

		decisions, err := runner.Plan(selection)
		if err != nil {
			return err
		}
		printPlan(decisions)

		res, runErr := runner.Execute(selection, *dry)
		printSummary(res)
		if runErr != nil {
			return runErr
		}
		return nil

	case "status":
		return printStatus(runner, store, gctx)

	case "invalidate":
		names := fs.Args()
		if len(names) == 0 {
			return fmt.Errorf("invalidate needs at least one step name")
		}
		for _, n := range names {
			if err := store.Forget(n); err != nil {
				return err
			}
			fmt.Printf("forgot %s (it and everything downstream will replay)\n", n)
		}
		return nil

	case "manifest":
		return printManifest(runner, store, gctx)

	default:
		fs.Usage()
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

// selectSteps turns --only / --from into a set, validating names so a typo does
// not silently run nothing.
func selectSteps(r *goal.Runner, only, from string) (map[string]bool, error) {
	known := map[string]bool{}
	for _, s := range r.Steps() {
		known[s.Name] = true
	}
	switch {
	case only != "":
		set := map[string]bool{}
		for _, n := range strings.Split(only, ",") {
			n = strings.TrimSpace(n)
			if !known[n] {
				return nil, fmt.Errorf("unknown step %q", n)
			}
			set[n] = true
		}
		return set, nil
	case from != "":
		if !known[from] {
			return nil, fmt.Errorf("unknown step %q", from)
		}
		set := map[string]bool{}
		seen := false
		for _, n := range r.Order() {
			if n == from {
				seen = true
			}
			if seen {
				set[n] = true
			}
		}
		return set, nil
	}
	return nil, nil
}

func printPlan(ds []goal.Decision) {
	fmt.Println("\n\033[1mGOAL PLAN\033[0m")
	fmt.Println()
	heavy := 0
	for _, d := range ds {
		mark := "\033[2mSKIP\033[0m"
		if d.Action == goal.ActionRun {
			mark = "\033[33mRUN \033[0m"
			if d.Step.Heavy {
				heavy++
			}
		}
		fmt.Printf("  [%s] %-16s %s\n", mark, d.Step.Name, d.Reason)
	}
	fmt.Printf("\n  %d step(s), %d heavy step(s) to execute\n\n", len(ds), heavy)
}

func printSummary(res *goal.Result) {
	if res == nil {
		return
	}
	fmt.Printf("\n\033[1mSUMMARY\033[0m\n")
	fmt.Printf("  ran        %d  %s\n", len(res.Ran), strings.Join(res.Ran, " "))
	fmt.Printf("  skipped    %d  %s\n", len(res.Skipped), strings.Join(res.Skipped, " "))
	if len(res.Failed) > 0 {
		fmt.Printf("  failed     %d  %s\n", len(res.Failed), strings.Join(res.Failed, " "))
	}
	fmt.Printf("  heavy run  %d\n", res.HeavyRan)
	fmt.Printf("  elapsed    %.1fs\n\n", float64(res.TotalTimeMs)/1000)
}

func printStatus(r *goal.Runner, store *goal.Store, c *goal.Ctx) error {
	recs, err := store.All()
	if err != nil {
		return err
	}
	fmt.Println("\n\033[1mPROJECT GOAL STATUS\033[0m")
	commit, dirty := gitState(c.Root)
	fmt.Printf("\nRepository\n  commit:   %s\n  dirty:    %s\n", commit, dirty)
	fmt.Printf("\nSteps\n")
	reached := true
	for _, name := range r.Order() {
		rec := recs[name]
		switch {
		case rec == nil:
			fmt.Printf("  %-16s \033[2mnever run\033[0m\n", name)
			reached = false
		case rec.Status == goal.StatusSuccess:
			fmt.Printf("  %-16s \033[32mVALID\033[0m    %s  %s\n", name, rec.Fingerprint, goalDuration(rec.DurationSec))
		default:
			fmt.Printf("  %-16s \033[31m%s\033[0m   %s\n", name, strings.ToUpper(string(rec.Status)), firstLine(rec.Error))
			reached = false
		}
	}
	fmt.Println()
	if reached {
		fmt.Println("\033[32mGOAL: all steps valid\033[0m")
	} else {
		fmt.Println("\033[33mGOAL: not reached — run `goal plan` to see what is missing\033[0m")
	}
	fmt.Println()
	return nil
}

// Manifest is the machine-readable provenance of a local build.
type Manifest struct {
	SourceCommit string                  `json:"source_commit"`
	Dirty        bool                    `json:"dirty"`
	Steps        map[string]*goal.Record `json:"steps"`
	Config       map[string]string       `json:"config"`
	Toolchain    map[string]string       `json:"toolchain,omitempty"`
	Counters     map[string]string       `json:"counters,omitempty"`
}

func printManifest(r *goal.Runner, store *goal.Store, c *goal.Ctx) error {
	recs, err := store.All()
	if err != nil {
		return err
	}
	commit, dirty := gitState(c.Root)
	m := Manifest{
		SourceCommit: commit,
		Dirty:        dirty != "no",
		Steps:        recs,
		Config:       c.Config,
	}
	if b, err := os.ReadFile(filepath.Join(c.Local, "state", "toolchain.json")); err == nil {
		_ = json.Unmarshal(b, &m.Toolchain)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	out := filepath.Join(c.Local, "manifest.json")
	if err := goal.WriteAtomic(out, append(b, '\n')); err != nil {
		return err
	}
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "\nwritten to %s\n", out)
	return nil
}

// ------------------------------------------------------------------ helpers

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	// Fall back to walking up for go.mod, so the tool still works in a tarball.
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside the repository (no go.mod found)")
		}
		dir = parent
	}
}

func gitState(root string) (string, string) {
	commit := "unknown"
	if out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	dirty := "no"
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output(); err == nil {
		if n := len(strings.Fields(strings.TrimSpace(string(out)))); n > 0 {
			dirty = fmt.Sprintf("yes (%d entries)", len(strings.Split(strings.TrimSpace(string(out)), "\n")))
		}
	}
	return commit, dirty
}

// dataContractFlags asks scripts/data-contract.sh, which the ADR designates as
// the single source of the completeness contract. Duplicating its logic here is
// exactly the drift the ADR exists to prevent.
func dataContractFlags(root string) string {
	out, err := exec.Command("bash", filepath.Join(root, "scripts", "data-contract.sh")).Output()
	if err != nil {
		return "--require-fts --require-hnsw --require-embed-complete"
	}
	return strings.TrimSpace(string(out))
}

func toolchainIdentity(root string) string {
	out, err := exec.Command("bash", "-c",
		"source "+filepath.Join(root, "scripts", "local", "toolchain-env.sh")+" >/dev/null 2>&1; toolchain_identity").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func goalDuration(sec float64) string {
	if sec <= 0 {
		return ""
	}
	if sec < 60 {
		return fmt.Sprintf("%.1fs", sec)
	}
	return fmt.Sprintf("%dm%02ds", int(sec)/60, int(sec)%60)
}
