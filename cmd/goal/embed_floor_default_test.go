package main

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/goal"
)

// GOAL'S FLOOR IS goal.DefaultEmbedFloor, READ OFF THE FLAG ITSELF.
//
// The floor the pipeline embeds and validates at, and the floor build-image.sh
// applies when nothing sets one, were two literals — "Rel-99" here and
// ${EMBED_FLOOR:-Rel-99} there — equal by coincidence. Under goal the image now
// receives validate's floor explicitly (internal/goal/embed_floor.go), but a
// standalone `make image` still falls back to the script's. This holds goal's side
// to DefaultEmbedFloor through the real flag set; internal/goal's
// TestEveryEmbedFloorDefaultIsThePipelines holds the scripts to the same constant.
func TestTheEmbedFloorFlagDefaultsToThePipelineFloor(t *testing.T) {
	t.Setenv("GOAL_EMBED_FLOOR", "") // nothing set: `make build`
	fs, _ := newFlagSet()
	f := fs.Lookup("embed-floor")
	if f == nil {
		t.Fatal("goal declares no -embed-floor")
	}
	if f.DefValue != goal.DefaultEmbedFloor {
		t.Fatalf("goal -embed-floor defaults to %q, the pipeline's floor is %q: validate and the image would "+
			"hold an unconfigured build to different contracts", f.DefValue, goal.DefaultEmbedFloor)
	}

	t.Setenv("GOAL_EMBED_FLOOR", "Rel-19")
	fs, _ = newFlagSet()
	if got := fs.Lookup("embed-floor").DefValue; got != "Rel-19" {
		t.Fatalf("GOAL_EMBED_FLOOR=Rel-19 gives -embed-floor %q", got)
	}
}
