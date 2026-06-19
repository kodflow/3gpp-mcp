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

// EnumerateIDs crawls /deliver/<typeDir>/ → range folders → number tokens and returns
// every deliverable's canonical id, sorted and de-duplicated. This is the ETSI
// analogue of 3GPP's status-report parse: it discovers the WHOLE corpus of a type
// (not a hand-listed scope), so the pipeline can fetch/index/ingest/embed the latest
// PUBLISHED version of every deliverable — the same completeness 3GPP has.
//
// failed carries the range folders that could not be listed (a transient fetch
// error), so the caller retries them rather than silently dropping deliverables.
func EnumerateIDs(fetch Fetcher, typeDir string) (ids []string, failed []string) {
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
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, failed
}
