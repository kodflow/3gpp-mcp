package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// changelogBound is one side of get_changelog's range, classified BEFORE anything
// is queried — because the store only understands one of the three shapes a caller
// sends, and it answers the other two by ignoring them.
//
// MEASURED ON THE PUBLISHED CORPUS (2026-09-11, image sha256:b0e4ccbf…):
//
//	get_changelog(23.501)                                    3 064 records
//	get_changelog(23.501, Rel-18 .. Rel-18)                    631 records
//	get_changelog(23.501, 18.4.0 .. 18.5.0)                  3 064 records
//	get_changelog(23.501, from_release="banana")             3 064 records
//
// store.GetChangelog maps a bound through releaseMajor, and a label it cannot
// parse is "no bound" — the right reading of an EMPTY argument and the wrong one
// of a present argument it does not understand. A caller who asked for two
// versions got the whole history back with nothing saying the range was dropped:
// the defect #322 fixed for release bounds ("accepted and never applied"), still
// open for every other shape. The tool's own description of trace_clause tells a
// caller that an ETSI deliverable evolves along VERSIONS, so a version bound is not
// an exotic input here — it is the only kind an ETSI deliverable can take.
//
// So the handler sorts the argument into what the store can apply (a release), what
// it applies itself (a version), and what nobody can (anything else) — and says so
// in the answer when a bound was not applied, instead of pretending it was.
type changelogBound struct {
	raw     string // what the caller sent, trimmed
	release string // handed to the store: "Rel-18", "18"
	version string // applied here, on to_version: "18.4.0"
	invalid bool   // present, and neither of the above
}

var (
	// store.releaseMajor accepts "Rel-18", "rel-18" and a bare "18". Matching the
	// same set here keeps the two layers agreeing on what a release IS.
	reReleaseBound = regexp.MustCompile(`^(?:[Rr]el-)?\d+$`)
	// A dotted version. ETSI prints its versions "V1.23.1", so the leading V is
	// accepted and dropped: a caller copying from the cover page must not be told
	// their bound is unreadable.
	reVersionBound = regexp.MustCompile(`^[Vv]?(\d+(?:\.\d+){1,2})$`)
)

func parseChangelogBound(s string) changelogBound {
	b := changelogBound{raw: strings.TrimSpace(s)}
	switch {
	case b.raw == "":
	case reReleaseBound.MatchString(b.raw):
		b.release = b.raw
	case reVersionBound.MatchString(b.raw):
		b.version = reVersionBound.FindStringSubmatch(b.raw)[1]
	default:
		b.invalid = true
	}
	return b
}

// applyVersionBounds keeps the records whose to_version lies inside the version
// bounds, inclusive — the same column the store filters release bounds on, so the
// two shapes answer the same question ("which changes PRODUCED a version in this
// range") at two granularities.
//
// A record that names no to_version cannot be placed in a version range, so a
// version bound excludes it rather than guessing where it falls.
func applyVersionBounds(changes []model.Change, from, to changelogBound) []model.Change {
	if from.version == "" && to.version == "" {
		return changes
	}
	// A NEW slice, never changes[:0]: the handler keeps the unbounded set for the
	// coverage note, and filtering in place would rewrite it under that reader.
	out := make([]model.Change, 0, len(changes))
	for _, c := range changes {
		if strings.TrimSpace(c.ToVersion) == "" {
			continue
		}
		if from.version != "" && compareVersions(c.ToVersion, from.version) < 0 {
			continue
		}
		if to.version != "" && compareVersions(c.ToVersion, to.version) > 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// boundsNote names every bound that was NOT applied, or returns "".
func boundsNote(from, to changelogBound) string {
	var bad []string
	if from.invalid {
		bad = append(bad, fmt.Sprintf("from_release %q", from.raw))
	}
	if to.invalid {
		bad = append(bad, fmt.Sprintf("to_release %q", to.raw))
	}
	if len(bad) == 0 {
		return ""
	}
	return strings.Join(bad, " and ") + " is neither a release (Rel-18) nor a version (18.4.0), so it was NOT " +
		"applied: the records below are unbounded on that side."
}

// joinNotes puts two notes in one field, the bound problem first: a caller who
// learns the range was dropped needs to know that before reading anything the
// coverage note says about the records.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " " + b
	}
}

// etsiChangelogNote is the ETSI half's answer to "what does this number mean",
// and it is read from the corpus being served rather than written down once.
//
// THE ETSI HALF HAS TWO POSSIBLE STATES, AND THE NOTE MUST NOT DESCRIBE THE WRONG
// ONE. Today etsi.duckdb holds no change record at all, and the note has always
// said so. A writer that reads the change-history annex ETSI prints inside some of
// its deliverables would change that for those deliverables only — and a note
// still claiming "this corpus holds no change-request records for the ETSI half"
// would then be false for exactly the deliverables a caller is most likely to ask
// about. The key that tells the two states apart is the one the 3GPP half already
// uses: `changes_source`, stamped inside the transaction that writes the rows.
//
// And a third state the old code answered with SILENCE: an ETSI spec_id on a
// server started without the ETSI half. specStore then routes to the 3GPP store,
// which holds no such spec, and the handler's two branches both declined to speak
// — count 0, no note, on a question the server was never able to answer.
func etsiChangelogNote(ctx context.Context, etsi store.Reader, specID string, all []model.Change) string {
	const useTraceClause = "Use trace_clause with from_release/to_release (they accept two VERSIONS here) to " +
		"diff a clause between two published versions from the text itself."
	if etsi == nil {
		return "the ETSI half is not attached to this server (it was started without --etsi-db), so nothing " +
			"about " + specID + " can be answered here: count 0 is the absence of that corpus, not of changes."
	}
	// A STAMP THAT COULD NOT BE READ IS NOT AN ABSENT STAMP. Through GetMeta, a
	// failed read came back as "", and "" selects the branch below that says the
	// ETSI half holds no change record at all — printed beside the records the
	// same call had just served. metaReads keeps the failure apart (meta_reads.go),
	// and the no-records branch now also requires that there ARE no records.
	meta := newMetaReads(ctx, etsi)
	source, sourceOK := meta.get("changes_source")
	source = strings.TrimSpace(source)
	switch {
	case !sourceOK:
		source = "the ETSI half's changes_source stamp could not be read: " + meta.errs["changes_source"]
		if len(all) == 0 {
			return "this corpus holds no change-request record for " + specID + " — and " + source +
				", so this answer cannot say whether the ETSI half carries change records at all. A count of 0 " +
				"means \"no record here\", not \"never changed\". " + useTraceClause
		}
	case source == "" && len(all) == 0:
		return "this corpus holds no change-request records for the ETSI half: ETSI publishes PDFs, " +
			"and the change-history table does not survive text extraction well enough to cite. " +
			"A count of 0 means \"no record here\", not \"never changed\". " + useTraceClause
	case source == "":
		source = "no changes_source stamp is recorded"
	}
	if len(all) == 0 {
		return "this corpus holds no change-request record for " + specID + ". ETSI publishes PDFs, and its " +
			"change records are read only from the change-history annex a deliverable prints about itself, " +
			"and only where that annex names each change request on a line of its own (" + source + "); " +
			"the other layouts do not survive text extraction well enough to cite and are left out rather " +
			"than guessed. A count of 0 means \"no record here\", not \"never changed\". " + useTraceClause
	}
	return "these records are read from the change-history annex " + specID + " prints about itself (" + source +
		"), not from a change-request database: ETSI publishes none. to_version is the first published " +
		"version whose annex lists the change request and from_version the version before it; a request " +
		"already listed in the first version that carries the annex cannot be dated that way and is absent. " +
		"`meeting` is not recorded. Each citation is the published PDF whose annex lists the record. " +
		useTraceClause
}

// etsiChangeCitations cites, for each version the records name as their
// to_version, the published PDF of that version — the document whose annex the
// record was read from. One citation per version, in the order the records carry
// them, and only for versions the corpus holds: a version it does not hold has no
// text behind it here, and cite-or-silent (CLAUDE.md §1) does not stretch to a
// URL built for a document nobody fetched.
//
// The second result counts the records left WITHOUT a citation — a version the
// corpus does not hold, or a version list that could not be read — so the handler
// can say so rather than serve records beside an empty citations block in
// silence (CodeRabbit, #332). ingest-etsi-changes only writes a record whose two
// versions the corpus holds, so on a corpus it built this is 0.
func etsiChangeCitations(ctx context.Context, st store.Reader, specID string, changes []model.Change) ([]model.Citation, int) {
	cites := []model.Citation{}
	if len(changes) == 0 {
		return cites, 0
	}
	vs, err := st.ListReleases(ctx, specID)
	if err != nil {
		return cites, len(changes)
	}
	held := make(map[string]model.SpecVersion, len(vs))
	for _, v := range vs {
		held[v.Version] = v
	}
	seen := map[string]bool{}
	uncited := 0
	for _, c := range changes {
		v, ok := held[c.ToVersion]
		if !ok {
			uncited++
			continue
		}
		if seen[c.ToVersion] {
			continue
		}
		seen[c.ToVersion] = true
		url := v.DocxURL
		if url == "" {
			url = model.SpecURL(specID, v.Version)
		}
		cites = append(cites, model.Citation{
			SpecID: specID, Release: v.Release, Version: v.Version, URL: url,
			// Every version in the ETSI half was fetched from /deliver, which is
			// the PUBLICATION archive. model.IsStableVersion encodes the 3GPP rule
			// (major < 3 is a draft) and would call ETSI TS 103 221-1 V1.23.1 — a
			// published standard — a draft.
			Stable: true,
		})
	}
	return cites, uncited
}
