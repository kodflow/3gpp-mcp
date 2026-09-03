package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fetch work list decides how much of the corpus is re-acquired, and it used
// to decide it at series granularity: one spec moving in series 23 put every spec
// of series 23 back on the list. Measured on this repository on 2026-09-03, that
// was 20 225 entries against the 201 that had actually moved.
//
// On a machine that still holds its converted sources that is merely wasteful.
// This machine does not: `fetch` purges each archive once its HTML exists, and
// the converted tree had been reclaimed down to 1 410 files for 20 163 versions.
// There, the wholesale list is not a re-listing of finished work — it is ~30 h of
// re-download, followed by an ingest over a source tree that no longer matches
// the corpus. That is what made `make build` unrunnable on the machine that had
// published the corpus.

// TestAFinishedCorpusDoesNotScheduleTheWriteSide is the whole point: when
// upstream has published nothing, fetch must DECLINE.
//
// A decline reports the previous provenance, so ingest, merge, embed and index
// all stay skipped. Returning nil instead — which is what it did — records a
// success with a fresh provenance, and every one of those steps folds it: an
// empty delta used to schedule the entire write side to reproduce bytes nobody
// had touched.
func TestAFinishedCorpusDoesNotScheduleTheWriteSide(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["floor"] = "Rel-99"

	write(t, c.statePath("series.json"), `["21","23"]`)
	write(t, c.statePath("worklist.txt"), "\n")

	err := stepFetch().Run(c)
	if !Declined(err) {
		t.Fatalf("an empty work list must decline, not succeed; got %v", err)
	}
	if !strings.Contains(err.Error(), "work list is empty") {
		t.Errorf("the decline does not say why, so nobody can act on it: %v", err)
	}
}

// TestAnEmptySeriesListAlsoDeclines: the two halves of discover's output must
// agree before any work is scheduled. A work list with no series to ingest is
// the same "nothing to do", reached from the other side.
func TestAnEmptySeriesListAlsoDeclines(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["floor"] = "Rel-99"

	write(t, c.statePath("series.json"), `[]`)
	write(t, c.statePath("worklist.txt"), "Rel-4 https://example.invalid/21100-100.zip 21100-100.zip\n")

	if err := stepFetch().Run(c); !Declined(err) {
		t.Fatalf("no series to ingest must decline, not succeed; got %v", err)
	}
}

// The negative control. A decline that also fires on real work would be far
// worse than the bug it replaces: it would silently freeze the corpus at
// whatever version it happened to hold, and every gate downstream would keep
// measuring that corpus against itself and passing.
//
// Run() cannot complete here — there is no LibreOffice and no network in a unit
// test — so the assertion is that it got PAST the decline and reached the work.
func TestRealWorkIsNotDeclined(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["floor"] = "Rel-99"
	c.Config["jobs"] = "1"

	write(t, c.statePath("series.json"), `["21"]`)
	write(t, c.statePath("worklist.txt"),
		"Rel-4 https://example.invalid/21100-100.zip 21100-100.zip\n")

	err := stepFetch().Run(c)
	if err == nil {
		t.Fatal("a work list with a spec in it cannot have completed a fetch in a unit test")
	}
	if Declined(err) {
		t.Fatalf("201 specs of genuine drift were declined as 'nothing to do': %v", err)
	}
}

// TestTheWorkListIsProportionateWhereItCanBeComputed pins the default. The
// precise set was already implemented (`--repair-plan`) and already understood —
// the flag that selected it called itself "proportionate, ~1k specs vs ~20k" —
// but it was opt-in behind -repair, so the command everybody actually runs got
// the wholesale one.
func TestTheWorkListIsProportionateWhereItCanBeComputed(t *testing.T) {
	if !proportionateWorklist("0", true, true) {
		t.Error("with both an index and a corpus, the precise set is computable and must be the default")
	}
}

// The negative controls: every case where "everything" is the honest answer and
// the precise set would be a lie.
func TestTheWholesaleWorkListSurvivesWhereItIsTheTruth(t *testing.T) {
	cases := []struct {
		name                        string
		full                        string
		indexPresent, corpusPresent bool
	}{
		{"a first build, with no index and no corpus", "0", false, false},
		{"an index but no corpus to find holes in", "0", true, false},
		{"a corpus but no index to diff against", "0", false, true},
		{"an explicit full rebuild", "1", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if proportionateWorklist(tc.full, tc.indexPresent, tc.corpusPresent) {
				t.Error("a proportionate list here would silently skip specs that are genuinely missing")
			}
		})
	}
}

// TestFetchReadsTheWorkListItWasGiven. discover writes the exact set it wants
// acquired; fetch used to hand corpus.sh a SERIES list instead and let it
// re-enumerate, which is precisely how 201 specs became 20 225 again. The series
// list addresses ingest, which walks the converted tree by series — it was never
// a statement about what to download.
func TestFetchReadsTheWorkListItWasGiven(t *testing.T) {
	c, _ := newTestCtx(t)
	step := stepFetch()

	ins, err := step.Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	var sawWorklist bool
	for _, in := range ins {
		if filepath.Base(in) == "worklist.txt" {
			sawWorklist = true
		}
	}
	if !sawWorklist {
		t.Error("fetch does not declare worklist.txt as an input, so a changed work list would not replay it")
	}
}

// TestTheStepsThatChangedSaySo. Both steps changed what they produce, and
// neither names its own Go source in Impl (the orchestrator's own code is
// deliberately not provenance — that was the fix that stopped a comment in
// cmd/server from invalidating the embeddings). Version is therefore the only
// thing that can invalidate them, and leaving it at 1 would have shipped the new
// behaviour behind a fingerprint that still claimed the old one was current.
func TestTheStepsThatChangedSaySo(t *testing.T) {
	for _, s := range []*Step{stepDiscover(), stepFetch()} {
		if s.Version < 2 {
			t.Errorf("%s changed which specs it acquires but still declares Version %d",
				s.Name, s.Version)
		}
	}
}

// TestAnIngestThatAddedNothingDoesNotSendMergeOver22GB. `--resume --corpus`
// skips whatever the corpus already holds, so a pass over an already-held tree
// ends with every shard empty. Publishing a fresh provenance for that sends
// merge over the whole corpus and enrich behind it, to reproduce bytes nobody
// touched — the same defect as fetch's, one step further down.
func TestAnIngestThatAddedNothingDoesNotSendMergeOver22GB(t *testing.T) {
	log := `11:05:42 | ingest series 43 (17/19)
ingest: corpus already holds 20017 (spec, version) pair(s)
ingest: series 43 → 0 spec(s), 0 clause(s) (0 file(s))
11:05:42 | ingest series 50 (18/19)
ingest: series 50 → 0 spec(s), 0 clause(s) (0 file(s))
`
	if !ingestedNothing(log) {
		t.Fatal("a pass where every series added 0 specs and 0 clauses was treated as work")
	}
}

// The negative controls. Declining a pass that DID add clauses would carry a
// stale provenance forward and leave real content out of the corpus — silently,
// and permanently, because the next run would see nothing left to do.
func TestRealIngestWorkIsNeverDeclined(t *testing.T) {
	cases := map[string]string{
		"one series added clauses": `ingest: series 21 → 0 spec(s), 0 clause(s) (0 file(s))
ingest: series 23 → 4 spec(s), 1200 clause(s) (4 file(s))`,
		"a spec with no clause is still a spec":   `ingest: series 23 → 1 spec(s), 0 clause(s) (1 file(s))`,
		"no tally at all is not proof of nothing": `ingest: corpus already holds 20017 (spec, version) pair(s)`,
		"an empty transcript proves nothing":      ``,
	}
	for name, log := range cases {
		t.Run(name, func(t *testing.T) {
			if ingestedNothing(log) {
				t.Error("real or unproven work was reported as 'nothing to do'")
			}
		})
	}
}

// TestNoStepFoldsItsOwnOutputIntoItsFingerprint — for EVERY step, not just the
// one that was caught first.
//
// A step's own product can never be a stable input to itself, and in this
// pipeline it is worse than circular: compact, index and the paragraph conversion
// all rewrite the corpus later in the same build. So a step that fingerprints it
// records one set of bytes and is judged against another, and replays on every
// build for ever. It cost `merge` a 22 GB restore and a 38-minute fold per run,
// and it kept `embed`, `embed-etsi`, `paragraphs` and `paragraphs-etsi` planned
// as "certain to run (heavy)" on a corpus nothing had touched — measured
// 2026-09-03, on a pipeline whose every step was VALID.
//
// Sweeping the whole DAG rather than one step is the point: this was found three
// times in three places before it was looked for everywhere.
func TestNoStepFoldsItsOwnOutputIntoItsFingerprint(t *testing.T) {
	c, _ := newTestCtx(t)
	for _, s := range Pipeline() {
		if s.Inputs == nil || s.Outputs == nil {
			continue
		}
		ins, err := s.Inputs(c)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		outs := map[string]bool{}
		for _, o := range s.Outputs(c) {
			outs[filepath.Clean(o)] = true
		}
		for _, in := range ins {
			if outs[filepath.Clean(in)] {
				t.Errorf("step %s declares %s as both an input and an output, so its "+
					"fingerprint can never settle and every build replays it",
					s.Name, filepath.Base(in))
			}
		}
	}
}

// TestTheCorpusIsNobodysInput. The corpus file is rewritten by compact, index and
// the paragraph conversion, so ANY step that fingerprints it is judged against
// bytes that moved after it ran — whether or not it also declares it an output.
// That is how embed and embed-etsi stayed "certain to run" on a finished corpus.
func TestTheCorpusIsNobodysInput(t *testing.T) {
	c, _ := newTestCtx(t)
	rewritten := map[string]bool{
		filepath.Clean(c.dataPath("3gpp.duckdb")): true,
		filepath.Clean(c.dataPath("etsi.duckdb")): true,
	}
	// The honest exceptions: every one of these runs at or after the last write to
	// the half it fingerprints, so what it records is what it is judged against.
	// compact is the last rewrite; index and index-etsi freeze on top of it;
	// validate reads the finished 3GPP corpus, which nothing touches after index;
	// smoke reads both to prove they serve. Fingerprinting the corpus is the whole
	// point for these — it is what makes them notice a corpus that changed.
	allowed := map[string]bool{
		"compact": true, "index": true, "index-etsi": true,
		"validate": true, "smoke": true,
	}
	for _, s := range Pipeline() {
		if s.Inputs == nil || allowed[s.Name] {
			continue
		}
		ins, err := s.Inputs(c)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		for _, in := range ins {
			if rewritten[filepath.Clean(in)] {
				t.Errorf("step %s fingerprints %s, which later steps rewrite — it will "+
					"replay on every build", s.Name, filepath.Base(in))
			}
		}
	}
}

// The negative control: dropping the corpus must not have dropped the shards,
// which are what there actually is to fold. A merge that ignored them would
// never notice new content at all.
func TestMergeStillWatchesTheShards(t *testing.T) {
	c, _ := newTestCtx(t)
	write(t, filepath.Join(c.Local, "shards", "23.duckdb"), "shard")

	ins, err := stepMerge().Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	var sawShard bool
	for _, in := range ins {
		if filepath.Base(in) == "23.duckdb" {
			sawShard = true
		}
	}
	if !sawShard {
		t.Error("merge no longer watches the shards, so new clauses would never replay the fold")
	}
}

// TestTheCompactionBackupHasAnEndOfLife.
//
// compact writes <db>.pre-compact and prints "once served and verified, remove
// it" — and nothing ever did. That is not just disk: compact REFUSES to
// overwrite an existing .pre-compact, so one build's backup blocks the next.
// On 2026-09-03 the ETSI half compacted and verified (14.7 GiB, 3 169 614
// clauses) and then failed the in-place swap because of a backup from the 2nd.
//
// The release belongs to `smoke`, and nowhere earlier: until the shipped binary
// has served the compacted corpus and answered, the backup is the only way back.
func TestTheCompactionBackupHasAnEndOfLife(t *testing.T) {
	c, _ := newTestCtx(t)
	for _, n := range []string{"3gpp.duckdb.pre-compact", "etsi.duckdb.pre-compact"} {
		write(t, c.dataPath(n), "backup")
	}
	// A corpus that has NOT been served keeps its backup.
	if _, err := os.Stat(c.dataPath("3gpp.duckdb.pre-compact")); err != nil {
		t.Fatal(err)
	}

	releasePreCompact(c)

	for _, n := range []string{"3gpp.duckdb.pre-compact", "etsi.duckdb.pre-compact"} {
		if _, err := os.Stat(c.dataPath(n)); !os.IsNotExist(err) {
			t.Errorf("%s survived the smoke, so the next compact will refuse to swap", n)
		}
	}
}

// The negative control: releasing a backup that is not there must not fail, and
// must never touch the corpus itself.
func TestReleasingAnAbsentBackupIsHarmless(t *testing.T) {
	c, _ := newTestCtx(t)
	write(t, c.dataPath("3gpp.duckdb"), "the corpus")

	releasePreCompact(c) // no .pre-compact anywhere

	if _, err := os.Stat(c.dataPath("3gpp.duckdb")); err != nil {
		t.Fatalf("releasing an absent backup removed the corpus itself: %v", err)
	}
}

// TestNothingAPreviousCompactionLeftBehindCanBlockTheNextOne.
//
// compact refuses to overwrite either of its own artefacts: <db>.compact (the
// copy, left only by a run that died between copy and swap) and <db>.pre-compact
// (the backup, created only BY a completed swap). Each refusal is right about the
// file and wrong about the run — it strands the pipeline on a corpus it can never
// finish compacting. Three manual deletions on 2026-09-03.
//
// The release first lived in `smoke`, so that a backup would outlive the corpus
// until the shipped binary had served it. Sound reasoning, wrong placement:
// compact fails before smoke can run, so the release sat behind the very step it
// had to unblock.
func TestNothingAPreviousCompactionLeftBehindCanBlockTheNextOne(t *testing.T) {
	c, _ := newTestCtx(t)
	write(t, c.dataPath("3gpp.duckdb"), "the live corpus")
	write(t, c.dataPath("etsi.duckdb"), "the live corpus")
	for _, n := range []string{
		"3gpp.duckdb.compact", "3gpp.duckdb.pre-compact",
		"etsi.duckdb.compact", "etsi.duckdb.pre-compact",
	} {
		write(t, c.dataPath(n), "left behind")
	}

	rotateCompactionArtefacts(c)

	for _, n := range []string{
		"3gpp.duckdb.compact", "3gpp.duckdb.pre-compact",
		"etsi.duckdb.compact", "etsi.duckdb.pre-compact",
	} {
		if _, err := os.Stat(c.dataPath(n)); !os.IsNotExist(err) {
			t.Errorf("%s survived, so compact will refuse again", n)
		}
	}
	// The negative control the refusals exist for: the live corpora are what must
	// never be touched.
	for _, n := range []string{"3gpp.duckdb", "etsi.duckdb"} {
		if _, err := os.Stat(c.dataPath(n)); err != nil {
			t.Fatalf("rotation removed the live corpus %s: %v", n, err)
		}
	}
}

// TestAnInterruptedCompactionDoesNotBlockTheNextOne.
//
// compact writes <db>.compact, verifies it, then swaps — so a completed run
// leaves none. A leftover means the previous attempt died between the copy and
// the swap, and compact then refuses: "already exists — refusing to overwrite
// it". That refusal is right about the file and wrong about the run: it leaves
// the pipeline stuck on a corpus it can never finish compacting. It happened
// twice on 2026-09-03, once per half, each time needing a manual delete.
func TestAnInterruptedCompactionDoesNotBlockTheNextOne(t *testing.T) {
	c, _ := newTestCtx(t)
	write(t, c.dataPath("3gpp.duckdb"), "the live corpus")
	write(t, c.dataPath("3gpp.duckdb.compact"), "a half-finished copy")
	write(t, c.dataPath("etsi.duckdb.compact"), "a half-finished copy")

	rotateCompactionArtefacts(c)

	for _, n := range []string{"3gpp.duckdb.compact", "etsi.duckdb.compact"} {
		if _, err := os.Stat(c.dataPath(n)); !os.IsNotExist(err) {
			t.Errorf("%s survived, so compact will refuse to swap again", n)
		}
	}
	// The negative control that matters: the live corpus is what the guard
	// exists to protect, and clearing an intermediate must never touch it.
	if _, err := os.Stat(c.dataPath("3gpp.duckdb")); err != nil {
		t.Fatalf("clearing the intermediate removed the live corpus: %v", err)
	}
}
