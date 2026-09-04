package goal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// measuredWindowsLock is what dbcount printed on this machine, verbatim, at
// 02:29 on 2026-09-04, while an orphaned "migrate-paragraphs --verify" still held
// the corpus. It is kept as a fixture rather than paraphrased because this whole
// guard is a claim about THIS text, and a paraphrase would let the guard drift
// away from the message it has to recognise.
//
// Note what is and is not usable in it. The middle sentence comes from the
// operating system and it is LOCALISED — this machine is French. The two lines
// that matter come from DuckDB itself, in English, and the second names the
// holder and its PID, which is the entire remedy.
const measuredWindowsLock = "dbcount: open duckdb read-only \"data/3gpp.duckdb\": " +
	"database/sql/driver: could not connect to database: IO Error: Cannot open file " +
	"\"C:\\Users\\Public\\3gpp-mcp\\data\\3gpp.duckdb\": Le processus ne peut pas " +
	"acc\u00e9der au fichier car ce fichier est utilis\u00e9 par un autre processus.\n\n" +
	"File is already open in \n" +
	"C:\\Users\\Public\\3gpp-mcp\\.local\\bin\\migrate-paragraphs.exe (PID 1232)"

func TestTheHolderOfALockedCorpusIsNamed(t *testing.T) {
	who, locked := heldElsewhere(errors.New(measuredWindowsLock))
	if !locked {
		t.Fatal("the message DuckDB prints when another process holds the corpus was not recognised as a lock")
	}
	if !strings.Contains(who, "migrate-paragraphs.exe") || !strings.Contains(who, "1232") {
		t.Errorf("the holder must be named with its PID so the operator can end it; got %q", who)
	}
}

// The POSIX wordings too, so this does not quietly become a guard that only works
// on the one machine it was written on.
func TestThePosixLockWordingsAreRecognisedToo(t *testing.T) {
	cases := map[string]string{
		"conflicting lock names the holder": "IO Error: Could not set lock on file \"/data/3gpp.duckdb\": Conflicting lock is held in /usr/local/bin/server (PID 4711)",
		"holder cannot be identified":       "IO Error: Could not set lock on file \"/data/3gpp.duckdb\": Resource temporarily unavailable",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, locked := heldElsewhere(errors.New(msg)); !locked {
				t.Error("a POSIX lock message was not recognised, so this guard would be Windows-only")
			}
		})
	}
}

// THE NEGATIVE CONTROLS, and they are what keeps the pipeline able to build at
// all. Replacing a destructive false positive with a false negative that refuses
// to rebuild a genuinely broken corpus is not a fix, it is the same mistake
// pointing the other way. Every case here must stay a REAL validation failure.
func TestARealFailureIsStillAFailure(t *testing.T) {
	cases := map[string]string{
		"no corpus at all":            "dbcount: open duckdb read-only \"data/3gpp.duckdb\": no such file or directory",
		"a truncated database":        "dbcount: IO Error: The file \"data/3gpp.duckdb\" exists but it is not a valid DuckDB database file",
		"a schema with no rows":       "dbcount: Catalog Error: Table with name clauses does not exist",
		"the binary itself is absent": "exec: dbcount: executable file not found in PATH",
		"a plain non-zero exit":       "command failed (exit 1): dbcount.exe --db data/3gpp.duckdb",
		"an empty message":            "",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			base := errors.New(msg)
			if _, locked := heldElsewhere(base); locked {
				t.Fatal("a real corpus failure was excused as a lock; the corpus would never be rebuilt")
			}
			got := stillOpenElsewhere("3gpp.duckdb", base)
			if Undecidable(got) {
				t.Error("a real corpus failure became cannot-decide, so the step would never run again")
			}
			if !errors.Is(got, base) {
				t.Errorf("the original failure was not preserved: %v", got)
			}
		})
	}
	if stillOpenElsewhere("3gpp.duckdb", nil) != nil {
		t.Error("a validation that passed was turned into a failure")
	}
}

func TestALockedCorpusIsUndecidableNotInvalid(t *testing.T) {
	err := stillOpenElsewhere("3gpp.duckdb", errors.New(measuredWindowsLock))
	if !Undecidable(err) {
		t.Fatal("a locked corpus did not produce ErrUndecidable, so the planner would schedule a rebuild")
	}
	for _, want := range []string{"3gpp.duckdb", "migrate-paragraphs.exe", "PID 1232", "re-plan"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not carry %q, so it is not actionable:\n%v", want, err)
		}
	}
}

// TestAnUndecidableValidationStopsThePlanInsteadOfSchedulingWork is the end of
// the chain, and the reason the rest of this file exists.
//
// The step here has the shape of seed: it succeeded, its fingerprint still
// matches, its output is on disk — and its validation cannot open that output
// because something else holds it. The planner used to read that as "invalid" and
// schedule the step, whose real Run downloads and REPLACES 21 GB of corpus.
func TestAnUndecidableValidationStopsThePlanInsteadOfSchedulingWork(t *testing.T) {
	ctx, store := newTestCtx(t)
	write(t, filepath.Join(ctx.Root, "src", "seed.go"), "package seed")

	var runs int
	locked := false
	s := counter("seed", nil, []string{"src/seed.go"}, "out-seed", &runs)
	s.Validate = func(c *Ctx) error {
		if !locked {
			return nil
		}
		return stillOpenElsewhere("3gpp.duckdb", errors.New(measuredWindowsLock))
	}

	r, err := NewRunner([]*Step{s}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("the first build did not produce the corpus (runs=%d)", runs)
	}

	// Now something else grabs the file. Nothing about the corpus has changed.
	locked = true
	if _, err := r.Plan(nil); err == nil {
		t.Fatal("the plan carried on over a corpus it could not read — this is where 21 GB gets re-downloaded")
	} else if !strings.Contains(err.Error(), "migrate-paragraphs.exe") {
		t.Errorf("the plan failed without naming the process holding the file: %v", err)
	}
	if _, err := r.Execute(nil, false); err == nil {
		t.Fatal("execution proceeded past an undecidable validation")
	}
	if runs != 1 {
		t.Fatalf("seed RE-RAN over a corpus that was merely locked (runs=%d) — the corpus was replaced", runs)
	}

	// And when the holder goes away the pipeline carries on with no rebuild: a
	// transient condition must leave no trace at all.
	locked = false
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("seed re-ran after the lock cleared (runs=%d)", runs)
	}
}

// TestNoValidationOpensACorpusWithoutTheLockGuard sweeps the SOURCE rather than
// trusting that the call sites edited by hand were all of them.
//
// That is the lesson of the night before this one: the same defect was fixed in
// front of whoever found it, eleven times over, because nobody looked for the
// rest of it. So this reads every Validate body in the package and fails when one
// launches a binary that opens a corpus without routing the failure through
// stillOpenElsewhere — including a Validate added next month by someone who never
// read this file.
//
// embedReport counts as guarded: it wraps the failure itself, on behalf of the
// four steps that call it.
func TestNoValidationOpensACorpusWithoutTheLockGuard(t *testing.T) {
	opensACorpus := []string{"dbcount", "migrate-paragraphs", "embed-io", "freeze-hnsw"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range validateBodies(string(src)) {
			hit := ""
			for _, tool := range opensACorpus {
				if strings.Contains(body, "\""+tool+"\"") {
					hit = tool
				}
			}
			if hit == "" {
				continue
			}
			checked++
			if !strings.Contains(body, "stillOpenElsewhere") && !strings.Contains(body, "embedReport") {
				t.Errorf("%s: a Validate launches %s but never classifies a lock — a corpus "+
					"another process holds would be reported as invalid and the step rescheduled:\n%s",
					f, hit, body)
			}
		}
	}
	if checked == 0 {
		t.Fatal("this sweep found no corpus-opening validation at all; it has stopped testing anything")
	}
}

// validateBodies returns the source of every Validate closure, by brace depth.
// Crude on purpose: a parser would be more correct, and this only has to be right
// about balanced braces in gofmt-ed code.
func validateBodies(src string) []string {
	const marker = "Validate: func(c *Ctx) error {"
	var out []string
	for i := 0; ; {
		j := strings.Index(src[i:], marker)
		if j < 0 {
			return out
		}
		start := i + j + len(marker)
		depth := 1
		k := start
		for ; k < len(src) && depth > 0; k++ {
			switch src[k] {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		out = append(out, src[start:k])
		i = k
	}
}
