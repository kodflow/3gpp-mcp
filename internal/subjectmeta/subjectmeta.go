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

// Meta describes one domain subject for incremental-rebuild purposes.
type Meta struct {
	// Name is the stable subject id; MUST equal the subject's Subject.Name().
	Name string
	// Version is bumped whenever the subject's extraction logic changes, so its
	// footprint shifts and the next CI run re-indexes its series.
	Version string
	// Series lists the 2-digit 3GPP series the subject owns (e.g. {"33"} for LI,
	// which owns TS 33.128). Drives which matrix shards a subject change rebuilds.
	Series []string
}

// All is the authoritative subject list. Keep it in lockstep with
// registry.Default() — TestSubjectMetaMatchesRegistry fails the build otherwise,
// so a subject can never be added without its incremental metadata.
var All = []Meta{
	{Name: "li", Version: "li-v1", Series: []string{"33"}},
	{Name: "glossary", Version: "glossary-v1", Series: []string{"21"}},
}

// Footprint is the per-subject digest compared base-vs-current to decide whether
// the subject changed. Today it covers the version tag; widening it (e.g. seed
// file content hashes) only means feeding more bytes here — the published
// subject-index.json shape and the discover comparison stay identical.
func Footprint(m Meta) string {
	h := sha256.Sum256([]byte(m.Name + "|" + m.Version))
	return hex.EncodeToString(h[:])[:12]
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
