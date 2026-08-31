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

// EtsiDeliverURL builds the deterministic ETSI deliver-archive URL for a published
// TS, e.g. ("103 221-1","1.21.1") ->
// https://www.etsi.org/deliver/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf
// (number token "10322101"; 100-range folder "103200_103299"; version "01.21.01";
// "_60"=published milestone, "p"=PDF). When the version is absent/unparseable the
// exact file cannot be named, so it returns the spec's deliver FOLDER instead —
// cite-the-pointer, never fabricate a version (mirrors the 3GPP unindexed fallback
// in internal/mcp/server.go). Returns "" only if the id itself is not ETSI-shaped.
func EtsiDeliverURL(id, version string) string {
	return EtsiDeliverURLIn(EtsiTypeTS, id, version)
}

// The three /deliver document-type folders this project crawls. The folder is not
// cosmetic: it selects the tree AND the file-name prefix, and the same number does
// not exist in two of them — /deliver/etsi_ts/103100_103199/103101/ is a 404 while
// /deliver/etsi_tr/103100_103199/103101/ is TR 103 101. Building a TR's URL under
// etsi_ts, which is what a single hardcoded folder did, therefore does not return
// the wrong document: it returns nothing, after five retries.
const (
	EtsiTypeTS = "etsi_ts"
	EtsiTypeTR = "etsi_tr"
	EtsiTypeEN = "etsi_en"
)

// EtsiDeliverURLIn builds the deterministic ETSI deliver-archive URL for a
// published deliverable in a given document-type folder:
//
//	("etsi_ts","103 221-1","1.21.1") -> …/etsi_ts/103200_103299/10322101/01.21.01_60/ts_10322101v012101p.pdf
//	("etsi_tr","103 101","1.1.1")    -> …/etsi_tr/103100_103199/103101/01.01.01_60/tr_103101v010101p.pdf
//	("etsi_en","301 893","2.2.1")    -> …/etsi_en/301800_301899/301893/02.02.01_60/en_301893v020201p.pdf
//
// An unknown typeDir yields "" rather than a plausible-looking URL into a tree
// that does not exist.
func EtsiDeliverURLIn(typeDir, id, version string) string {
	switch typeDir {
	case EtsiTypeTS, EtsiTypeTR, EtsiTypeEN:
	default:
		return ""
	}
	filePrefix := strings.TrimPrefix(typeDir, "etsi_") // ts | tr | en

	token, base6, ok := etsiArchiveToken(id)
	if !ok {
		return ""
	}
	floor, err := strconv.Atoi(base6)
	if err != nil {
		return ""
	}
	floor = (floor / 100) * 100
	rangeFolder := fmt.Sprintf("%06d_%06d", floor, floor+99)
	base := "https://www.etsi.org/deliver/" + typeDir + "/" + rangeFolder + "/" + token

	maj, mnr, ed, vok := parseEtsiVersion(version)
	if !vok {
		return base + "/" // landing folder: cite the pointer, no fabricated version
	}
	verFolder := fmt.Sprintf("%02d.%02d.%02d_60", maj, mnr, ed)
	verToken := fmt.Sprintf("%02d%02d%02d", maj, mnr, ed)
	return fmt.Sprintf("%s/%s/%s_%sv%sp.pdf", base, verFolder, filePrefix, token, verToken)
}

// reEtsiArchiveID is the ARCHIVE-side id parser, deliberately looser than
// reEtsiID: any 3-digit prefix, and any number of "-P" parts.
//
// reEtsiID is a RECOGNISER — it decides whether a string appearing in 3GPP clause
// text is an ETSI citation — so it is anchored on "1NN" to keep it from claiming
// arbitrary number pairs out of running prose. That constraint is right there and
// wrong here: ETSI ENs are numbered 3NN NNN (EN 301 893, EN 300 328), so every
// single EN in the /deliver archive parsed as "not ETSI-shaped" and got an EMPTY
// url in the work list. Two parsers because there are genuinely two jobs; they are
// next to each other so the difference stays visible.
var reEtsiArchiveID = regexp.MustCompile(`^(?:ETSI\s+)?(?:T[SR]|EN\s+)?\s*(\d{3})\s*(\d{3})((?:-\d+)*)$`)

// etsiArchiveToken turns an archive id into its deliver number token, the inverse
// of etsicat.TokenToID: "103 221-1" -> "10322101", "301 893" -> "301893",
// "103 192-6" -> "10319206". Multi-part ids chain their 2-digit parts.
func etsiArchiveToken(id string) (token, base6 string, ok bool) {
	m := reEtsiArchiveID.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return "", "", false
	}
	base6 = m[1] + m[2]
	token = base6
	for _, part := range strings.Split(strings.TrimPrefix(m[3], "-"), "-") {
		if part == "" {
			continue
		}
		p, err := strconv.Atoi(part)
		if err != nil || p < 0 || p > 99 {
			return "", "", false
		}
		token += fmt.Sprintf("%02d", p)
	}
	return token, base6, true
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
