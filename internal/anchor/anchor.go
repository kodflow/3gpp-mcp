// Package anchor is the 3GPP delta anchor as a FUNCTION OF THE CORPUS.
//
// The anchor (.local/corpus-index.json, "spec|release -> highest version") is what
// discover diffs the live 3GPP status report against: a key the anchor already
// holds at the site's version is not fetched. It is written by the fold at the end
// of `ingest` (rust/store merge --index-out), from the spec_versions table of the
// corpus it just published — nothing else goes into it.
//
// WHY IT IS DERIVED, NOT DOWNLOADED. A fresh clone used to take its anchor from
// the `latest` GitHub release (corpus-index.json, last written 2026-06-05, which
// nothing republishes) while it took its corpus from a GHCR snapshot pinned by
// digest — two artefacts of two generations. Measured 2026-09-11 against the
// corpus the next snapshot will be published from: that anchor is behind it on
// 645 keys and lacks 140 more, so discover would re-acquire ~785 spec versions
// the snapshot already holds. The opposite drift is worse — an anchor AHEAD of
// its corpus makes discover skip specs that were never ingested, silently.
//
// An anchor recomputed from the corpus's own spec_versions is the same
// generation by construction, needs no publication at all, and is byte-identical
// to what the fold writes: measured on the 20 057-key corpus, the derivation and
// the fold's corpus-index.json are the same 554 235 bytes.
//
// This package is pure (no DuckDB): the rule and the format. cmd/derive-anchor
// reads spec_versions and applies them. Where the anchor sits in the pipeline —
// seed, discover, ingest's fold — is CLAUDE.md §6 ("Pipeline d'ingestion") and
// docs/local-pipeline.md; why it is derived is ADR 0003's second 2026-09-11
// amendment.
package anchor

import (
	"encoding/json"
	"strconv"
	"strings"
)

// CmpVer orders versions the way the FOLD does — rust/identity cmp_ver, which
// writes the canonical anchor: the first THREE dotted components, each parsed
// whole as a signed integer, anything unparseable (or absent) counting as 0.
// "2.10.0" is after "2.9.0", which string order gets wrong; "1.2.3.4" equals
// "1.2.3", because the fold never looks past the third component; "19x" is 0,
// not 19, because the fold parses the component whole.
//
// Matching the fold, not merely agreeing with it on today's data, is the point:
// the derived anchor must be the file the fold would have written. (On the real
// corpus every one of the 20 163 versions is a plain numeric triple, measured
// 2026-09-11, so the two readings coincide there; outside that shape they did
// not, and it is the fold's reading that discover has always been given.)
func CmpVer(a, b string) int {
	x, y := triple(a), triple(b)
	for i := range x {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// triple is cmp_ver's `v.split('.').take(3)` with `p.parse().unwrap_or(0)`:
// strconv.ParseInt accepts and refuses what Rust's i64 parse does (an optional
// sign, decimal digits, no spaces, no overflow).
func triple(v string) [3]int64 {
	var t [3]int64
	for i, p := range strings.SplitN(v, ".", 4) {
		if i == 3 {
			break
		}
		if n, err := strconv.ParseInt(p, 10, 64); err == nil {
			t[i] = n
		}
	}
	return t
}

// Index is an anchor: "spec|release" -> highest version.
type Index map[string]string

// Add records one spec_versions row, keeping the highest version per key.
func (ix Index) Add(spec, release, version string) {
	k := spec + "|" + release
	if cur, ok := ix[k]; !ok || CmpVer(version, cur) > 0 {
		ix[k] = version
	}
}

// Marshal renders the anchor exactly as the fold writes it: sorted keys, a
// one-space indent, no trailing newline. discover only needs the map, but the
// bytes matter too — the file is an Input of discover, and an anchor rewritten
// with the same content in another layout would still look like a change.
func (ix Index) Marshal() ([]byte, error) {
	return json.MarshalIndent(map[string]string(ix), "", " ")
}

// Parse reads an anchor file's content.
func Parse(b []byte) (Index, error) {
	ix := Index{}
	if err := json.Unmarshal(b, &ix); err != nil {
		return nil, err
	}
	return ix, nil
}

// Drift is how an anchor differs from the one its corpus implies.
//
// Behind and Missing cost re-work: discover re-requests what the corpus holds.
// Ahead and Extra are the dangerous direction: the anchor claims versions the
// corpus does not have, and discover skips them forever.
type Drift struct {
	Behind, Missing, Ahead, Extra int
}

// Zero reports whether the two anchors agree on every key.
func (d Drift) Zero() bool { return d == Drift{} }

// OverClaims reports whether the anchor claims anything the corpus lacks.
func (d Drift) OverClaims() bool { return d.Ahead+d.Extra > 0 }

// Compare measures `have` against `want`, the anchor derived from the corpus.
func Compare(have, want Index) Drift {
	var d Drift
	for k, wv := range want {
		hv, ok := have[k]
		switch {
		case !ok:
			d.Missing++
		case CmpVer(hv, wv) < 0:
			d.Behind++
		case CmpVer(hv, wv) > 0:
			d.Ahead++
		}
	}
	for k := range have {
		if _, ok := want[k]; !ok {
			d.Extra++
		}
	}
	return d
}
