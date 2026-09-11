package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// THE CONTRACT IS RUN ONCE PER CORPUS STATE, NOT TWICE.
//
// build-image.sh re-runs cmd/validate over data/3gpp.duckdb before baking it — the
// gate that keeps a corpus failing its own contract out of an image. Inside the
// pipeline that gate had already run: `validate` holds the same binary to the same
// flags over the same file, minutes earlier, and publish stands on it through
// smoke. Measured on the 2026-09-11 publish: 8m15 of the script's 40m38 went to
// re-deriving a verdict nothing could have changed.
//
// So `validate` leaves a certificate naming exactly what it passed — the flags, the
// floor, and the identity (size + mtime to the nanosecond) of both corpus files —
// and publish tells the script to skip the re-run ONLY when every one of those
// still holds. Anything else, including no certificate at all and every
// standalone run of the script, validates as before. A certificate can make the
// publish faster; it cannot make it accept a corpus the contract did not see.

// contractCertificate is what a passing `validate` (3GPP arm) writes.
type contractCertificate struct {
	Flags       string            `json:"flags"`
	EmbedFloor  string            `json:"embed_floor"`
	Files       map[string]string `json:"files"`
	CertifiedAt string            `json:"certified_at"`
}

func contractCertificatePath(c *Ctx) string { return c.statePath("contract-certified.json") }

// certifiedFiles are the files the 3GPP contract reads: the corpus, and the ETSI
// half that --require-etsi opens. An absent ETSI file is recorded as absent, so its
// later appearance voids the certificate.
func certifiedFiles(c *Ctx) []string {
	return []string{c.dataPath("3gpp.duckdb"), c.dataPath("etsi.duckdb")}
}

// fileIdentity is size and mtime to the nanosecond, or "absent".
func fileIdentity(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("%d @%d", st.Size(), st.ModTime().UnixNano())
}

// voidContractCertificate is called BEFORE the contract runs: a failed or
// interrupted run must not leave the previous pass standing for bytes it did not
// check.
func voidContractCertificate(c *Ctx) {
	_ = os.Remove(contractCertificatePath(c))
}

// writeContractCertificate records a pass. Called only after cmd/validate and
// anchorcheck both succeeded.
func writeContractCertificate(c *Ctx, t corpusTarget) error {
	cert := contractCertificate{
		Flags:       c.Cfg(t.ContractKey),
		EmbedFloor:  t.Floor(c),
		Files:       map[string]string{},
		CertifiedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, f := range certifiedFiles(c) {
		cert.Files[filepath.Base(f)] = fileIdentity(f)
	}
	b, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(contractCertificatePath(c), append(b, '\n'))
}

// contractCertified says why the image build may skip re-running the contract, or
// "" when it must run it. imageFloor is the floor build-image.sh would apply
// (${EMBED_FLOOR:-…}), which is not necessarily the one `validate` used: the two
// knobs are separate today, and a certificate for another floor is not one for
// this build.
func contractCertified(c *Ctx, imageFloor string) string {
	b, err := os.ReadFile(contractCertificatePath(c))
	if err != nil {
		return ""
	}
	var cert contractCertificate
	if json.Unmarshal(b, &cert) != nil {
		return ""
	}
	if cert.Flags == "" || cert.Flags != c.Cfg("contract_flags") {
		return ""
	}
	if cert.EmbedFloor != imageFloor {
		return ""
	}
	var names []string
	for _, f := range certifiedFiles(c) {
		name := filepath.Base(f)
		got, ok := cert.Files[name]
		if !ok || got != fileIdentity(f) {
			return ""
		}
		names = append(names, name+" "+got)
	}
	sort.Strings(names)
	return fmt.Sprintf("certified by the validate step at %s over these exact bytes (%s), flags %q, floor %q",
		cert.CertifiedAt, strings.Join(names, "; "), cert.Flags, cert.EmbedFloor)
}

// contractEnv is what runPublish adds to build-image.sh's environment about the
// contract: the certificate's reason when it matches, and otherwise the variable
// set EMPTY.
//
// CLEARED, NOT OMITTED. Ctx.Run hands the child append(os.Environ(), …), so a
// CORPUS_CONTRACT_CERTIFIED left in the operator's shell would reach the script
// and skip the contract with no certificate behind it. The script tests -n, so an
// explicit empty value disarms an inherited one.
func contractEnv(c *Ctx, imageFloor string) []string {
	if why := contractCertified(c, imageFloor); why != "" {
		c.Log.Printf("corpus contract: %s", why)
		return []string{"CORPUS_CONTRACT_CERTIFIED=" + why}
	}
	c.Log.Printf("corpus contract: no certificate matches these bytes — build-image.sh re-runs it")
	return []string{"CORPUS_CONTRACT_CERTIFIED="}
}
