package goal

import (
	"path/filepath"
	"strings"
	"testing"
)

// A step's Impl is its PROVENANCE: the sources whose change means the step would
// produce something different. Declaring a directory is convenient and wrong
// whenever the directory holds more than the step can reach.
//
// MEASURED 2026-09-06, build 20:
//
//	STEP corpus-etsi
//	  reason  implementation changed: rust/ingest/src/bin/ingest_li.rs
//
// ingest_li.rs writes the Lawful-Interception registry from TS 33.128. ETSI has
// no such registry, corpus-etsi never invokes ingest-li, and the whole ETSI half
// was re-derived anyway: ~1 h of rework and 18.8 GiB re-pushed for a file the
// step cannot reach. `ingest` carried the same over-broad declaration.
//
// These tests pin BOTH directions, because the fix has a failure mode of its
// own: narrowing too far turns a loud waste into a silent staleness.

// implFixture materialises every path a step declares, so implHash can run
// against a synthetic tree. A file path becomes a file; a directory path becomes
// a directory with one file in it.
func implFixture(t *testing.T, root string, paths []string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.Contains(filepath.Base(p), ".") {
			write(t, full, "placeholder\n")
			continue
		}
		write(t, filepath.Join(full, "placeholder.rs"), "placeholder\n")
	}
}

// mutate rewrites one file under root and returns the new fingerprint.
func implAfterWriting(t *testing.T, root string, paths []string, rel, body string) string {
	t.Helper()
	write(t, filepath.Join(root, filepath.FromSlash(rel)), body)
	h, _, err := implHash(root, paths, false)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A binary a step never runs must not be able to invalidate it. This is the
// false POSITIVE — the one that cost an hour.
func TestIngestStepsIgnoreBinariesTheyNeverRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		step *Step
	}{
		{"corpus-etsi", stepCorpusETSI()},
		{"ingest", stepIngest()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			implFixture(t, root, tc.step.Impl)
			// The bins live in the crates but outside anything declared: ingest's
			// four overlays, and the store's own binaries (embed-io, merge, overlay).
			write(t, filepath.Join(root, "rust", "ingest", "src", "bin", "ingest_li.rs"), "fn main() {}\n")
			write(t, filepath.Join(root, "rust", "store", "src", "bin", "embed_io.rs"), "fn main() {}\n")

			before, _, err := implHash(root, tc.step.Impl, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, bin := range []string{
				"rust/ingest/src/bin/ingest_li.rs",
				"rust/store/src/bin/embed_io.rs",
			} {
				after := implAfterWriting(t, root, tc.step.Impl, bin, "fn main() { let _ = 1; }\n")
				if before != after {
					t.Fatalf("%s re-runs when %s changes — it never invokes that binary; "+
						"measured cost on corpus-etsi: ~1 h and 18.8 GiB", tc.name, bin)
				}
			}
		})
	}
}

// THE OTHER DIRECTION, and the reason this is not simply "declare less". A step
// that stops watching what it really uses keeps a stale corpus in silence, which
// is strictly worse than re-running for nothing.
func TestIngestStepsStillWatchWhatTheyActuallyUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		step *Step
	}{
		{"corpus-etsi", stepCorpusETSI()},
		{"ingest", stepIngest()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, rel := range []string{
				"rust/ingest/src/main.rs", // the binary both of them run
				"rust/ingest/Cargo.toml",  // its dependencies and features
				"rust/store/src/lib.rs",   // the library it links
				"rust/store/Cargo.toml",   // that library's own dependencies
				"rust/Cargo.toml",         // the workspace that resolves them
				"rust/Cargo.lock",         // and the versions it resolved to
				"rust/parse/placeholder.rs",
				"rust/identity/placeholder.rs",
			} {
				root := t.TempDir()
				implFixture(t, root, tc.step.Impl)
				before, _, err := implHash(root, tc.step.Impl, false)
				if err != nil {
					t.Fatal(err)
				}
				if after := implAfterWriting(t, root, tc.step.Impl, rel, "changed\n"); before == after {
					t.Fatalf("%s does NOT watch %s — a change there would leave a stale corpus "+
						"with no error anywhere", tc.name, rel)
				}
			}
		})
	}
}

// The declaration itself, read as text. The behavioural tests above would also
// pass if someone re-added the directory and the fixture happened not to cover
// it; this says the intent out loud so a future edit has to argue with it.
func TestIngestStepsDeclareFilesNotTheIngestCrate(t *testing.T) {
	for _, tc := range []struct {
		name string
		step *Step
	}{
		{"corpus-etsi", stepCorpusETSI()},
		{"ingest", stepIngest()},
	} {
		for _, p := range tc.step.Impl {
			switch p {
			case "rust/ingest", "rust/ingest/src", "rust/ingest/src/bin":
				t.Errorf("%s declares %q — that pulls in binaries it never runs", tc.name, p)
			}
		}
		if !contains(tc.step.Impl, "rust/ingest/src/main.rs") {
			t.Errorf("%s must declare the source of the binary it runs", tc.name)
		}
		// AND ITS MANIFEST. Cargo.toml selects the dependency versions and features
		// the binary is compiled with, so a manifest-only change produces a different
		// `ingest` from identical sources. build-rust cannot cover the gap: build
		// steps are Step.Tool by design, so a dirty tool never replays a data step —
		// the corpus would be kept from the previous binary, silently. Narrowing to
		// src/main.rs dropped it once; this is what stops that recurring.
		if !contains(tc.step.Impl, "rust/ingest/Cargo.toml") {
			t.Errorf("%s must declare rust/ingest/Cargo.toml — a dependency or feature "+
				"change alters the binary and would otherwise leave the corpus stale", tc.name)
		}
	}
}

// enrich is the STEP THAT MAY declare the bin directory, and the contrast is the
// point: it invokes ingest-catalog, ingest-openapi and ingest-li, so those files
// really are its implementation. Left as-is deliberately — this test records why
// it is not a leftover.
func TestEnrichKeepsTheBinDirectoryOnPurpose(t *testing.T) {
	if !contains(stepEnrich().Impl, "rust/ingest/src/bin") {
		t.Fatal("enrich no longer watches the binaries it runs — an overlay fix would not replay")
	}
}

// THE LEDGER IMPORT BELONGS TO embed-io, NOT TO merge — and until 2026-09-07 the
// provenance said the opposite, in both directions at once.
//
// Store::import_ledger lived in rust/store/src/lib.rs. `merge` declares that file
// and genuinely links the library, so a change to the vector import replayed a
// 38-MINUTE reconstruction of a corpus the change could not affect. Build 23 paid
// exactly that. Meanwhile `embed` and `sparse` declared only the BINARY, so the
// same change did NOT replay the steps whose entire output it decides — a silent
// staleness, which is the worse half.
//
// Splitting the file into rust/store/src/vectors.rs is what makes both statements
// expressible at once. The split was driven by evidence, not taste: every Store
// method each binary calls was listed first, and only what nothing but embed-io
// calls moved (clauses_is_view stayed, because compact.rs calls it too).
func TestTheLedgerImportInvalidatesEmbedAndSparseButNotMerge(t *testing.T) {
	const ledger = "rust/store/src/vectors.rs"

	for _, tc := range []struct {
		name    string
		step    *Step
		replays bool
	}{
		{"embed", stepEmbed(corpus3GPP()), true},
		{"sparse", stepSparse(corpus3GPP()), true},
		{"merge", stepMerge(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			implFixture(t, root, tc.step.Impl)
			// The ledger file must exist either way, or "unchanged" would only mean
			// "absent" and the merge case would pass for the wrong reason.
			write(t, filepath.Join(root, filepath.FromSlash(ledger)), "fn a() {}\n")

			before, _, err := implHash(root, tc.step.Impl, false)
			if err != nil {
				t.Fatal(err)
			}
			after := implAfterWriting(t, root, tc.step.Impl, ledger, "fn a() { let _ = 1; }\n")

			if tc.replays && before == after {
				t.Fatalf("%s does NOT re-run when the ledger import changes — it is the step "+
					"that import decides, and a stale corpus does not announce itself", tc.name)
			}
			if !tc.replays && before != after {
				t.Fatalf("%s re-runs when the ledger import changes — it never calls it; "+
					"measured cost on build 23: 38m01 of reconstruction", tc.name)
			}
		})
	}
}

// And merge must still re-run for the library it DOES use: the narrowing above
// must not have turned a loud waste into a silent staleness.
func TestMergeStillReRunsForTheLibraryItActuallyLinks(t *testing.T) {
	step := stepMerge()
	root := t.TempDir()
	implFixture(t, root, step.Impl)

	before, _, err := implHash(root, step.Impl, false)
	if err != nil {
		t.Fatal(err)
	}
	after := implAfterWriting(t, root, step.Impl, "rust/store/src/lib.rs", "fn a() { let _ = 1; }\n")
	if before == after {
		t.Fatal("merge ignores rust/store/src/lib.rs — the narrowing went too far, and a " +
			"stale corpus is worse than a wasted hour")
	}
}
