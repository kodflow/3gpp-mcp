package etsicat

import (
	"regexp"
	"sort"
	"strconv"
)

// DeliverTypeDirs are the ETSI /deliver document-type folders that carry citable,
// normative-or-study deliverables. etsi_ts (Technical Spec) + etsi_tr (Technical
// Report) + etsi_en (European Norm) are the analogue of the 3GPP TS/TR universe;
// the same range→token→version crawl resolves any of them.
var DeliverTypeDirs = []string{"etsi_ts", "etsi_tr", "etsi_en"}

// deliverBase is the ETSI deliver-archive root (== the prefix in model.EtsiDeliverURL).
const deliverBase = "https://www.etsi.org/deliver/"

// reRangeFolder matches a deliver "100-range" folder, e.g. "103200_103299".
var reRangeFolder = regexp.MustCompile(`^\d{6}_\d{6}$`)

// reNumberToken matches a deliverable's number token: the 6-digit base plus zero or
// more 2-digit part numbers, e.g. "103280" (no part) or "10322101" (part 1).
var reNumberToken = regexp.MustCompile(`^\d{6}(\d{2})*$`)

// TokenToID reverses model.etsiNumberToken: the deliver number token back to the
// canonical id the rest of the pipeline keys on (NormalizeEtsiID form, BuildSite
// input). "103280" -> "103 280"; "10322101" -> "103 221-1"; "10319206" -> "103 192-6".
// Multi-part tokens chain ("-P-Q"). ok is false for anything not a valid token.
func TokenToID(token string) (string, bool) {
	if !reNumberToken.MatchString(token) {
		return "", false
	}
	id := token[:3] + " " + token[3:6]
	for rest := token[6:]; rest != ""; rest = rest[2:] {
		p, err := strconv.Atoi(rest[:2])
		if err != nil {
			return "", false
		}
		id += "-" + strconv.Itoa(p)
	}
	return id, true
}

// EnumerateIDs crawls one document-type folder and returns the bare ids. Kept for
// callers that already know the type; EnumerateDeliverables is what the --all crawl
// wants, because the type has to travel with the id.
func EnumerateIDs(fetch Fetcher, typeDir string) (ids []string, failed []string) {
	ds, failed := EnumerateDeliverables(fetch, typeDir)
	ids = make([]string, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.ID)
	}
	return ids, failed
}

// EnumerateDeliverables crawls /deliver/<typeDir>/ → range folders → number tokens
// and returns every deliverable, TAGGED WITH ITS FOLDER, sorted and de-duplicated.
// This is the ETSI analogue of 3GPP's status-report parse: it discovers the WHOLE
// corpus of a type (not a hand-listed scope), so the pipeline can fetch/index/
// ingest/embed the latest PUBLISHED version of every deliverable — the same
// completeness 3GPP has.
//
// The folder is carried rather than dropped because it cannot be recovered later:
// nothing in "103 101" says TR, and looking it up under etsi_ts returns a 404, not
// a redirect. The earlier version of this function returned bare ids, and the
// caller then resolved all of them as TS — which is why the crawl could report
// 7 501 deliverables while the work list ended up TS-only.
//
// failed carries the range folders that could not be listed (a transient fetch
// error), so the caller retries them rather than silently dropping deliverables.
func EnumerateDeliverables(fetch Fetcher, typeDir string) (ds []Deliverable, failed []string) {
	root := deliverBase + typeDir + "/"
	rc, err := fetch(root)
	if err != nil {
		return nil, []string{typeDir}
	}
	rangeLinks, err := ExtractLinks(rc)
	_ = rc.Close()
	if err != nil {
		return nil, []string{typeDir}
	}

	seen := map[string]bool{}
	for _, rl := range rangeLinks {
		rng := lastSegment(rl)
		if !reRangeFolder.MatchString(rng) {
			continue
		}
		rc2, err := fetch(root + rng + "/")
		if err != nil {
			failed = append(failed, rng)
			continue
		}
		tokenLinks, err := ExtractLinks(rc2)
		_ = rc2.Close()
		if err != nil {
			failed = append(failed, rng)
			continue
		}
		for _, tl := range tokenLinks {
			id, ok := TokenToID(lastSegment(tl))
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			ds = append(ds, Deliverable{TypeDir: typeDir, ID: id})
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].ID < ds[j].ID })
	return ds, failed
}

// ThreeGPPRepublication reports whether an ETSI deliverable id is ETSI's
// republication of a 3GPP specification rather than ETSI's own work.
//
// ETSI republishes every 3GPP deliverable under its own numbering by prefixing the
// 3GPP series with a 1: 3GPP TS 23.501 is ETSI TS 123 501, TR 26.978 is ETSI TR
// 126 978, TS 55.216 is ETSI TS 155 216. 3GPP's series are 21-38 and 41-55, which
// maps to the ETSI ranges 121 000-138 999 and 141 000-155 999.
//
// This matters because those deliverables are not merely redundant with the 3GPP
// half of this corpus — they are STRICTLY WORSE. The archive publishes one latest
// version of each, while the 3GPP side already carries every release: TR 26.978 is
// in this corpus across fifteen of them. Indexing the republication would spend the
// download, the conversion and the GPU on a single version of text that is already
// held in full lineage, under a second id that blurs provenance.
//
// Measured on the live archive (7 320 resolvable deliverables): 2 203 are
// republications, 5 117 are ETSI's own.
func ThreeGPPRepublication(id string) bool {
	base := 0
	digits := 0
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9':
			if digits == 6 {
				continue
			}
			base = base*10 + int(r-'0')
			digits++
		case r == ' ':
			// the id's internal space, not a separator between base and part
		default:
			// "-P": everything from the first part suffix on is irrelevant here
			digits = 6
		}
	}
	if digits < 6 {
		return false
	}
	series := base/1000 - 100 // 123501 -> 23
	return (series >= 21 && series <= 38) || (series >= 41 && series <= 55)
}
