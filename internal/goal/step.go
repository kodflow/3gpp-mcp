// Package goal is the resumable state machine that drives the local corpus
// pipeline.
//
// # Why this exists
//
// The corpus is built by a chain of expensive steps — download, LibreOffice
// conversion, parse/ingest, merge, GPU embedding, index freeze. Any of them can
// take hours, and any of them can be interrupted (an agent killed, a terminal
// closed, a machine rebooted, a later step failing). Replaying a finished step
// "just to be sure" costs hours of GPU or a full re-download; skipping a step
// that SHOULD have been replayed silently serves an index that no longer matches
// its data. Both failure modes are unacceptable, so neither "always run" nor
// "run once" is a correct default.
//
// # The contract
//
// Every step is identified by a deterministic FINGERPRINT computed from exactly
// what can change its result — its own implementation, its declared inputs, its
// configuration, the fingerprints of its dependencies, and (only where it
// matters) the toolchain or model identity. A step is skipped if and only if all
// four hold:
//
//	the previous run succeeded
//	AND the previous fingerprint equals the current one
//	AND every declared output still exists
//	AND the output passes its cheap validation
//
// A present file is not proof. A timestamp is not proof. An old "success" is not
// proof. Only the conjunction is.
//
// # Invalidation is transitive and minimal
//
// Changing a step invalidates that step and everything downstream of it, and
// nothing else. A change to cmd/server never invalidates the embeddings; a model
// change never invalidates the downloaded corpus.
package goal

import (
	"context"
	"errors"
	"time"
)

// ErrDeclined is returned by a Run that deliberately did nothing and therefore
// produced none of its declared Outputs. It is not a failure and not work to
// retry: the step looked, found the conditions for its work absent, and said so.
//
// The runner records a declined step as a success with no outputs and SKIPS the
// output and validation gates, because those gates describe a run that did the
// work. Without this, `seed` — which declines when no GHCR credential is present
// so the corpus is built from 3gpp.org instead — was recorded as
// "declared output missing after a successful run", and every step depending on
// it was blocked. The documented no-credential path could not run at all.
//
// The next run still re-evaluates a declined step: its outputs are absent, so
// the plan marks it stale. A credential that appears later is picked up.
var ErrDeclined = errors.New("step declined: nothing to do")

// Declined reports whether err is a deliberate decline rather than a failure.
func Declined(err error) bool { return errors.Is(err, ErrDeclined) }

// Status is the terminal state of a step's last attempt.
type Status string

const (
	// StatusSuccess means the step completed AND its outputs validated. Only a
	// success record can make a later run skip.
	StatusSuccess Status = "success"
	// StatusFailed means the step ran and did not complete. The record is kept
	// (with its error and checkpoint) so the next run can resume rather than
	// restart, and so the failure is diagnosable after the process is gone.
	StatusFailed Status = "failed"
	// StatusRunning is written before the work starts. A record left in this
	// state means the process died mid-step: never trusted, always replayed.
	StatusRunning Status = "running"
)

// Action is what the planner decided to do with a step.
type Action string

const (
	ActionRun  Action = "RUN"
	ActionSkip Action = "SKIP"
)

// Step declares one unit of the pipeline. The declaration is data: what the step
// depends on, what defines it, what it produces, how to check what it produced.
// Run is the only executable part, and it must be re-entrant — it can be called
// again after an interruption and is expected to resume from its own checkpoint.
type Step struct {
	// Name is the stable identifier, also the state file name. Never reuse a
	// name for different work: the state file would be misinterpreted.
	Name string

	// Version is bumped by hand when the step's MEANING changes in a way the
	// implementation hash cannot see (a new external contract, a corrected
	// interpretation of an input). Bumping it forces exactly one replay.
	Version int

	// Deps are the names of steps that must be materialised first. Their
	// fingerprints fold into this step's fingerprint, which is what makes
	// invalidation transitive without any extra bookkeeping.
	Deps []string

	// AnyDeps are ALTERNATIVE producers of the same artefact: at least one must
	// have succeeded, and it does not matter which.
	//
	// This exists because `data/3gpp.duckdb` has two legitimate producers. `merge`
	// folds locally-ingested shards into it; `seed` downloads the published
	// snapshot instead, which is the whole point of seeding — it buys the corpus
	// without 37 GB of archives and ~30 h of LibreOffice. Declaring only `merge`
	// made the graph state something false: that a seeded corpus can never be
	// vectorised. The practical consequence was worse than the inaccuracy —
	// operators reached `embed` with `--only`, which does not merely reorder the
	// plan, it skips dependency checking entirely. The state file recorded
	// `"merge": "missing"` and nothing objected. A graph that has to be bypassed
	// to do a supported thing trains people to bypass it.
	//
	// Semantics, deliberately narrow:
	//   - ordering: every alternative sorts before this step, as a Dep would;
	//   - satisfaction: at least one must have SUCCEEDED, else the step refuses
	//     to run rather than operating on an artefact nobody produced;
	//   - dirtiness: any alternative re-running makes this step dirty, because
	//     any of them can rewrite the shared artefact;
	//   - fingerprint: ALL of them fold in, exactly as Deps do. A seeded corpus
	//     and a merged one are different corpora, so switching producer must
	//     replay the step.
	AnyDeps []string

	// Impl lists the repo-relative files and directories whose CONTENT defines
	// this step. This is the precision the mission demands: the embed step must
	// not be invalidated by a change to the MCP server, and the fetch step must
	// not be invalidated by a change to the README. Directories are walked;
	// vendored, generated and state directories are skipped (see implHash).
	Impl []string

	// ExcludeTests drops _test.go files and testdata/ from the implementation
	// hash. `go build` does not compile them, so for a BUILD step they are not
	// determinants: editing a test must not relink eight binaries. The `test`
	// step deliberately leaves this off.
	ExcludeTests bool

	// Inputs returns the data files this step consumes. Unlike Impl these are
	// large and numerous, so they are fingerprinted by size+mtime rather than by
	// content — hashing ~37 GB of converted HTML would cost more than the
	// ingestion it guards. Returning an error aborts planning: a step whose
	// inputs cannot be enumerated cannot be honestly skipped.
	Inputs func(ctx *Ctx) ([]string, error)

	// Extra folds arbitrary determinants into the fingerprint — an embed
	// identity, a release floor, a model revision. Keep it to values that truly
	// change the result.
	Extra func(ctx *Ctx) (map[string]string, error)

	// Outputs are the paths that must exist for a success record to be trusted.
	Outputs func(ctx *Ctx) []string

	// Validate is a CHEAP check that the outputs are real, not merely present —
	// a DuckDB that opens, a JSONL whose last line parses, a non-empty index. It
	// runs on every plan, including for steps that would otherwise be skipped,
	// which is what makes a corrupted or truncated output invalidate its step
	// instead of being trusted because its fingerprint matched.
	Validate func(ctx *Ctx) error

	// Run does the work. It must be re-entrant and should checkpoint internally
	// for anything long enough to be interrupted (see the fetch and embed steps).
	Run func(ctx *Ctx) error

	// Heavy marks a step whose execution costs real time or GPU. The no-op proof
	// asserts that a second identical run executes ZERO heavy steps; cheap
	// validations may legitimately re-run every time.
	Heavy bool

	// Toolchain folds the compiler/runtime identity into the fingerprint. Set it
	// on build steps only: a Go upgrade must rebuild the server, but it must NOT
	// re-download the corpus or re-run the GPU.
	Toolchain bool

	// Optional marks a step whose failure does not fail the goal. It must be an
	// explicit, reviewed property — never an inline "|| true" that hides a real
	// failure. A failed optional step is still recorded as failed and reported.
	Optional bool

	// Doc is a one-line explanation shown in the plan.
	Doc string
}

// Record is what a step persists. It is the only thing a fresh agent, with no
// memory of any previous session, reads to decide what is already valid.
type Record struct {
	Step        string            `json:"step"`
	StepVersion int               `json:"step_version"`
	Status      Status            `json:"status"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at,omitempty"`
	Fingerprint string            `json:"fingerprint"`
	Deps        map[string]string `json:"dependencies,omitempty"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Outputs     map[string]string `json:"outputs,omitempty"`
	Validation  map[string]string `json:"validation,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Impl        map[string]string `json:"implementation,omitempty"`
	Error       string            `json:"error,omitempty"`
	// Declined marks a step that ran, found nothing to do, and produced none
	// of its outputs on purpose. Recorded as a success so dependants proceed,
	// flagged so the run report does not read as if the work happened.
	Declined bool `json:"declined,omitempty"`
	// Checkpoint is free-form per-step resume detail (how many items done, which
	// shard was in flight). It survives a failure so the next run resumes.
	Checkpoint map[string]string `json:"checkpoint,omitempty"`
	// LogFile points at the full stdout/stderr of the attempt.
	LogFile string `json:"log_file,omitempty"`
	// DurationSec is kept for the performance report.
	DurationSec float64 `json:"duration_sec,omitempty"`
}

// Decision is the planner's verdict for one step, with the reason shown to the
// operator. The reason is not decoration: "why is this running?" is the first
// question after an unexpected rebuild.
type Decision struct {
	Step        *Step
	Action      Action
	Reason      string
	Fingerprint string
	Previous    *Record
}

// Ctx carries the paths and resolved configuration a step needs. It is passed to
// every callback so steps never reach for globals and stay unit-testable.
type Ctx struct {
	Context context.Context

	// Root is the repository root.
	Root string
	// Local is .local — runtime state, logs, locks, toolchain. Never versioned.
	Local string
	// Data is where the corpus, shards, DB and models live. Configurable so a
	// slow filesystem (a Windows mount seen from WSL) can be avoided.
	Data string

	// Config holds resolved knobs that belong in fingerprints (release floor,
	// embed floor, scope).
	Config map[string]string

	// Log is where the current step writes its output.
	Log *StepLog

	// record is the in-flight record, so Run can set checkpoints.
	record *Record
}

// Checkpoint records resume detail for the running step. It is flushed with the
// record, so an interrupted step leaves behind exactly where it stopped.
func (c *Ctx) Checkpoint(k, v string) {
	if c.record == nil {
		return
	}
	if c.record.Checkpoint == nil {
		c.record.Checkpoint = map[string]string{}
	}
	c.record.Checkpoint[k] = v
}

// Cfg reads a resolved configuration value.
func (c *Ctx) Cfg(k string) string { return c.Config[k] }
