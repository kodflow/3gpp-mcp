// Package subjectmeta is the CGO-free source of truth for domain-subject
// versioning and the 3GPP series each subject owns.
//
// Why a separate package from internal/subject + internal/registry: the
// `discover` binary runs WITHOUT CGO (it only fetches + diffs the 3GPP status
// report; the heavy DuckDB work stays in ingest/merge). It therefore cannot
// import the concrete subjects (li, glossary), which pull in the CGO store. But
// discover MUST know whether a subject changed so it can size the incremental
// matrix to include that subject's series. subjectmeta carries exactly that
// metadata with zero CGO dependencies, so discover, merge and ingest can all
// share it. The dynamic registry (which DOES import the concrete subjects) is
// kept in lockstep by TestSubjectMetaMatchesRegistry.
//
// Incremental contract (plan TROU #1): a subject's footprint is published in
// subject-index.json alongside corpus-index.json. On a delta run, discover
// compares each subject's published footprint against the current code; any
// mismatch forces that subject's series back into the build matrix, so the
// already-tested ingest→merge→resume machinery recomputes just that series.
// BUMP a subject's Version whenever its extraction logic changes.
package subjectmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// ASN1ScannerVersion tags the TS 33.128 ASN.1 scanner (internal/subject/li/asn1).
// It is folded into the LI subject's SourceHash so that an ASN.1 scanner change
// shifts the LI footprint independently of the hand-bumped Version constant —
// closing the `pv-omits-asn1-scanner` gap (a scanner improvement that forgets to
// bump li Version would otherwise be invisible to discover AND to the resume
// gate). BUMP this whenever the scanner's extraction output shape changes.
const ASN1ScannerVersion = "asn1-v1"

// Meta describes one domain subject for incremental-rebuild purposes.
type Meta struct {
	// Name is the stable subject id; MUST equal the subject's Subject.Name().
	Name string
	// Version is bumped whenever the subject's extraction logic changes, so its
	// footprint shifts and the next CI run re-indexes its series.
	Version string
	// SourceHash is a content-derived discriminator for the subject's extraction
	// machinery that is NOT captured by the hand-bumped Version — e.g. the ASN.1
	// scanner tag for LI. Folding it into Footprint makes a scanner/extractor
	// change self-invalidating instead of relying solely on a remembered string
	// bump (see ASN1ScannerVersion). Empty for subjects with no such auxiliary
	// machinery (their Version alone fully describes their extraction).
	SourceHash string
	// Series lists the 2-digit 3GPP series the subject owns (e.g. {"33"} for LI,
	// which owns TS 33.128). Drives which matrix shards a subject change rebuilds.
	Series []string
}

// All is the authoritative subject list. Keep it in lockstep with
// registry.Default() — TestSubjectMetaMatchesRegistry fails the build otherwise,
// so a subject can never be added without its incremental metadata.
var All = []Meta{
	{Name: "li", Version: "li-v1", SourceHash: ASN1ScannerVersion, Series: []string{"33"}},
	{Name: "glossary", Version: "glossary-v1", Series: []string{"21"}},
}

// Footprint is the per-subject digest compared base-vs-current to decide whether
// the subject changed. It covers the version tag AND the content-derived
// SourceHash (e.g. the ASN.1 scanner version), so a scanner change shifts the
// footprint even without a Version bump. Widening it further (e.g. embedding the
// subject's whole source tree) only means feeding more bytes into SourceHash —
// the published subject-index.json shape and the discover comparison stay
// identical.
func Footprint(m Meta) string {
	h := sha256.Sum256([]byte(m.Name + "|" + m.Version + "|" + m.SourceHash))
	return hex.EncodeToString(h[:])[:12]
}

// IngestFootprints returns every subject's footprint, sorted, for folding into
// model.SpecIngestIdentity. Sorting makes the result order-independent so the
// ingest gate is stable regardless of the order subjects are declared in All.
func IngestFootprints() []string {
	out := make([]string, 0, len(All))
	for _, m := range All {
		out = append(out, Footprint(m))
	}
	sort.Strings(out)
	return out
}

// Index returns name->footprint for every subject. Serialised to
// subject-index.json by merge, diffed by discover.
func Index() map[string]string {
	out := make(map[string]string, len(All))
	for _, m := range All {
		out[m.Name] = Footprint(m)
	}
	return out
}

// ChangedSeries returns the sorted set of series owned by any subject whose
// published footprint differs from the current code's (or is absent from the
// published index). Intended for the DELTA path only: an empty published map
// (no subject-index.json yet — e.g. the first run after this feature ships)
// returns every subject's series, so the subjects get re-indexed exactly once
// and the next merge publishes a complete index that subsequent runs compare
// precisely against. Callers on the FULL path don't need this (everything runs).
func ChangedSeries(published map[string]string) []string {
	seriesSet := map[string]bool{}
	for _, m := range All {
		if published[m.Name] != Footprint(m) {
			for _, s := range m.Series {
				seriesSet[s] = true
			}
		}
	}
	out := make([]string, 0, len(seriesSet))
	for s := range seriesSet {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
