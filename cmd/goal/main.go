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
	"bytes"
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

// goalFlags are goal's command-line flags.
type goalFlags struct {
	floor, scope, jobs, embedFloor, etsiScope, dataDir, only, from *string
	full, repair, dry, forceOnly                                   *bool
}

// newFlagSet declares goal's flags. It is a function of its own so that a test can
// read the defaults goal actually applies (TestEveryEmbedFloorDefaultIsThePipelines
// holds --embed-floor's to the scripts' ${EMBED_FLOOR:-…}).
func newFlagSet() (*flag.FlagSet, goalFlags) {
	fs := flag.NewFlagSet("goal", flag.ContinueOnError)
	f := goalFlags{
		floor: fs.String("floor", env("GOAL_FLOOR", "Rel-99"), "lowest 3GPP release to index (Rel-99 = every real release)"),
		scope: fs.String("scope", env("GOAL_SCOPE", ""), "explicit series scope, space separated (empty = automatic delta)"),
		jobs:  fs.String("jobs", env("GOAL_JOBS", "4"), "conversion workers (LibreOffice is RAM-hungry)"),
		// ONE floor for embed, validate and the published image: publish passes the
		// floor validate applied to build-image.sh, and refuses an EMBED_FLOOR that
		// disagrees with it (internal/goal/embed_floor.go).
		embedFloor: fs.String("embed-floor", env("GOAL_EMBED_FLOOR", goal.DefaultEmbedFloor), "embed clauses at or above this release; validate and the published image are held to the same floor"),
		etsiScope:  fs.String("etsi-scope", env("GOAL_ETSI_SCOPE", ""), "ETSI deliverables to index: empty = the whole /deliver archive with EVERY published version (the analogue of keeping every 3GPP release); 'all' = the archive at the latest version of each; 'li-suite' = only the fourteen built-in Lawful-Interception deliverables; else a comma-separated id list"),
		dataDir:    fs.String("data", env("GOAL_DATA", ""), "corpus/DB directory (default <repo>/data)"),
		full:       fs.Bool("full", false, "ignore the delta anchor and reindex everything"),
		repair:     fs.Bool("repair", false, "require the proportionate work list (upstream drift UNION corpus holes) and fail if it cannot be computed; it is the DEFAULT wherever a corpus and an index exist"),
		dry:        fs.Bool("dry-run", false, "decide but do not execute"),
		only:       fs.String("only", "", "restrict to these steps, comma separated (preconditions are still checked)"),
		forceOnly:  fs.Bool("force-only", false, "run the selected steps even when their preconditions are unmet — loudly, and the result is not reproducible"),
		from:       fs.String("from", "", "run this step and everything after it"),
	}
	return fs, f
}

func run() error {
	fs, f := newFlagSet()
	var (
		floor, scope, jobs, embedFloor = f.floor, f.scope, f.jobs, f.embedFloor
		etsiScope, dataDir, only, from = f.etsiScope, f.dataDir, f.only, f.from
		full, repair, dry, forceOnly   = f.full, f.repair, f.dry, f.forceOnly
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
			"floor":               *floor,
			"scope":               *scope,
			"jobs":                *jobs,
			"embed_floor":         *embedFloor,
			"etsi_scope":          *etsiScope,
			"model_dir":           filepath.Join(data, "models", "bge-m3"),
			"contract_flags":      dataContractFlags(root, arm3GPP),
			"contract_flags_etsi": dataContractFlags(root, armETSI),
			"full":                boolStr(*full),
			"repair":              boolStr(*repair),
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
	// Close the selection over the BUILD steps it needs. `--only sparse` must
	// launch the sparse binary this checkout describes, not whatever was last
	// linked — see Runner.WithToolDeps for the five times that cost us a run.
	// They skip in milliseconds when nothing changed, so the plan stays honest
	// without becoming expensive.
	selection = runner.WithToolDeps(selection)

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
		// A RUN THAT CONTAINS A FAILURE MUST NOT EXIT 0.
		//
		// An Optional step that fails is logged and the run continues, which is the
		// right behaviour — the other steps are still worth doing. But Execute then
		// returns no error, so `goal run` exited 0 with "failed 1 sparse" printed
		// three lines above, and every caller that checks the exit code believed the
		// pipeline had succeeded: `make build`, a chained script, anything.
		//
		// Optional does not even mean what the swallow implies any more. runStep
		// already turns ErrDeclined — "this machine cannot do this, and that is
		// fine" — into a success, so the branch that continues is reached ONLY by a
		// step that ran and genuinely broke. Continuing past it is defensible;
		// claiming the run succeeded is not.
		if len(res.Failed) > 0 {
			return fmt.Errorf("%d step(s) failed: %s", len(res.Failed), strings.Join(res.Failed, " "))
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

// printPlan separates what WILL run from what will merely be re-examined. The
// two used to print identically, so a plan whose only certain work was a relink
// announced fifteen heavy steps and a corpus rebuild.
func printPlan(ds []goal.Decision) {
	fmt.Println("\n\033[1mGOAL PLAN\033[0m")
	fmt.Println()
	heavy, maybe, maybeHeavy := 0, 0, 0
	for _, d := range ds {
		mark := "\033[2mSKIP\033[0m"
		switch {
		case d.Action != goal.ActionRun:
		case d.Conditional:
			mark = "\033[2mRUN?\033[0m"
			maybe++
			if d.Step.Heavy {
				maybeHeavy++
			}
		default:
			mark = "\033[33mRUN \033[0m"
			if d.Step.Heavy {
				heavy++
			}
		}
		fmt.Printf("  [%s] %-16s %s\n", mark, d.Step.Name, d.Reason)
	}
	fmt.Printf("\n  %d step(s) examined, %d certain to run (%d heavy)\n", len(ds), len(ds)-countSkips(ds)-maybe, heavy)
	if maybe > 0 {
		fmt.Printf("  %d more (%d heavy) marked RUN? — each is decided against real state\n"+
			"  once its dependency has finished, and skipped if that dependency changed nothing.\n",
			maybe, maybeHeavy)
	}
	fmt.Println()
}

func countSkips(ds []goal.Decision) int {
	n := 0
	for _, d := range ds {
		if d.Action != goal.ActionRun {
			n++
		}
	}
	return n
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

// The two arms of the contract. They are the argument scripts/data-contract.sh
// takes, and they are named here so a typo is a compile error rather than a
// silently weaker gate: "3gp" makes the script exit 2, which sends
// dataContractFlags down its fallback path and past the very check it applies.
const (
	arm3GPP = "3gpp"
	armETSI = "etsi"
)

// dataContractFlags asks scripts/data-contract.sh, which the ADR designates as
// the single source of the completeness contract. Duplicating its logic here is
// exactly the drift the ADR exists to prevent.
//
// IT IS ASKED TWICE, ONCE PER CORPUS. `validate` and `validate-etsi` hold the
// two halves of the product to the SAME contract, and the flag string that
// expresses it comes from ONE file for both of them. The arms differ by exactly
// one flag -- --require-etsi, the only check about the PAIR rather than about a
// corpus -- and the script is what decides that, not this function.
//
// DATA_ETSI_DB IS SUPPLIED BECAUSE THE LAYOUTS DIFFER. --require-etsi takes the
// second corpus's PATH, and the script's default is the IMAGE's (/data/mcp-3gpp).
// A local build keeps it under the repo, so without this the ETSI half of the
// contract would point at a file that does not exist here and the gate would fail
// for the wrong reason.
//
// THE FALLBACK IS THE STRONG CONTRACT, NOT THE WEAK ONE. It used to return the
// dense-only flags, so anything that stopped the script from running — bash off
// the PATH, a bad exit — silently downgraded the gate that decides what gets
// published. Failing loudly on a corpus that is genuinely incomplete is the
// correct outcome; publishing an unchecked one is not. Loosening stays available
// through DATA_CONTRACT, where it is a decision someone typed.
//
// ...and a fallback that is never mentioned is the same defect one level down: it
// would decide the publish gate without leaving a trace. The failure is therefore
// printed, with the script's own stderr, which is where the reason lives.
//
// DATA_ETSI_DB IS A DEFAULT, NOT AN OVERRIDE. Appending it unconditionally would
// win over an operator who set it — later entries take precedence in exec's
// environment — so a corpus kept somewhere else would be checked at the repo path
// instead, silently. It is supplied only when absent.
//
// AND THE FALLBACK USES THE SAME PATH. Resolving it once, above both branches, is
// the point: a fallback that quietly reverted to the repo path would check a
// DIFFERENT corpus than the script would have, and only in the branch that already
// means something went wrong — the hardest case to notice and the worst one to be
// wrong in.
func dataContractFlags(root, arm string) string {
	etsi := filepath.Join(root, "data", "etsi.duckdb")
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "data-contract.sh"), arm)
	cmd.Env = os.Environ()
	if v, set := os.LookupEnv("DATA_ETSI_DB"); set && v != "" {
		etsi = v
	} else {
		cmd.Env = append(cmd.Env, "DATA_ETSI_DB="+etsi)
	}
	// THE WORK-LIST PATHS COME FROM HERE FOR THE SAME REASON DATA_ETSI_DB DOES,
	// and for one more: they must be OS paths, not the shell's.
	//
	// --require-worklist is read by cmd/validate, a Windows binary. Left to derive
	// its own default, the script computes it with `cd … && pwd` under the
	// toolchain's bash and emits "/c/Users/Public/3gpp-mcp/.local/state/…", which
	// no Windows process can open — the gate would fail on a file that is present,
	// which is the most confusing way for a check to be wrong. Passing
	// filepath.Join's result makes the path the same one every other step uses.
	//
	// Defaults, not overrides, exactly like DATA_ETSI_DB: an operator who set
	// either variable keeps it.
	//
	// ONE FILE, ONE KEY. The register has a WRITER (scripts/etsi-fetch.sh, which
	// reads ETSI_ABSENCES) and a READER (scripts/data-contract.sh, which reads
	// DATA_ETSI_ABSENCES). An operator who set only the writer's key would have the
	// fetch record its absences in one file while the gate looked for them in
	// another — and the gate does not fail on a register it cannot find, it reports
	// UNVERIFIED, so the divergence would read as "this corpus predates the
	// register" rather than as a misconfiguration. The writer's key therefore wins
	// here when it is set, which makes the two names one setting.
	absences := filepath.Join(root, ".local", "state", "etsi-absences.tsv")
	if v, set := os.LookupEnv("ETSI_ABSENCES"); set && v != "" {
		absences = v
	}
	for k, v := range map[string]string{
		"DATA_ETSI_WORKLIST": filepath.Join(root, ".local", "state", "etsi-worklist.tsv"),
		"DATA_ETSI_ABSENCES": absences,
	} {
		if got, set := os.LookupEnv(k); !set || got == "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"goal: scripts/data-contract.sh %s failed (%v) -- falling back to the strong contract; stderr: %s\n",
			arm, err, strings.TrimSpace(stderr.String()))
		// THE FALLBACK IS PER ARM TOO, for the reason the strong fallback exists at
		// all: a fallback that says something different from what the script would
		// have said decides the publish gate on a contract nobody wrote, and it does
		// so only in the branch that already means something is wrong. The ETSI arm
		// drops --require-etsi here for the reason the script drops it there.
		// --require-no-reingest is in the fallback because it is in the script. A
		// fallback that omits a check decides the publish gate on a weaker contract
		// than the one anybody wrote, and only in the branch that already means
		// something went wrong.
		strong := "--require-fts --require-hnsw --require-embed-complete --require-no-reingest --require-sparse"
		if arm == armETSI {
			return strong
		}
		return strong + " --require-etsi " + etsi
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
