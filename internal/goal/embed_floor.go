package goal

import (
	"fmt"
	"os"
	"strings"
)

// ONE EMBED FLOOR FOR THE PIPELINE AND THE IMAGE IT PUBLISHES.
//
// --require-embed-complete is floor-aware: it counts only the clauses at or above
// --embed-floor, and the only floor that makes it mean anything is the one embed
// ran with (see validateArgs). Until 2026-09-11 two knobs set it. `validate` applied
// the pipeline's (GOAL_EMBED_FLOOR, --embed-floor; config "embed_floor"), and
// build-image.sh re-derived the contract from its own ${EMBED_FLOOR:-Rel-99}. Equal
// by default, and by nothing else:
//
//   - EMBED_FLOOR=Rel-17 exported for the image held it to a WEAKER contract than
//     the one validate had passed, and recorded embed_floor=Rel-17 in publish's
//     provenance for a corpus the pipeline had checked at Rel-99;
//   - GOAL_EMBED_FLOOR=Rel-19 alone made the image re-check a Rel-19 corpus at
//     Rel-99 and refuse it, after a full build;
//   - and either way validate's certificate (contract_certificate.go) no longer
//     matched, so the publish paid the 8-minute re-run the certificate exists to
//     spare.
//
// Now the floor the image is held to is the floor validate APPLIED, computed from
// the same argument list validate runs with, and runPublish hands it to the script
// as --embed-floor (a flag, because an empty floor — every release — is a value the
// script's `${EMBED_FLOOR:-…}` cannot receive). EMBED_FLOOR keeps its meaning for a
// standalone `make image`. Under goal it may be left unset or set to that same
// floor; any other value is refused at plan time, since honouring it would
// publish under a contract nobody checked and ignoring it would drop a setting
// somebody typed.

// DefaultEmbedFloor is the pipeline's embed floor when nothing sets one: cmd/goal's
// --embed-floor default. build-image.sh, corpus-local.sh and publish-corpus.sh each
// write the same literal as their ${EMBED_FLOOR:-…} default, and
// TestEveryEmbedFloorDefaultIsThePipelines holds all of them to this one.
const DefaultEmbedFloor = "Rel-99"

// appliedEmbedFloor is the floor `validate` holds the 3GPP corpus to: the
// --embed-floor of the very argument list it runs cmd/validate with. That is the
// config floor, unless the contract flags carry their own (DATA_EMBED_FLOOR, read
// by data-contract.sh), which validateArgs lets win. "" means no floor: every
// release is checked.
func appliedEmbedFloor(c *Ctx, t corpusTarget) string {
	v, _ := lastFlagValue(validateArgs(c, t), "embed-floor")
	return v
}

// imageEmbedFloor is the floor build-image.sh must apply: validate's, refused when
// the operator's EMBED_FLOOR says otherwise.
func imageEmbedFloor(c *Ctx) (string, error) {
	floor := appliedEmbedFloor(c, corpus3GPP())
	if v := os.Getenv("EMBED_FLOOR"); v != "" && v != floor {
		shown := floor
		if shown == "" {
			shown = "none (every release)"
		}
		return "", fmt.Errorf("EMBED_FLOOR=%q, but this pipeline embeds and validates the corpus at floor %s "+
			"(GOAL_EMBED_FLOOR / --embed-floor, or DATA_EMBED_FLOOR in the contract): under goal the image is "+
			"held to the contract validate checked, so change GOAL_EMBED_FLOOR (embed, validate and the image "+
			"move together) or unset EMBED_FLOOR", v, shown)
	}
	return floor, nil
}

// lastFlagValue is the value of a Go-style flag in an argument list, spelled with
// one dash or two, as "-name value" or "-name=value". The LAST occurrence wins, as it
// does in the flag package that parses cmd/validate's arguments.
func lastFlagValue(args []string, name string) (string, bool) {
	var (
		val   string
		found bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		k, v, hasEq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if k != name {
			continue
		}
		switch {
		case hasEq:
			val, found = v, true
		case i+1 < len(args):
			val, found = args[i+1], true
			i++
		}
	}
	return val, found
}
