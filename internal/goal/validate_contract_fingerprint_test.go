package goal

import "testing"

// TestTheContractFoldsIntoValidatesFingerprint fails when changing the contract a
// validate step applies — or the floor it applies it at — leaves the step's
// fingerprint unchanged.
//
// THE DEFECT THIS PINS. cmd/goal asks scripts/data-contract.sh for each arm's
// flags once, into c.Config, and the answer depends on DATA_CONTRACT. validate
// declared the script in Impl but not its ANSWER, so switching DATA_CONTRACT
// between dense and dense+sparse+etsi changed what the gate checked and not whether
// it ran: the step skipped on a verdict rendered under a different contract. The
// same shape was found in publish's fingerprint on 2026-09-10.
//
// Both arms are checked, because each reads its own config key and a fix wired to
// one ContractKey would leave the other arm exactly as it was.
func TestTheContractFoldsIntoValidatesFingerprint(t *testing.T) {
	for _, target := range []corpusTarget{corpus3GPP(), corpusETSI()} {
		s := stepValidate(target)
		if s.Extra == nil {
			t.Fatalf("%s declares no Extra: the contract it applies cannot reach its fingerprint", s.Name)
		}
		ctx := func(contract, floor string) *Ctx {
			return &Ctx{Config: map[string]string{
				target.ContractKey: contract,
				"embed_floor":      floor,
			}}
		}
		extra := func(c *Ctx) map[string]string {
			t.Helper()
			m, err := s.Extra(c)
			if err != nil {
				t.Fatalf("%s: Extra: %v", s.Name, err)
			}
			return m
		}
		full := "--require-fts --require-hnsw --require-embed-complete --require-sparse"
		dense := "--require-fts --require-hnsw --require-embed-complete"

		base := extra(ctx(full, "Rel-99"))
		if same(base, extra(ctx(dense, "Rel-99"))) {
			t.Errorf("%s: relaxing the contract from %q to %q leaves the fingerprint unchanged — "+
				"the gate would keep a verdict the weaker contract never rendered", s.Name, full, dense)
		}
		if same(base, extra(ctx(full, "Rel-99"))) == false {
			t.Errorf("%s: the same contract fingerprints differently on two evaluations — the step "+
				"would replay on every plan", s.Name)
		}
		// The 3GPP arm's floor comes from config; the ETSI arm's is fixed at "" by
		// design (a non-empty floor selects zero ETSI clauses). Only the arm that
		// can vary is required to vary.
		if target.Suffix == "" && same(base, extra(ctx(full, "Rel-17"))) {
			t.Errorf("%s: changing the embed floor leaves the fingerprint unchanged — --embed-floor "+
				"is a flag of the check", s.Name)
		}
	}
}

func same(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
