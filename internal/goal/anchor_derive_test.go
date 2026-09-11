package goal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// stubDerive replaces cmd/derive-anchor with a function of the corpus file: the
// anchor it "derives" is whatever `of` says that corpus holds. The real tool is
// tested in cmd/derive-anchor against a real DuckDB.
func stubDerive(t *testing.T, of func(dbContent string) string) *int {
	t.Helper()
	calls := 0
	old := runDeriveAnchor
	runDeriveAnchor = func(_ *Ctx, db, out string) error {
		calls++
		b, err := os.ReadFile(db)
		if err != nil {
			return err
		}
		return os.WriteFile(out, []byte(of(string(b))), 0o644)
	}
	t.Cleanup(func() { runDeriveAnchor = old })
	return &calls
}

// stepLogOf reads what a step wrote to its log on its last run.
func stepLogOf(t *testing.T, ctx *Ctx, store *Store, step string) string {
	t.Helper()
	rec, _ := store.Load(step)
	if rec == nil || rec.LogFile == "" {
		t.Fatalf("no log recorded for %s", step)
	}
	b, err := os.ReadFile(filepath.Join(ctx.Root, rec.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// seedFixture is a machine about to seed the 3GPP arm from a pinned snapshot.
func seedFixture(t *testing.T, arm corpusTarget) (*Ctx, *Store) {
	t.Helper()
	noCorpusEnv(t)
	t.Setenv("GHCR_PAT", "test-token")
	ctx, store := newTestCtx(t)
	writePins(t, ctx.Root, digestN('3'), digestN('e'))
	write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover")
	for _, f := range stepSeed(arm).Impl {
		write(t, filepath.Join(ctx.Root, f), "package goal")
	}
	old := fetchCorpus
	fetchCorpus = func(_ context.Context, s bootstrap.CorpusSource, _, dest string, _ func(string, ...any)) (string, error) {
		return s.Ref, os.WriteFile(dest, []byte("the pinned snapshot"), 0o644)
	}
	t.Cleanup(func() { fetchCorpus = old })
	return ctx, store
}

// The anchor of the pinned snapshot, as the derivation sees it.
const snapshotAnchor = "{\n \"23.501|Rel-19\": \"19.5.0\",\n \"24.555|Rel-19\": \"19.5.0\"\n}"

// A FRESH CLONE GETS THE ANCHOR OF THE SNAPSHOT IT PULLED — and an anchor left on
// disk from another generation is REPLACED, loudly, instead of trusted.
//
// The anchor used to come from the `latest` GitHub release: 2026-06-05, 645 keys
// behind the corpus the next snapshot will be published from and 140 short. The
// stale anchor below has both drifts, plus the dangerous one: a key the snapshot
// does not hold, which discover would skip for ever.
func TestASeededSnapshotGetsItsOwnAnchor(t *testing.T) {
	ctx, store := seedFixture(t, corpus3GPP())
	calls := stubDerive(t, func(db string) string {
		if db != "the pinned snapshot" {
			t.Errorf("the anchor was derived from %q, not from the snapshot just pulled", db)
		}
		return snapshotAnchor
	})
	stale := `{"23.501|Rel-19": "19.4.0", "38.300|Rel-19": "19.9.0"}`
	write(t, anchorPath(ctx), stale)

	var downstream int
	r := seedOnly(t, corpus3GPP(), ctx, store, &downstream)
	if _, err := r.Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(anchorPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != snapshotAnchor {
		t.Fatalf("anchor after seeding:\n%s\nwant the snapshot's:\n%s", got, snapshotAnchor)
	}
	if *calls != 1 {
		t.Errorf("derive-anchor ran %d times, want 1", *calls)
	}
	log := stepLogOf(t, ctx, store, "seed")
	for _, want := range []string{"NOT this corpus's", "behind=1 missing=1 ahead=0 extra=1", "discover would have skipped them"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not say %q:\n%s", want, log)
		}
	}
	if _, err := os.Stat(anchorPath(ctx) + ".derived"); err == nil {
		t.Error("the derivation's temporary file was left behind")
	}
}

// A CORPUS ALREADY ON DISK KEEPS THE ANCHOR THE FOLD WROTE BESIDE IT. The seed
// declines there — every build on a machine that has built once — and must not
// touch the anchor: it is an Input of discover, and a rewrite would replay it.
func TestAPresentCorpusKeepsItsAnchor(t *testing.T) {
	ctx, store := seedFixture(t, corpus3GPP())
	calls := stubDerive(t, func(string) string { return snapshotAnchor })
	write(t, filepath.Join(ctx.Data, "3gpp.duckdb"), "a corpus this machine built")
	local := `{"23.501|Rel-19": "19.6.0"}`
	write(t, anchorPath(ctx), local)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(anchorPath(ctx), past, past); err != nil {
		t.Fatal(err)
	}

	var downstream int
	if _, err := seedOnly(t, corpus3GPP(), ctx, store, &downstream).Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(anchorPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(anchorPath(ctx))
	if string(got) != local || !st.ModTime().Equal(past) {
		t.Errorf("the anchor beside a present corpus was rewritten (%q)", got)
	}
	if *calls != 0 {
		t.Errorf("derive-anchor ran %d times over a corpus that has its anchor", *calls)
	}
}

// A CORPUS WITH NO ANCHOR GETS ONE DERIVED, instead of a FULL discover — which
// on this corpus means asking 3GPP for all 20 163 spec versions again.
func TestAPresentCorpusWithoutAnAnchorGetsOneDerived(t *testing.T) {
	ctx, store := seedFixture(t, corpus3GPP())
	stubDerive(t, func(db string) string { return snapshotAnchor })
	write(t, filepath.Join(ctx.Data, "3gpp.duckdb"), "a corpus this machine built")

	var downstream int
	if _, err := seedOnly(t, corpus3GPP(), ctx, store, &downstream).Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(anchorPath(ctx)); string(got) != snapshotAnchor {
		t.Errorf("no anchor derived for a corpus that had none: %q", got)
	}
}

// AN ANCHOR THAT ALREADY DESCRIBES THE CORPUS IS NOT REWRITTEN — same bytes, same
// mtime. discover fingerprints its anchor by size and mtime, so rewriting
// identical bytes after a re-seed would replay it for nothing.
func TestAnAnchorThatAlreadyMatchesIsLeftAlone(t *testing.T) {
	ctx, _ := newTestCtx(t)
	stubDerive(t, func(string) string { return snapshotAnchor })
	db := filepath.Join(ctx.Data, "3gpp.duckdb")
	write(t, db, "corpus")
	write(t, anchorPath(ctx), snapshotAnchor)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(anchorPath(ctx), past, past); err != nil {
		t.Fatal(err)
	}
	if err := deriveAnchor(ctx, db); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(anchorPath(ctx))
	if !st.ModTime().Equal(past) {
		t.Error("an anchor identical to the derived one was rewritten")
	}
	if _, err := os.Stat(anchorPath(ctx) + ".derived"); err == nil {
		t.Error("the derivation's temporary file was left behind")
	}
}

// A DERIVATION THAT FAILS FAILS THE STEP, and leaves the anchor as it was. It is
// a read of one table: failing means the tool or the corpus is broken, and
// carrying on would send discover on a FULL pass or leave a stale anchor in charge.
func TestAFailedDerivationFailsAndTouchesNothing(t *testing.T) {
	ctx, _ := newTestCtx(t)
	old := runDeriveAnchor
	runDeriveAnchor = func(_ *Ctx, _, out string) error {
		_ = os.WriteFile(out, []byte("{half"), 0o644)
		return errors.New("derive-anchor: cannot open the corpus")
	}
	t.Cleanup(func() { runDeriveAnchor = old })
	db := filepath.Join(ctx.Data, "3gpp.duckdb")
	write(t, db, "corpus")
	write(t, anchorPath(ctx), snapshotAnchor)
	if err := deriveAnchor(ctx, db); err == nil {
		t.Fatal("a failed derivation was reported as success")
	}
	if got, _ := os.ReadFile(anchorPath(ctx)); string(got) != snapshotAnchor {
		t.Errorf("a failed derivation changed the anchor: %q", got)
	}
	if _, err := os.Stat(anchorPath(ctx) + ".derived"); err == nil {
		t.Error("a failed derivation left its partial output behind")
	}
}

// INGEST'S NO-FOLD PATH RESTORES A MISSING ANCHOR — by deriving it. It asked
// `merge --index-out --base <corpus>` with no shard, which merge refuses before
// doing anything ("pass at least one shard path", measured against the real
// binary), so this path failed the step every time it was reached.
func TestEnsureCorpusIndexDerivesTheAnchor(t *testing.T) {
	ctx, _ := newTestCtx(t)
	calls := stubDerive(t, func(string) string { return snapshotAnchor })
	write(t, filepath.Join(ctx.Data, "3gpp.duckdb"), "corpus")
	if err := ensureCorpusIndex(ctx); err != nil {
		t.Fatalf("restoring a missing anchor failed: %v", err)
	}
	if got, _ := os.ReadFile(anchorPath(ctx)); string(got) != snapshotAnchor {
		t.Errorf("anchor = %q", got)
	}
	// Present: nothing to do.
	if err := ensureCorpusIndex(ctx); err != nil || *calls != 1 {
		t.Errorf("a present anchor was derived again (calls=%d, err=%v)", *calls, err)
	}
}

// THE ANCHOR IS A 3GPP ARTEFACT. The ETSI seed installs none — ETSI has no delta
// anchor by design (docs/local-pipeline.md) — so deriving one there would point a
// 3GPP-shaped file at the ETSI corpus.
func TestTheETSISeedInstallsNoAnchor(t *testing.T) {
	ctx, store := seedFixture(t, corpusETSI())
	calls := stubDerive(t, func(string) string { return snapshotAnchor })
	var downstream int
	if _, err := seedOnly(t, corpusETSI(), ctx, store, &downstream).Execute(nil, false); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("the ETSI seed derived a 3GPP anchor (%d calls)", *calls)
	}
	if _, err := os.Stat(anchorPath(ctx)); err == nil {
		t.Error("the ETSI seed wrote corpus-index.json")
	}
}

// Every step that can produce the anchor declares the tool that derives it, so a
// change to how it is derived is a change to what the step produces.
func TestTheStepsThatWriteTheAnchorDeclareItsDerivation(t *testing.T) {
	for _, s := range []*Step{stepSeed(corpus3GPP()), stepIngest(corpus3GPP())} {
		have := map[string]bool{}
		for _, p := range s.Impl {
			have[p] = true
		}
		for _, want := range []string{"cmd/derive-anchor", "internal/anchor", "internal/goal/anchor_derive.go"} {
			if !have[want] {
				t.Errorf("%s writes the anchor and does not declare %s", s.Name, want)
			}
		}
		if !s.ExcludeTests {
			t.Errorf("%s launches derive-anchor; its tests are not a determinant", s.Name)
		}
	}
	found := false
	for _, b := range goBins {
		found = found || b == "derive-anchor"
	}
	if !found {
		t.Error("derive-anchor is not built by build-go: the steps would launch a binary nothing produces")
	}
}
