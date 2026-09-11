package goal

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE SNAPSHOT A FRESH CLONE SEEDS FROM PASSES THE CONTRACT `validate` APPLIES,
// ARM BY ARM — driven here through the real publish-corpus.sh, in a root whose
// validate is a fake that records how it was called.
//
// The gate used to be a hand-written dense contract on the 3GPP half alone: no
// --require-sparse, no --require-etsi, no --require-no-reingest, and `--only etsi`
// ran no check whatsoever. `seed` and `mcp-3gpp serve` pull these packages, so a
// corpus this machine's own validate refuses could reach both.
func TestPublishCorpusHoldsEachArmToThePipelinesContract(t *testing.T) {
	root, record := fakePublishCorpusRoot(t)
	posixRoot := bashOutput(t, root, `pwd`)

	want := map[string]string{}
	for _, arm := range []string{"etsi", "3gpp"} {
		want[arm] = bashOutput(t, root, `DATA_ETSI_DB="$PWD/data/etsi.duckdb" DATA_EMBED_FLOOR=Rel-99 bash scripts/data-contract.sh `+arm)
	}

	out, err := runPublishCorpus(t, root, "--dry-run")
	if err != nil {
		t.Fatalf("the dry run failed: %v\n%s", err, out)
	}
	argv := readValidateCalls(t, record)
	if len(argv) != 2 {
		t.Fatalf("validate ran %d time(s), want once per arm:\n%v\n%s", len(argv), argv, out)
	}
	// ARGUMENT BY ARGUMENT, not as one string: the fake records each argument's
	// boundaries (review of #339). The contract is split into arguments exactly as
	// the pipeline splits it — strings.Fields in validateArgs — so the snapshot gate
	// and the validate step hand cmd/validate the same argv.
	calls := make([]string, len(argv))
	for i, arm := range []string{"etsi", "3gpp"} {
		db := posixRoot + "/data/" + map[string]string{"etsi": "etsi.duckdb", "3gpp": "3gpp.duckdb"}[arm]
		wantArgv := strings.Join(append([]string{"EMBED_MODEL=bge-m3-sparse", "--db", db, "--report", "text"},
			strings.Fields(want[arm])...), "|")
		if argv[i] != wantArgv {
			t.Errorf("the %s arm was checked with argv\n\t%s\nwant exactly what scripts/data-contract.sh gives that arm, "+
				"split as validateArgs splits it:\n\t%s", arm, argv[i], wantArgv)
		}
		calls[i] = strings.ReplaceAll(argv[i], "|", " ")
	}

	// And that contract is the pipeline's, not whatever the script happens to say:
	// every check `validate` applies to a published corpus.
	for _, f := range []string{"--require-fts", "--require-hnsw", "--require-embed-complete",
		"--require-no-reingest", "--require-sparse"} {
		for i, arm := range []string{"etsi", "3gpp"} {
			if !strings.Contains(" "+calls[i]+" ", " "+f+" ") {
				t.Errorf("the %s snapshot is published without %s", arm, f)
			}
		}
	}
	if !strings.Contains(calls[1], "--require-etsi "+posixRoot+"/data/etsi.duckdb") {
		t.Errorf("the 3GPP snapshot is published without the pair check against the local ETSI half: %s", calls[1])
	}
	if !strings.Contains(calls[1], "--embed-floor Rel-99") {
		t.Errorf("the 3GPP snapshot is not held to the pipeline's embed floor: %s", calls[1])
	}
	if !strings.Contains(calls[0], "--require-worklist") {
		t.Errorf("the ETSI snapshot is published without the work-list reconciliation: %s", calls[0])
	}
	if strings.Contains(calls[0], "--embed-floor") || strings.Contains(calls[0], "--require-etsi") {
		t.Errorf("the ETSI arm carries a 3GPP-only flag, which selects zero clauses or points the pair at itself: %s", calls[0])
	}
	if strings.Count(out, "DRY RUN — would push") != 2 {
		t.Errorf("the dry run did not reach both packages:\n%s", out)
	}
}

// A FAILING HALF STOPS THE PUBLISH BEFORE EITHER HALF IS PUSHED, and `--only etsi`
// is checked too — it used to skip the gate entirely.
func TestPublishCorpusChecksEveryArmBeforePushingAny(t *testing.T) {
	root, record := fakePublishCorpusRoot(t)
	t.Setenv("FAKE_VALIDATE_FAIL", "etsi.duckdb")
	out, err := runPublishCorpus(t, root, "--dry-run")
	if err == nil {
		t.Fatalf("an ETSI corpus that fails its contract was published:\n%s", out)
	}
	if strings.Contains(out, "would push") {
		t.Fatalf("a package was pushed although the other half failed its contract:\n%s", out)
	}

	t.Setenv("FAKE_VALIDATE_FAIL", "")
	_ = os.Remove(record)
	out, err = runPublishCorpus(t, root, "--dry-run", "--only", "etsi")
	if err != nil {
		t.Fatalf("--only etsi failed: %v\n%s", err, out)
	}
	calls := readValidateCalls(t, record)
	if len(calls) != 1 || !strings.Contains(calls[0], "|--db|") || !strings.Contains(calls[0], "/data/etsi.duckdb|") {
		t.Fatalf("--only etsi checked %v, want the ETSI corpus once", calls)
	}
}

// THE IMAGE'S BASE IS A DIGEST, NOT A TAG. debian:bookworm-slim moves on every
// Debian point release; pinned by tag, the same commit and the same fingerprint
// composed a different image. Read through both readers: shellDefault, which
// publish fingerprints with, and this package's independent one.
func TestTheImageBaseIsPinnedByDigest(t *testing.T) {
	pinned := regexp.MustCompile(`^[a-z0-9./-]+(:[\w.-]+)?@sha256:[0-9a-f]{64}$`)
	d, err := shellDefault(repoRootForTest(), buildImageScript, "IMAGE_BASE")
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.MatchString(d) {
		t.Fatalf("build-image.sh defaults IMAGE_BASE to %q, which a registry can move under the same name: pin it "+
			"by digest (see the comment above it for how to bump it)", d)
	}
	defaults := literalDefaultsOf(readLF(t, filepath.Join(repoRootForTest(), filepath.FromSlash(buildImageScript))), "IMAGE_BASE")
	if len(defaults) != 1 || defaults[0].Value != d {
		t.Fatalf("build-image.sh writes IMAGE_BASE defaults %v, want exactly the one publish fingerprints (%q)", defaults, d)
	}
	c := publishCtx(t)
	t.Setenv("IMAGE_BASE", "")
	m, err := publishStep(t).Extra(c)
	if err != nil {
		t.Fatal(err)
	}
	if m["image_base"] != d {
		t.Fatalf("publish fingerprints image_base=%q, the script builds on %q", m["image_base"], d)
	}
}

// ---------------------------------------------------------------- helpers

// fakePublishCorpusRoot is a checkout holding copies of the real publish-corpus.sh
// and data-contract.sh, two small corpora, an ETSI work list, a crane that is never
// reached by a dry run, and a validate that appends each call — its EMBED_MODEL
// and its arguments, "|"-separated so their boundaries survive — to the returned
// file, failing when an argument contains
// $FAKE_VALIDATE_FAIL. curl and gh are fakes too, so nothing leaves this machine.
func fakePublishCorpusRoot(t *testing.T) (root, record string) {
	t.Helper()
	root = t.TempDir()
	for _, rel := range []string{"scripts/local/publish-corpus.sh", "scripts/data-contract.sh", "scripts/lib/corpus-pin.sh"} {
		src := filepath.Join(repoRootForTest(), filepath.FromSlash(rel))
		if _, err := os.Stat(src); err != nil {
			continue // corpus-pin.sh exists only once that change is merged
		}
		write(t, filepath.Join(root, filepath.FromSlash(rel)), readLF(t, src))
	}
	record = filepath.Join(root, "validate-calls.txt")
	exe := func(p, body string) {
		write(t, p, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe(filepath.Join(root, ".local", "bin", "crane.exe"), `exit 0`)
	exe(filepath.Join(root, ".local", "bin", "validate.exe"),
		`{ printf 'EMBED_MODEL=%s' "${EMBED_MODEL:-}"; printf '|%s' "$@"; echo; } >> "`+filepath.ToSlash(record)+`"`+"\n"+
			`[ -n "${FAKE_VALIDATE_FAIL:-}" ] && case "$*" in *"$FAKE_VALIDATE_FAIL"*) exit 1;; esac`+"\n"+`exit 0`)
	write(t, filepath.Join(root, "data", "3gpp.duckdb"), "3gpp corpus bytes")
	write(t, filepath.Join(root, "data", "etsi.duckdb"), "etsi corpus bytes")
	write(t, filepath.Join(root, ".local", "state", "etsi-worklist.tsv"), "ts_102232-1\t3.30.1\n")

	bin := t.TempDir()
	exe(filepath.Join(bin, "curl"), `exit 0`)
	exe(filepath.Join(bin, "gh"), `exit 1`)
	path := bin
	// publish_one measures with stat, df and du and packs with tar; the ones Git
	// ships understand the /c/… paths the script builds, busybox's do not.
	if b, err := exec.LookPath("bash"); err == nil {
		path += string(os.PathListSeparator) + filepath.Dir(b)
	}
	t.Setenv("PATH", path+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, k := range []string{"DATA_CONTRACT", "DATA_EMBED_FLOOR", "EMBED_FLOOR", "DATA_ETSI_DB",
		"DATA_ETSI_WORKLIST", "DATA_ETSI_ABSENCES", "ETSI_ABSENCES", "EMBED_MODEL", "FAKE_VALIDATE_FAIL"} {
		t.Setenv(k, "")
	}
	t.Setenv("GHCR_PAT", "not-a-token")
	return root, record
}

func runPublishCorpus(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"scripts/local/publish-corpus.sh"}, args...)...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func bashOutput(t *testing.T, dir, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v", script, err)
	}
	return strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n"))
}

func readValidateCalls(t *testing.T, record string) []string {
	t.Helper()
	b, err := os.ReadFile(record)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}
