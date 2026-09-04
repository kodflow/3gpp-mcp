package goal

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUndecidable marks a validation that reached NO verdict — not "the output is
// bad", but "I could not look at it".
//
// The distinction is the whole point of this file, and it is worth stating why it
// needed one. Every Validate in this pipeline answers a yes/no question about an
// artefact, and the planner reads a non-nil error as no: the step is invalid, so
// it runs again. That is right when the artefact is missing, truncated or
// unreadable. It is catastrophically wrong when the artefact could not be OPENED
// because another process has it, because then "no" is a guess, and the pipeline
// guesses in the destructive direction.
//
// Measured 2026-09-04 on this machine. `paragraphs` validates by running
// `migrate-paragraphs --verify`, which opens data/3gpp.duckdb exclusively and
// holds it for minutes. Interrupting the run orphaned that child, the orphan kept
// the handle, and the next plan read:
//
//	[RUN ] seed  validation failed: the seeded DB does not open: command failed
//	             (exit 1): ...\dbcount.exe --db ...\data\3gpp.duckdb
//
// with discover, fetch, ingest, merge, embed, enrich, paragraphs and sparse
// conditional behind it — ten heavy steps, and `seed` at the front of them, whose
// job is to DOWNLOAD AND REPLACE the corpus. 21 GB of perfectly valid data was
// one confirmation away from being re-acquired because a dead process still held
// a file handle.
//
// The eleven defects fixed the night before this one cost time. This one costs
// the corpus, so the rule here is different: a step that cannot reach a verdict
// says so and stops the plan. It does not fall back on the answer that destroys
// something.
var ErrUndecidable = errors.New("validation could not decide")

// Undecidable reports whether err means "no verdict", as opposed to "invalid".
func Undecidable(err error) bool { return errors.Is(err, ErrUndecidable) }

// lockMarkers are DuckDB's OWN words for "someone else has this file". They are
// matched instead of the operating system's message on purpose: the Windows text
// under them is localised — this machine emits "Le processus ne peut pas accéder
// au fichier car ce fichier est utilisé par un autre processus" — and a guard
// that only fires in English is a guard that silently does not fire.
//
// The first two are followed by the holder's own identity, which is the most
// useful thing in the whole message: DuckDB names the executable AND its PID, so
// the operator gets `taskkill /PID <n> /F` handed to them rather than a hunt.
var lockMarkers = []string{
	"File is already open in",     // Windows
	"Conflicting lock is held in", // POSIX
	"Could not set lock on file",  // POSIX, when the holder cannot be identified
}

// heldElsewhere reports who holds the file, when THAT is why a command failed.
//
// It reads the whole error, tail included: c.Output puts the child's stderr in
// ExecError.Tail and ExecError.Error() renders it, which is where DuckDB's
// message actually lives.
func heldElsewhere(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	msg := err.Error()
	for _, marker := range lockMarkers {
		i := strings.Index(msg, marker)
		if i < 0 {
			continue
		}
		// The holder is the first non-empty line after the marker. DuckDB puts a
		// newline between the two, so this is not the same line.
		for _, line := range strings.Split(msg[i+len(marker):], "\n") {
			if who := strings.TrimSpace(line); who != "" {
				return who, true
			}
		}
		return "another process", true
	}
	return "", false
}

// stillOpenElsewhere turns "I could not open <what>" into ErrUndecidable when the
// reason is a lock, and leaves every other failure exactly as it was.
//
// `what` names the artefact, because the message has to be actionable at 3am by
// someone who did not write this: which file, who has it, and what to do.
func stillOpenElsewhere(what string, err error) error {
	if err == nil {
		return nil
	}
	who, locked := heldElsewhere(err)
	if !locked {
		return err
	}
	return fmt.Errorf(
		"%w: %s is open in another process (%s), so nothing here can say whether it is valid.\n"+
			"  Nothing is wrong with the corpus — end that process and re-plan.\n"+
			"  A stale holder is usually an orphaned child of an interrupted run.",
		ErrUndecidable, what, who)
}
