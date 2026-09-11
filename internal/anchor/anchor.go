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
// reads spec_versions and applies them.
package anchor

import (
	"encoding/json"
	"strings"
)

// CmpVer orders dotted numeric versions component-wise: "2.10.0" is AFTER
// "2.9.0", which string order gets wrong. A missing or non-numeric component
// counts as 0 (digits up to the first non-digit), so a malformed version never
// panics and never sorts above a real one.
//
// It is the rule cmd/anchorcheck judges an anchor by, and the one the fold's
// identity::cmp_ver applies when it writes one; cmd/anchorcheck's tests hold the
// two Go copies to each other, and the byte-identity measurement above holds the
// derivation to the fold.
func CmpVer(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := part(as, i), part(bs, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func part(s []string, i int) int {
	if i >= len(s) {
		return 0
	}
	n := 0
	for _, r := range s[i] {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
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
