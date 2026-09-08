package goal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The helper process: prints a line to stdout and exits non-zero, which is
// exactly what anchorcheck does when it finds holes.
func TestHelperReportsThenFails(t *testing.T) {
	if os.Getenv("GOAL_TEST_HELPER") != "1" {
		t.Skip("helper process, run only via TestOutputKeepsWhatAFailingCommandPrinted")
	}
	fmt.Println("over_claim=3 missing_content=2")
	os.Exit(1)
}

// A FAILING COMMAND'S OUTPUT IS ITS FINDING, and discarding it has cost twice.
//
// anchorcheck exits 1 to REPORT holes, one line per violation. While Output
// returned "" on any non-zero exit, the caller could not tell a tool that found
// real holes from a tool that never started — and both of those have happened
// here, on this binary, days apart. The exit code is a summary; the output is the
// finding.
func TestOutputKeepsWhatAFailingCommandPrinted(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Context = context.Background()

	out, err := c.Output(Cmd{
		Name: os.Args[0],
		Args: []string{"-test.run=TestHelperReportsThenFails"},
		Env:  []string{"GOAL_TEST_HELPER=1"},
	})
	if err == nil {
		t.Fatal("the helper exited 1 and Output reported success")
	}
	if !strings.Contains(out, "over_claim=3") {
		t.Errorf("Output dropped the failing command's own report: %q", out)
	}
}

// And the consequence at the place it matters: validateAnchor separates "the
// binary never started" from "the corpus is missing text" by asking whether
// anchorcheck said anything. That question is only answerable because Output no
// longer throws the answer away.
func TestValidateAnchorTellsAnUnrunToolFromARealFinding(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Context = context.Background()

	// No anchor at all: nothing to verify, and that is not a failure.
	if err := validateAnchor(c); err != nil {
		t.Errorf("with no anchor on disk validateAnchor must decline quietly: %v", err)
	}
}

var _ = exec.Command
