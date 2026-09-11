package goal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A CERTIFICATE VOUCHES FOR THE BYTES IT CHECKED, UNDER THE FLAGS IT CHECKED THEM
// WITH, AND FOR NOTHING ELSE. Every way the corpus or the contract can differ from
// what `validate` saw must bring the script's own re-run back.
func TestTheContractCertificateVouchesOnlyForTheBytesItChecked(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags"] = "--require-fts --require-hnsw"
	c.Config["embed_floor"] = "Rel-99"
	for _, f := range certifiedFiles(c) {
		write(t, f, "corpus bytes")
	}
	if err := writeContractCertificate(c, corpus3GPP()); err != nil {
		t.Fatal(err)
	}
	if contractCertified(c, "Rel-99") == "" {
		t.Fatal("a certificate written over these exact files and flags was not honoured")
	}

	for _, tc := range []struct {
		name  string
		floor string
		mut   func()
		undo  func()
	}{
		{"the image applies another floor", "Rel-18", func() {}, func() {}},
		{"the contract flags changed", "Rel-99",
			func() { c.Config["contract_flags"] = "--require-fts" },
			func() { c.Config["contract_flags"] = "--require-fts --require-hnsw" }},
		{"the 3GPP corpus was rewritten", "Rel-99",
			func() { touchLater(t, c.dataPath("3gpp.duckdb")) }, nil},
		{"the ETSI corpus grew", "Rel-99",
			func() { write(t, c.dataPath("etsi.duckdb"), "corpus bytes, and more") }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.mut()
			if why := contractCertified(c, tc.floor); why != "" {
				t.Fatalf("honoured a certificate that does not describe this build: %s", why)
			}
			if tc.undo != nil {
				tc.undo()
			} else {
				// the files moved for good: re-certify for the next case
				if err := writeContractCertificate(c, corpus3GPP()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}

	voidContractCertificate(c)
	if contractCertified(c, "Rel-99") != "" {
		t.Fatal("a voided certificate was still honoured")
	}
	write(t, contractCertificatePath(c), "{not json")
	if contractCertified(c, "Rel-99") != "" {
		t.Fatal("an unreadable certificate was honoured")
	}
}

func touchLater(t *testing.T, p string) {
	t.Helper()
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
}

// THE CERTIFICATE IS VOID WHILE THE CONTRACT RUNS, AND WRITTEN ONLY AFTER BOTH
// VERDICTS. Read from the source because the order of three calls is the property:
// a certificate written before the anchor check, or left standing through a failed
// run, would vouch for bytes no gate passed.
func TestTheCertificateIsWrittenOnlyAfterEveryVerdict(t *testing.T) {
	src := readRepoFile(t, "internal/goal/pipeline_embed.go")
	start := strings.Index(src, "func stepValidate(t corpusTarget) *Step {")
	if start < 0 {
		t.Fatal("stepValidate not found")
	}
	body := src[start:]
	body = body[:strings.Index(body, "\n}\n")]
	void := strings.Index(body, "voidContractCertificate(c)")
	run := strings.Index(body, `c.Run(Cmd{Name: c.bin("validate")`)
	anchor := strings.Index(body, "validateAnchor(c)")
	cert := strings.Index(body, "writeContractCertificate(c, t)")
	if void < 0 || run < 0 || anchor < 0 || cert < 0 {
		t.Fatalf("cannot find the four calls (void=%d run=%d anchor=%d cert=%d)", void, run, anchor, cert)
	}
	if !(void < run && run < anchor && anchor < cert) {
		t.Fatalf("stepValidate must void the certificate, run the contract, check the anchor, THEN "+
			"certify — got void=%d run=%d anchor=%d cert=%d", void, run, anchor, cert)
	}
}

// AN INHERITED VARIABLE MUST NOT SKIP THE CONTRACT. runPublish hands the script
// os.Environ() plus contractEnv, so the uncertified answer has to SET the variable
// empty — the script tests -n — rather than leave it out.
func TestPublishDisarmsAnInheritedCertificate(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags"] = "--require-fts"
	t.Setenv("CORPUS_CONTRACT_CERTIFIED", "exported by hand in the operator's shell")

	env := contractEnv(c, "Rel-99")
	if len(env) != 1 || env[0] != "CORPUS_CONTRACT_CERTIFIED=" {
		t.Fatalf("with no certificate, contractEnv gave %q — an inherited CORPUS_CONTRACT_CERTIFIED would "+
			"reach build-image.sh and skip the contract", env)
	}

	for _, f := range certifiedFiles(c) {
		write(t, f, "corpus bytes")
	}
	c.Config["embed_floor"] = "Rel-99"
	if err := writeContractCertificate(c, corpus3GPP()); err != nil {
		t.Fatal(err)
	}
	env = contractEnv(c, "Rel-99")
	if len(env) != 1 || !strings.HasPrefix(env[0], "CORPUS_CONTRACT_CERTIFIED=certified by the validate step") {
		t.Fatalf("with a matching certificate, contractEnv gave %q", env)
	}

	script := readRepoFile(t, buildImageScript)
	if !strings.Contains(script, `if [ -n "${CORPUS_CONTRACT_CERTIFIED:-}" ]; then`) {
		t.Fatal("build-image.sh no longer tests CORPUS_CONTRACT_CERTIFIED with -n, so an empty value " +
			"may not disarm it")
	}
}

// BYTES REWRITTEN UNDER A RESTORED SIZE AND MTIME are the case a timestamp cannot
// see; the content sample does, wherever the change touches a sampled block — and
// a DuckDB file's header, at offset 0, changes on every checkpoint.
func TestACorpusRewrittenUnderItsOldMtimeVoidsTheCertificate(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags"] = "--require-fts"
	c.Config["embed_floor"] = "Rel-99"
	for _, f := range certifiedFiles(c) {
		write(t, f, strings.Repeat("corpus bytes ", 200000)) // > the whole-file sample limit
	}
	if err := writeContractCertificate(c, corpus3GPP()); err != nil {
		t.Fatal(err)
	}
	p := c.dataPath("3gpp.duckdb")
	st, _ := os.Stat(p)
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("HEADER CHANGED BY A CHECKPOINT"), 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chtimes(p, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(p); st2.Size() != st.Size() || !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("the test did not restore size and mtime, so it proves nothing")
	}
	if why := contractCertified(c, "Rel-99"); why != "" {
		t.Fatalf("a corpus rewritten under its old size and mtime kept its certificate: %s", why)
	}
}

// A CERTIFICATE FOR ANOTHER DATA DIRECTORY IS NOT ONE FOR THE CORPUS THE SCRIPT
// BAKES: build-image.sh reads <repo>/data whatever --data goal ran with.
func TestACertificateForAnotherDataDirectoryDoesNotExcuseThisOne(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags"] = "--require-fts"
	c.Config["embed_floor"] = "Rel-99"
	c.Data = filepath.Join(t.TempDir(), "elsewhere")
	for _, f := range certifiedFiles(c) {
		write(t, f, "corpus bytes")
	}
	if err := writeContractCertificate(c, corpus3GPP()); err != nil {
		t.Fatal(err)
	}
	if why := contractCertified(c, "Rel-99"); why != "" {
		t.Fatalf("a certificate for %s excused the corpus the script bakes from %s: %s",
			c.Data, filepath.Join(c.Root, "data"), why)
	}
}
