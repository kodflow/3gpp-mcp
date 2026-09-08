package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBothHalvesAreEnriched pins the defect this step was added to end.
//
// rust/ingest/src/bin/ingest_glossary.rs exists, is tested, and is BUILT by
// build-rust into .local/rust-bin/ingest-glossary.exe. No step ran it. The
// 4 941 acronyms the shipped ETSI half carries were written by hand, once, from
// .local/resume/v3-chain2.sh — so:
//
//   - a fresh clone built an ETSI corpus with an EMPTY glossary and reported
//     success, because nothing asked the corpus whether it had a vocabulary;
//   - a deliverable fetched after that never contributed its Abbreviations
//     clause, because the only pass that reads them was not in the graph.
//
// resolve_term federates to the ETSI half (internal/mcp/server.go), and ETSI is
// where UICC, ADF and AID are defined — terms TS 21.905 does not carry. So the
// missing step is not a tidiness point: it decides whether half the product's
// vocabulary exists at all.
func TestBothHalvesAreEnriched(t *testing.T) {
	steps := map[string]*Step{}
	for _, s := range Pipeline() {
		steps[s.Name] = s
	}
	for _, suffix := range []string{"", "-etsi"} {
		name := "enrich" + suffix
		if steps[name] == nil {
			t.Fatalf("no %q step: that half of the corpus is never enriched, and nothing "+
				"in the pipeline can say so", name)
		}
	}
}

// The ETSI enrich reads what ingest-etsi produced, with the binary build-rust
// produced. Both have to be in the graph or the step can run against a tree that
// is not there yet, or with a binary that predates its own source.
func TestETSIEnrichWaitsForItsCorpusAndItsBinary(t *testing.T) {
	e := stepEnrich(corpusETSI())
	if e.Name != "enrich-etsi" {
		t.Fatalf("the ETSI arm is named %q, not %q", e.Name, "enrich-etsi")
	}
	for _, want := range []string{"ingest-etsi", "build-rust"} {
		if !contains(e.Deps, want) {
			t.Errorf("enrich-etsi depends on %v, which does not include %q", e.Deps, want)
		}
	}
}

// BOTH conversions wait for their own enrich, and the ETSI one did not.
//
// The comment on paragraphsDeps explained the asymmetry — "ETSI has no such
// overlay (DynaReport describes 3GPP specs, not ETSI deliverables)" — and it was
// true of the CATALOGUE. It was then read as a statement about enrichment as a
// whole, which is how a written, built, unrun glossary miner stayed invisible.
func TestBothConversionsWaitForTheirOwnEnrich(t *testing.T) {
	for _, suffix := range []string{"", "-etsi"} {
		var target corpusTarget
		if suffix == "" {
			target = corpus3GPP()
		} else {
			target = corpusETSI()
		}
		deps := stepParagraphs(target).Deps
		if !contains(deps, "enrich"+suffix) {
			t.Errorf("paragraphs%s depends on %v, which does not include %q",
				suffix, deps, "enrich"+suffix)
		}
	}
}

// enrich-etsi declares NO Outputs, and that is deliberate rather than forgotten.
//
// It writes one table — `acronyms` — that no step downstream reads. Naming the
// corpus as its output would make every glossary refresh report "dependency
// output changed" to paragraphs-etsi, which replays the conversion, the sparse
// arm, the compaction, the freeze and the publish behind it: hours of work to
// carry a row into a table none of them touch. The 3GPP arm declares none for
// the same reason, and its Validate is what proves it ran.
func TestETSIEnrichDeclaresNoCorpusOutput(t *testing.T) {
	for _, target := range []corpusTarget{corpus3GPP(), corpusETSI()} {
		e := stepEnrich(target)
		if e.Outputs == nil {
			continue
		}
		c, _ := newTestCtx(t)
		if out := e.Outputs(c); len(out) != 0 {
			t.Errorf("%s declares outputs %v: every enrichment would replay the "+
				"conversion, the sparse arm, the compaction and the publish", e.Name, out)
		}
	}
}

// A DECLARED DIRECTORY IS NOT A WATCHED ONE, and this is the trap the ETSI arm
// walked into on its first draft.
//
// enrich-etsi reads data/sources/convert-etsi, so naming that directory as an
// Input reads as exactly right. inputsHash records a directory as the constant
// string "dir" (fingerprint.go), so the fingerprint cannot move when a hundred
// deliverables land in it — declared, and never watched. `enrich` carries a
// comment about paying for precisely that, and this asserts the lesson instead of
// restating it: no Input this step declares may be a directory.
func TestNoEnrichArmWatchesADirectoryItCannotSee(t *testing.T) {
	for _, target := range []corpusTarget{corpus3GPP(), corpusETSI()} {
		e := stepEnrich(target)
		if e.Inputs == nil {
			continue
		}
		c, _ := newTestCtx(t)
		// The trees have to EXIST for the check to mean anything: os.Stat on an
		// absent path reports neither file nor directory, and the assertion would
		// pass over a declaration that is a directory in every real run.
		for _, d := range []string{
			filepath.Join(c.Data, "sources", "convert-etsi", "ETSI"),
			filepath.Join(c.Data, "sources", "5g-apis"),
			filepath.Join(c.Data, "sources", "asn"),
		} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		in, err := e.Inputs(c)
		if err != nil {
			t.Fatalf("%s Inputs: %v", e.Name, err)
		}
		for _, p := range in {
			st, err := os.Stat(p)
			if err != nil || !st.IsDir() {
				continue
			}
			t.Errorf("%s declares the directory %s as an input: inputsHash records it "+
				"as the constant \"dir\", so nothing arriving in it can ever make this "+
				"step run", e.Name, p)
		}
	}
}

// counterValue is what the ETSI Validate reads its verdict from, so its two
// failure modes are asserted rather than assumed.
//
// The form it replaces — strings.Contains(out, "acronyms=0") — is wrong twice:
// it matches the leading digit of a longer number, and it cannot separate "the
// corpus holds nothing" from "this binary is older than the check". Those are
// opposite verdicts: one is a broken corpus, the other a broken toolchain.
func TestCounterValueSeparatesZeroFromAbsent(t *testing.T) {
	const out = "spec_versions=11822\nacronyms=4941\nclauses_with_sparse=0\n"

	if n, ok := counterValue(out, "acronyms"); !ok || n != 4941 {
		t.Errorf("acronyms read as (%d, %v), want (4941, true)", n, ok)
	}
	// Zero is a VALUE, and it is the one the ETSI Validate must reject.
	if n, ok := counterValue(out, "clauses_with_sparse"); !ok || n != 0 {
		t.Errorf("a zero counter read as (%d, %v), want (0, true)", n, ok)
	}
	// Absent is NOT zero: a dbcount that predates the counter must not be read as
	// a corpus with an empty glossary.
	if _, ok := counterValue("spec_versions=11822\n", "acronyms"); ok {
		t.Error("an absent counter read as present: a stale dbcount would be reported " +
			"as an empty glossary")
	}
	// The prefix trap the Contains form falls into.
	if n, ok := counterValue("acronyms=4941\n", "acronym"); ok {
		t.Errorf("a prefix of the key matched, giving %d", n)
	}
}

// A STEP THAT DECLARES A RUST BINARY'S SOURCE MUST HAVE THAT BINARY BUILT.
//
// build-rust runs `cargo build --bin <name>` for each entry in rustBins, so a
// binary missing from that map is never compiled and never staged into
// .local/rust-bin. enrich-etsi runs ingest-glossary and declared
// rust/ingest/src/bin/ingest_glossary.rs as its implementation — while rustBins
// listed only the other three. Nothing failed here, because the file happened to
// be on disk: hand-built, months earlier, by the session that ran the pass
// manually. On any other machine the step would have died with "the binary is
// missing", and the provenance would have been correct about a binary that did
// not exist.
//
// The invariant is general, so it is asserted generally: every Impl entry naming
// a file under rust/ingest/src/bin must correspond to a binary rustBins builds.
func TestEveryDeclaredRustBinaryIsActuallyBuilt(t *testing.T) {
	built := map[string]bool{}
	for _, bins := range rustBins {
		for _, b := range bins {
			built[b] = true
		}
	}
	const prefix = "rust/ingest/src/bin/"
	for _, s := range Pipeline() {
		for _, impl := range s.Impl {
			if !strings.HasPrefix(impl, prefix) || !strings.HasSuffix(impl, ".rs") {
				continue
			}
			// src/bin/ingest_glossary.rs is the binary `ingest-glossary`: cargo's
			// file-name convention, underscores for hyphens.
			name := strings.ReplaceAll(strings.TrimSuffix(impl[len(prefix):], ".rs"), "_", "-")
			if !built[name] {
				t.Errorf("%s declares %s but rustBins never builds %q: the step would "+
					"fail on any machine that does not already have the file", s.Name, impl, name)
			}
		}
	}
}
