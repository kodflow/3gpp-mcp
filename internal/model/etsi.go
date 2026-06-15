package model

// etsi.go — ETSI spec-id parsing + the deterministic ETSI "deliver" archive URL.
//
// The LI interfaces X1/X2/X3 are defined by ETSI (TS 103 221-1 = X1, 103 221-2 =
// X2/X3, 103 280 = identifiers, 103 120 = HI1/ADMF, …); 3GPP TS 33.128 only
// PROFILES them. 3GPP clause text references these as "ETSI TS 103 221-1" — a
// format the 3GPP miner (model/spec3gpp.go, NN.NNN) cannot see. This file lets the
// core RECOGNISE an ETSI id and CITE a pointer to its published PDF on the ETSI
// deliver archive — no ingestion, no PDF parsing (cite-or-silent, CLAUDE.md §1).
// Distinct from spec3gpp.go on purpose: ETSI ids are "1NN NNN[-P]" (space + part),
// versioned V<major>.<minor>.<editorial>, never the 3GPP "NN.NNN"/"Rel-NN" scheme.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// reEtsiID parses an ETSI deliverable id in any of the forms a 3GPP clause uses:
// "ETSI TS 103 221-1", "TS 103 221-1", "103 221-1", "103 280" (no part). The base
// is a 3-digit "1NN" + 3-digit number (optionally space-separated); an optional
// "-P" part suffix follows. Anchored so a bare 3GPP id ("23.501") never matches.
var reEtsiID = regexp.MustCompile(`^(?:ETSI\s+)?(?:T[SR]\s+)?(1\d{2})\s*(\d{3})(?:-(\d+))?$`)

// NormalizeEtsiID canonicalises an ETSI id mention to "1NN NNN[-P]" (e.g.
// "ETSI TS 103 221-1" -> "103 221-1", "TS 103 280" -> "103 280"). ok is false for
// anything that is not an ETSI-shaped id (notably every 3GPP "NN.NNN"), so callers
// can use it as the recognition predicate.
func NormalizeEtsiID(raw string) (id string, ok bool) {
	m := reEtsiID.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", false
	}
	id = m[1] + " " + m[2]
	if m[3] != "" {
		id += "-" + m[3]
	}
	return id, true
}

// etsiNumberToken turns a canonical id into the deliver-archive number token:
// "103 221-1" -> "10322101" (base digits + 2-digit zero-padded part), "103 280" ->
// "103280" (no part). Returns (token, base6, ok); base6 ("103221") drives the
// 100-range folder.
func etsiNumberToken(id string) (token, base6 string, ok bool) {
	m := reEtsiID.FindStringSubmatch(id)
	if m == nil {
		return "", "", false
	}
	base6 = m[1] + m[2] // "103" + "221" = "103221"
	token = base6
	if m[3] != "" {
		p, err := strconv.Atoi(m[3])
		if err != nil {
			return "", "", false
		}
		token += fmt.Sprintf("%02d", p)
	}
	return token, base6, true
}

// EtsiDeliverURL builds the deterministic ETSI deliver-archive URL for a published
// TS, e.g. ("103 221-1","1.21.1") ->
// https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf
// (number token "10322101"; 100-range folder "103200_103299"; version "01.21.01";
// "_60"=published milestone, "p"=PDF). When the version is absent/unparseable the
// exact file cannot be named, so it returns the spec's deliver FOLDER instead —
// cite-the-pointer, never fabricate a version (mirrors the 3GPP unindexed fallback
// in internal/mcp/server.go). Returns "" only if the id itself is not ETSI-shaped.
func EtsiDeliverURL(id, version string) string {
	token, base6, ok := etsiNumberToken(id)
	if !ok {
		return ""
	}
	floor, err := strconv.Atoi(base6)
	if err != nil {
		return ""
	}
	floor = (floor / 100) * 100
	rangeFolder := fmt.Sprintf("%06d_%06d", floor, floor+99)
	base := "https://www.etsi.org/deliver/etsi_ts/" + rangeFolder + "/" + token

	maj, mnr, ed, vok := parseEtsiVersion(version)
	if !vok {
		return base + "/" // landing folder: cite the pointer, no fabricated version
	}
	verFolder := fmt.Sprintf("%02d.%02d.%02d_60", maj, mnr, ed)
	verToken := fmt.Sprintf("%02d%02d%02d", maj, mnr, ed)
	return fmt.Sprintf("%s/%s/ts_%sv%sp.pdf", base, verFolder, token, verToken)
}

// ParseEtsiVersion splits an ETSI version "V1.21.1" / "1.21.1" into its three numeric
// parts (exported for the ETSI discover/diff in internal/etsicat). ok is false unless
// exactly three components parse.
func ParseEtsiVersion(version string) (major, minor, editorial int, ok bool) {
	return parseEtsiVersion(version)
}

// parseEtsiVersion splits an ETSI version "V1.21.1" / "1.21.1" into its three
// numeric parts. ok is false unless exactly three components parse, so a partial or
// 3GPP-style version never produces a wrong deliver URL.
func parseEtsiVersion(version string) (major, minor, editorial int, ok bool) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "V")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, false
	}
	if editorial, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, 0, false
	}
	return major, minor, editorial, true
}
