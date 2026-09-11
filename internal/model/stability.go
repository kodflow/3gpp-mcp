package model

import (
	"regexp"
	"strings"
)

// IsStableSpecVersion is the ONE stability rule a citation, a draft warning or a
// resolver may use, because stability is a fact about the HALF a version belongs
// to and the version string alone cannot tell the halves apart.
//
// THE 3GPP RULE WAS APPLIED TO ETSI, AND IT CALLED PUBLISHED STANDARDS DRAFTS.
// IsStableVersion says "major < 3 is a draft", which is how 3GPP numbers work in
// progress before a spec's first publication for a release. ETSI numbers editions
// V<major>.<technical>.<editorial> from V1.1.1, so its first PUBLISHED version of
// nearly everything is below 3. Measured on the corpus served 2026-09-11:
//
//   - get_spec answered "returned version 1.23.1 is a DRAFT" for ETSI TS 103 221-1
//     V1.23.1, a published TS, with stable:false on all 17 citations;
//   - 4 354 of the 5 142 ETSI deliverables have a newest version below 3, so
//     get_spec drew that warning for 84.7 % of the half;
//   - 1 373 152 of the 3 168 482 ETSI clauses (43.3 %) were cited stable:false.
//
// WHAT IS TRUE OF ETSI instead: every version the ETSI half holds was fetched
// from the /deliver PUBLICATION archive, milestone 60 — etsicat.LatestPublished
// and the all-versions crawl both drop every other milestone ("never cite a draft
// as a release"). On the same corpus all 11 822 versions sit in a "_60" folder,
// including the 14 whose major is 0 (ETSI EN 300 497-1 V0.3.2 and its siblings
// are publications too). So an ETSI version held here is stable, and an empty
// version — a deliverable the corpus does not hold — is not a version at all.
//
// The 3GPP half keeps IsStableVersion unchanged: 570 of its 20 163 versions are
// genuine drafts and must keep saying so.
//
// NOT "ANY NON-EMPTY STRING" (Qodo, #338). The rule vouches for what the crawl
// can produce: a /deliver folder "VV.VV.VV_60" becomes the dotted "V.V.V", and
// all 11 822 ETSI versions in the served corpus have exactly that shape. A value
// of any other shape did not come from that path, so nothing vouches for it and
// it is not called stable.
func IsStableSpecVersion(specID, version string) bool {
	if IsETSICorpusID(specID) {
		return reETSIPublishedVersion.MatchString(strings.TrimSpace(version))
	}
	return IsStableVersion(version)
}

// reETSIPublishedVersion is the shape etsicat.Version.String gives a milestone-60
// folder: three dotted integers.
var reETSIPublishedVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// IsETSICorpusID reports whether a spec_id names a deliverable of the ETSI half
// ("ETSI TS 103 221-1", "ETSI EN 300 497-1"). Same predicate the MCP router uses:
// trimmed, case-insensitive, "ETSI" prefix — the halves' ids are disjoint by
// construction (3GPP ids are dotted NN.NNN).
func IsETSICorpusID(specID string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(specID)), "ETSI")
}
