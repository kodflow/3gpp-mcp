package goal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// Every test here runs over BOTH arms. The two seeds were pinned to `latest` by
// the same two lines, and the note left on them said it plainly: pinning must be
// done on both at once, or the arms diverge again. So nothing is asserted about
// one arm that is not asserted about the other.
func bothArms() []corpusTarget { return []corpusTarget{corpus3GPP(), corpusETSI()} }

// noCorpusEnv clears every variable that can repoint a seed, so a test sees the
// pin and nothing else.
func noCorpusEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{bootstrap.EnvGHCROwner, bootstrap.EnvCorpusTag,
		bootstrap.EnvCorpusRef3GPP, bootstrap.EnvCorpusRefETSI} {
		t.Setenv(v, "")
	}
}

func digestN(c byte) string { return "sha256:" + strings.Repeat(string([]byte{c}), 64) }

// writePins writes a pin file under root naming the given digests.
func writePins(t *testing.T, root, d3gpp, dETSI string) {
	t.Helper()
	write(t, filepath.Join(root, corpusPinFile),
		"# test pin\n\n"+
			"ghcr.io/kodflow/3gpp-corpus@"+d3gpp+"\n"+
			"ghcr.io/kodflow/etsi-corpus@"+dETSI+"\n")
}

// THE PIN IN THE TREE IS THE ONE THE SEEDS READ, AND IT PINS BOTH ARMS BY DIGEST.
// A pin file that names one package, or names it by tag, or that nothing reads,
// would leave an arm on `latest` while this change claimed otherwise.
func TestBothArmsSeedFromTheDigestPinnedInTheTree(t *testing.T) {
	noCorpusEnv(t)
	root := repoRootForTest()
	pins, err := readCorpusPins(filepath.Join(root, corpusPinFile))
	if err != nil {
		t.Fatalf("the committed pin does not parse: %v", err)
	}
	if len(pins) != 2 {
		t.Errorf("the pin names %d packages, want exactly the two corpus packages: %v", len(pins), pins)
	}
	c := &Ctx{Root: root}
	seen := map[string]bool{}
	for _, arm := range bothArms() {
		src, origin, err := arm.seedSource(c)
		if err != nil {
			t.Fatalf("%s: %v", arm.DB, err)
		}
		pin, ok := pins[src.Image]
		if !ok {
			t.Fatalf("%s seeds from %s, which the pin does not name", arm.DB, src.Image)
		}
		if !bootstrap.IsDigest(src.Ref) || src.Ref != pin.Digest || src.Owner != pin.Owner {
			t.Errorf("%s would pull %s, want the pinned %s", arm.DB, src, bootstrap.FullRef(src, pin.Digest))
		}
		if origin != corpusPinFile {
			t.Errorf("%s: the reference came from %q, want the pin", arm.DB, origin)
		}
		// The determinant is the pinned reference, spelled as a digest reference.
		extra, err := arm.seedExtra(c)
		if err != nil {
			t.Fatal(err)
		}
		if want := "ghcr.io/" + pin.Owner + "/" + pin.Image + "@" + pin.Digest; extra["snapshot"] != want {
			t.Errorf("%s: Extra snapshot = %q, want %q", arm.DB, extra["snapshot"], want)
		}
		// And the step actually declares it.
		if s := stepSeed(arm); s.Extra == nil {
			t.Errorf("%s declares no Extra: a pin bump would be invisible to its fingerprint", s.Name)
		}
		seen[src.Ref] = true
	}
	if len(seen) != 2 {
		t.Error("both arms resolved to the same digest: one corpus would be seeded with the other's snapshot")
	}
}

// EACH ARM'S PIN MOVES ONLY ITS OWN SEED. The pin is one file for two packages;
// declaring the file in both steps' Impl would have made an ETSI republish
// re-evaluate the 3GPP seed too.
func TestAPinBumpMovesOnlyItsOwnArm(t *testing.T) {
	noCorpusEnv(t)
	root := t.TempDir()
	writePins(t, root, digestN('3'), digestN('e'))
	c := &Ctx{Root: root}
	three, etsi := corpus3GPP(), corpusETSI()
	before3, _ := three.seedExtra(c)
	beforeE, _ := etsi.seedExtra(c)

	writePins(t, root, digestN('3'), digestN('f'))
	after3, err := three.seedExtra(c)
	if err != nil {
		t.Fatal(err)
	}
	afterE, err := etsi.seedExtra(c)
	if err != nil {
		t.Fatal(err)
	}
	if after3["snapshot"] != before3["snapshot"] {
		t.Errorf("bumping the ETSI pin moved the 3GPP seed: %q -> %q", before3["snapshot"], after3["snapshot"])
	}
	if afterE["snapshot"] == beforeE["snapshot"] {
		t.Errorf("bumping the ETSI pin did not move the ETSI seed (%q)", afterE["snapshot"])
	}
}

// AN OVERRIDE REPLACES THE PIN ON BOTH ARMS, THE SAME WAY — and an owner the pin
// does not name is refused rather than sent a digest from another repository.
func TestOverridesApplyToBothArmsAlike(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, digestN('3'), digestN('e'))
	c := &Ctx{Root: root}

	noCorpusEnv(t)
	t.Setenv(bootstrap.EnvCorpusTag, "2026-08-26")
	for _, arm := range bothArms() {
		src, origin, err := arm.seedSource(c)
		if err != nil || src.Ref != "2026-08-26" || origin != "$"+bootstrap.EnvCorpusTag {
			t.Errorf("%s: shared tag not applied: (%s, %q, %v)", arm.DB, src, origin, err)
		}
	}

	noCorpusEnv(t)
	t.Setenv(bootstrap.EnvCorpusRef3GPP, digestN('a'))
	t.Setenv(bootstrap.EnvCorpusRefETSI, digestN('b'))
	for arm, want := range map[string]string{"3gpp.duckdb": digestN('a'), "etsi.duckdb": digestN('b')} {
		target := corpus3GPP()
		if arm == "etsi.duckdb" {
			target = corpusETSI()
		}
		if src, _, err := target.seedSource(c); err != nil || src.Ref != want {
			t.Errorf("%s: per-package digest not applied: (%s, %v)", arm, src, err)
		}
	}

	noCorpusEnv(t)
	t.Setenv(bootstrap.EnvGHCROwner, "a-fork")
	for _, arm := range bothArms() {
		if _, _, err := arm.seedSource(c); err == nil || !strings.Contains(err.Error(), "one repository") {
			t.Errorf("%s: an owner the pin does not name was accepted with the pin's digest: %v", arm.DB, err)
		}
	}
}

// seedOnly builds a runner over the REAL seed step of one arm and one synthetic
// dependant standing where discover stands. build-go is dropped from Deps (it is a
// Tool and contributes nothing to the fingerprint either way), and Validate is
// dropped because it runs dbcount on a real DuckDB; everything else — Extra, Run,
// the decline — is the shipped step.
func seedOnly(t *testing.T, arm corpusTarget, ctx *Ctx, store *Store, downstream *int) *Runner {
	t.Helper()
	seed := stepSeed(arm)
	seed.Deps = nil
	seed.Validate = nil
	dep := counter("discover"+arm.Suffix, []string{seed.Name}, []string{"src/discover.go"}, "out-discover"+arm.Suffix, downstream)
	r, err := NewRunner([]*Step{seed, dep}, ctx, store, func() string { return "tc" })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// THE PRICE OF A PIN BUMP, pinned. On a machine that holds a corpus — every
// machine that has built once — bumping the pin must re-run `seed` (its
// configuration changed), which DECLINES, and must replay NOTHING behind it:
// discover-etsi folds seed-etsi's provenance, and a new one would cascade into
// fetch, ingest, embed and a rewritten corpus. This is the trap the change could
// have walked into; the decline carries the provenance, and this proves it holds
// for the shipped step, on both arms.
func TestAPinBumpOnAMachineWithACorpusReplaysNothingBehindSeed(t *testing.T) {
	for _, arm := range bothArms() {
		t.Run(arm.DB, func(t *testing.T) {
			noCorpusEnv(t)
			ctx, store := newTestCtx(t)
			writePins(t, ctx.Root, digestN('3'), digestN('e'))
			write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover")
			write(t, filepath.Join(ctx.Data, arm.DB), "a corpus this machine built")
			write(t, filepath.Join(ctx.Local, "corpus-index.json"), `{"23.501|Rel-18":"18.0.0"}`)
			// Impl is read from the tree, like every step's.
			for _, f := range stepSeed(arm).Impl {
				write(t, filepath.Join(ctx.Root, f), "package goal")
			}
			old := fetchCorpus
			fetchCorpus = func(context.Context, bootstrap.CorpusSource, string, string, func(string, ...any)) (string, error) {
				t.Fatal("seed pulled a snapshot over a corpus that was already there")
				return "", nil
			}
			defer func() { fetchCorpus = old }()

			var downstream int
			r := seedOnly(t, arm, ctx, store, &downstream)
			if _, err := r.Execute(nil, false); err != nil {
				t.Fatal(err)
			}
			first, _ := store.Load("seed" + arm.Suffix)
			if first == nil || !first.Declined || downstream != 1 {
				t.Fatalf("first pass: seed record %+v, downstream=%d", first, downstream)
			}

			// The publisher pushed a new snapshot of BOTH packages and bumped the pin.
			writePins(t, ctx.Root, digestN('4'), digestN('f'))
			if _, err := r.Execute(nil, false); err != nil {
				t.Fatal(err)
			}
			second, _ := store.Load("seed" + arm.Suffix)
			if second.Fingerprint == first.Fingerprint {
				t.Fatal("the pin moved and seed's fingerprint did not: the bump is invisible to the step")
			}
			if !second.Declined {
				t.Error("seed did not decline over an existing corpus")
			}
			if publishedProvenance(second) != publishedProvenance(first) {
				t.Errorf("a declining seed published a new provenance (%s -> %s): everything behind it replays",
					publishedProvenance(first), publishedProvenance(second))
			}
			if downstream != 1 {
				t.Errorf("a pin bump over an existing corpus replayed the step behind seed (runs=%d)", downstream)
			}
		})
	}
}

// WHAT WAS PULLED REACHES THE DEPENDANTS, even when the reference cannot say it.
// Under a tag override (`latest`, the old default) the fingerprint is the same
// before and after the tag moves. The manifest digest actually pulled is what
// tells two seeds apart, and it must reach the provenance — otherwise a corpus
// re-seeded from a moved tag is served under the index of the previous one.
func TestTheSnapshotASeedPulledIsItsProvenance(t *testing.T) {
	for _, arm := range bothArms() {
		t.Run(arm.DB, func(t *testing.T) {
			noCorpusEnv(t)
			t.Setenv("GHCR_PAT", "test-token")
			t.Setenv(refEnvName(arm.Snapshot().Image), "latest")
			ctx, store := newTestCtx(t)
			writePins(t, ctx.Root, digestN('3'), digestN('e'))
			write(t, filepath.Join(ctx.Root, "src", "discover.go"), "package discover")
			// Present, so the 3GPP seed never reaches for the published anchor.
			write(t, filepath.Join(ctx.Local, "corpus-index.json"), `{"23.501|Rel-18":"18.0.0"}`)
			for _, f := range stepSeed(arm).Impl {
				write(t, filepath.Join(ctx.Root, f), "package goal")
			}

			served := digestN('1') // what `latest` resolves to right now
			var asked []string
			old := fetchCorpus
			fetchCorpus = func(_ context.Context, s bootstrap.CorpusSource, _, dest string, _ func(string, ...any)) (string, error) {
				asked = append(asked, s.String())
				return served, os.WriteFile(dest, []byte("snapshot "+served), 0o644)
			}
			defer func() { fetchCorpus = old }()

			db := filepath.Join(ctx.Data, arm.DB)
			var downstream int
			r := seedOnly(t, arm, ctx, store, &downstream)
			run := func() *Record {
				t.Helper()
				if _, err := r.Execute(nil, false); err != nil {
					t.Fatal(err)
				}
				rec, _ := store.Load("seed" + arm.Suffix)
				return rec
			}

			first := run()
			wantRef := "ghcr.io/kodflow/" + arm.Snapshot().Image
			if len(asked) != 1 || asked[0] != wantRef+":latest" {
				t.Fatalf("seed asked for %v, want %s:latest", asked, wantRef)
			}
			if got := first.Produced["snapshot"]; got != wantRef+"@"+served {
				t.Fatalf("the record names %q as pulled, want %q", got, wantRef+"@"+served)
			}

			// The corpus is lost and `latest` has moved: the SAME fingerprint pulls a
			// DIFFERENT snapshot, and what stands on it must replay.
			_ = os.Remove(db)
			served = digestN('2')
			second := run()
			if second.Fingerprint != first.Fingerprint {
				t.Fatal("test premise: the reference did not change, so the fingerprint must not either")
			}
			if publishedProvenance(second) == publishedProvenance(first) {
				t.Error("two different snapshots published the same provenance: a moved tag is invisible downstream")
			}
			if downstream != 2 {
				t.Errorf("a different snapshot did not replay the step behind seed (runs=%d, want 2)", downstream)
			}

			// Negative control: the same snapshot again changes nothing downstream.
			_ = os.Remove(db)
			third := run()
			if publishedProvenance(third) != publishedProvenance(second) {
				t.Error("the same snapshot, pulled twice, published two provenances")
			}
			if downstream != 2 {
				t.Errorf("re-seeding the same snapshot replayed the step behind seed (runs=%d, want 2)", downstream)
			}

			// And a decline keeps naming it: the corpus is there, the reference
			// changes, seed declines — its record still says where the corpus came from.
			t.Setenv(refEnvName(arm.Snapshot().Image), "2026-09-11")
			fourth := run()
			if !fourth.Declined {
				t.Fatal("seed did not decline over the corpus it had just seeded")
			}
			if fourth.Produced["snapshot"] != wantRef+"@"+served {
				t.Errorf("the decline forgot which snapshot the corpus came from: %v", fourth.Produced)
			}
			if publishedProvenance(fourth) != publishedProvenance(third) || downstream != 2 {
				t.Errorf("the decline moved the provenance or replayed downstream (runs=%d)", downstream)
			}
		})
	}
}

// THE PUBLISHER WRITES WHAT THE SEED READS. The pin has one writer (bash, in
// publish-corpus.sh) and one reader (this package); a format the two disagreed on
// would be found on the day a republish was merged and a fresh clone seeded from
// whatever the reader made of it. So the real writer rewrites a copy of the
// committed pin and the real reader must see exactly that change.
func TestThePublisherWritesWhatTheSeedReads(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH; scripts/lib/corpus-pin_test.sh covers the writer alone")
	}
	noCorpusEnv(t)
	root := t.TempDir()
	committed, err := os.ReadFile(filepath.Join(repoRootForTest(), corpusPinFile))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, corpusPinFile), string(committed))
	before, err := readCorpusPins(filepath.Join(root, corpusPinFile))
	if err != nil {
		t.Fatal(err)
	}

	lib, err := filepath.Abs(filepath.Join(repoRootForTest(), "scripts", "lib", "corpus-pin.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bumped := "ghcr.io/kodflow/etsi-corpus@" + digestN('9')
	cmd := exec.Command(bash, "-c", `. "$1" && pin_corpus_snapshot "$2" etsi-corpus "$3"`, "bash",
		filepath.ToSlash(lib), filepath.ToSlash(filepath.Join(root, corpusPinFile)), bumped)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pin_corpus_snapshot: %v\n%s", err, out)
	}

	after, err := readCorpusPins(filepath.Join(root, corpusPinFile))
	if err != nil {
		t.Fatalf("the seed cannot read what the publisher wrote: %v", err)
	}
	if after[bootstrap.ImageETSI].Digest != digestN('9') {
		t.Errorf("the seed reads the ETSI pin as %+v, want %s", after[bootstrap.ImageETSI], digestN('9'))
	}
	if after[bootstrap.Image3GPP] != before[bootstrap.Image3GPP] {
		t.Errorf("bumping ETSI moved the 3GPP pin: %+v -> %+v", before[bootstrap.Image3GPP], after[bootstrap.Image3GPP])
	}
	src, _, err := corpusETSI().seedSource(&Ctx{Root: root})
	if err != nil || src.String() != bumped {
		t.Errorf("seed-etsi would pull %s (%v), want the bumped %s", src, err, bumped)
	}
}

// A MALFORMED PIN FAILS THE PLAN, NOT A FUTURE CLONE. A line the parser skipped
// would leave an arm with no pin, and the failure would surface as a seed of the
// wrong corpus on some fresh machine months later.
func TestAMalformedPinIsAnError(t *testing.T) {
	noCorpusEnv(t)
	for name, body := range map[string]string{
		"a tag":          "ghcr.io/kodflow/3gpp-corpus:latest\n",
		"a short digest": "ghcr.io/kodflow/3gpp-corpus@sha256:abc\n",
		"a duplicate": "ghcr.io/kodflow/3gpp-corpus@" + digestN('1') + "\n" +
			"ghcr.io/kodflow/3gpp-corpus@" + digestN('2') + "\n",
	} {
		root := t.TempDir()
		write(t, filepath.Join(root, corpusPinFile), body)
		if _, err := readCorpusPins(filepath.Join(root, corpusPinFile)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A package the pin does not name is an error for that arm, not `latest`.
	root := t.TempDir()
	write(t, filepath.Join(root, corpusPinFile), "ghcr.io/kodflow/3gpp-corpus@"+digestN('1')+"\n")
	if _, err := corpusETSI().seedExtra(&Ctx{Root: root}); err == nil {
		t.Error("an arm the pin does not name fell back to something instead of failing")
	}
	// A CRLF checkout (the main one has 211 such files) reads the same.
	write(t, filepath.Join(root, corpusPinFile), "# c\r\nghcr.io/kodflow/3gpp-corpus@"+digestN('1')+"\r\n")
	if pins, err := readCorpusPins(filepath.Join(root, corpusPinFile)); err != nil || pins[bootstrap.Image3GPP].Digest != digestN('1') {
		t.Errorf("a CRLF pin did not read: %v %v", pins, err)
	}
}
