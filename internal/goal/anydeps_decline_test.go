package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A DECLINED PRODUCER THAT LEFT NO ARTEFACT HAS NOT PRODUCED ONE.
//
// `seed` declines in two very different situations and the record cannot tell
// them apart: it declines when a local corpus is already there (the artefact
// exists, the dependant may start) and it declines when there is no GHCR
// credential (the artefact does NOT exist, and the decline happens before the
// download). Both are recorded StatusSuccess, and checkAnyDeps returned on the
// status alone — so `--only embed-etsi` on a machine that had never built passed
// a gate whose message says "produced its artefact", and reached the embedder
// with no corpus.
func TestADeclinedProducerWithNoArtefactDoesNotSatisfyAnyDeps(t *testing.T) {
	c, store := newTestCtx(t)
	db := c.dataPath("etsi.duckdb")

	producer := &Step{
		Name:    "fake-seed",
		Outputs: func(c *Ctx) []string { return []string{db} },
		Run:     func(c *Ctx) error { return nil },
	}
	consumer := &Step{
		Name:    "fake-embed",
		AnyDeps: []string{"fake-seed"},
		Run:     func(c *Ctx) error { return nil },
	}
	r := &Runner{
		byName: map[string]*Step{"fake-seed": producer, "fake-embed": consumer},
		ctx:    c,
		store:  store,
	}

	declined := &Record{Step: "fake-seed", Status: StatusSuccess, Declined: true}
	if err := r.store.Save(declined); err != nil {
		t.Fatal(err)
	}

	// No database on disk: the decline produced nothing, so the gate must refuse.
	err := r.checkAnyDeps(consumer)
	if err == nil {
		t.Fatal("a decline that produced no corpus satisfied the producer gate — this is the " +
			"state that reaches the embedder with no database")
	}
	if !strings.Contains(err.Error(), "declined without producing") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	// THE CONTROL, and it is the case that must keep working: seed declines
	// BECAUSE the corpus is already there. That decline is a green light.
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("a corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.checkAnyDeps(consumer); err != nil {
		t.Errorf("a decline taken BECAUSE the artefact exists must satisfy the gate: %v", err)
	}
}
