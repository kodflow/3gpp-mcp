package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// evolutionHalf is one corpus trace_evolution reads, with the name the answer
// reports it under.
type evolutionHalf struct {
	name string
	st   store.Reader
}

// evolutionHalves lists the halves this server can read edges from: the 3GPP
// store always, the ETSI store when it is attached.
//
// TRACE_EVOLUTION READ THE 3GPP HALF ALONE, AND SAID NOTHING ABOUT IT. The same
// defect class as #311 and the find_cross_references routing fix: every other
// lookup that can name an ETSI entity federates, and this one answered count 0
// for UICC, X1, ADMF or "ETSI TS 103 221-1" from a table the ETSI half was never
// consulted for — with the constant note "Curated NE↔NF seed", which reads as
// "we looked, and there is nothing". The ETSI half holds no edge today (no writer
// has ever seeded one), so federating changes no answer yet; what it changes is
// that the day an ETSI edge exists it is served, and that the answer can say, per
// half, how many edges there were to look at.
func (h *handlers) evolutionHalves() []evolutionHalf {
	halves := []evolutionHalf{{name: "3gpp", st: h.st}}
	if h.etsi != nil {
		halves = append(halves, evolutionHalf{name: "etsi", st: h.etsi})
	}
	return halves
}

// countEvolutions reads how many edges a half holds, or -1 when it cannot be read.
//
// READ FROM THE CORPUS BEING SERVED, not written into the note. changelogNote's
// history is the reason: an earlier draft of that note carried "covers 311 of the
// 3 568 specs", true of the snapshot it was measured on and silently false of
// every later one. A count read at call time cannot go stale against the corpus
// it describes.
func countEvolutions(ctx context.Context, st store.Reader) int {
	var n int
	if err := st.QueryRowContext(ctx, "SELECT count(*) FROM evolutions").Scan(&n); err != nil {
		return -1
	}
	return n
}

// evolutionCitation cites the clause that justifies one edge, from the half that
// owns the justification spec.
//
// The 3GPP path is the one the handler always had (the versioned archive ZIP, or
// the spec directory when the spec is not indexed). The ETSI path is new and only
// reachable once an ETSI edge exists: it must resolve the version in the ETSI
// store and cite the /deliver PDF — model.ArchiveURL knows nothing about ETSI and
// would return "" for it, which is a citation with no pointer.
func (h *handlers) evolutionCitation(ctx context.Context, e model.Evolution) model.Citation {
	spec := e.JustificationSpec
	rel, ver, _, _ := h.storeFor(spec).LatestVersion(ctx, spec)
	if isETSISpecID(spec) {
		return model.Citation{
			SpecID: spec, Release: rel, Version: ver, Clause: e.JustificationClause,
			URL: model.SpecURL(spec, ver),
			// A version in the ETSI half is a /deliver PUBLICATION; see
			// etsiChangeCitations for why the 3GPP draft rule does not apply.
			Stable: ver != "",
		}
	}
	// Fill release+version from the justification spec's latest indexed version
	// so the citation carries {spec_id, release, version, clause, url} whenever
	// the spec is in the corpus (issue #7). An evolution is inherently
	// cross-release, so the citation points at the spec's current state — the
	// clause is the curated justification anchor. If the spec isn't indexed,
	// release/version stay empty (cite-or-silent: we still give spec+clause+url).
	//
	// Prefer the exact versioned archive URL; fall back to the spec directory
	// when the justification spec isn't indexed (no version to encode) so the
	// citation always carries a resolvable URL.
	url := model.ArchiveURL(spec, ver)
	if url == "" {
		url = "https://www.3gpp.org/ftp/Specs/archive/" + model.SeriesOf(spec) + "_series/" + spec + "/"
	}
	return model.Citation{
		SpecID: spec, Release: rel, Version: ver,
		Clause: e.JustificationClause,
		URL:    url,
		Stable: model.IsStableVersion(ver),
	}
}

// reDocumentID recognises an entity argument that is a DOCUMENT rather than a
// network entity: a 3GPP spec number ("23.501", "TS 23.501") or an ETSI
// deliverable ("ETSI TS 103 221-1", "TS 102 232-1").
var reDocumentID = regexp.MustCompile(`^(?i)(?:ETSI\s+)?(?:(?:TS|TR|EN|ES)\s*)?(?:\d{2}\.\d{3}(?:-\d+)?|\d{3}\s?\d{3}(?:-\d+)?)$`)

// evolutionNote says what the answer is drawn from, and — when it is empty — what
// the empty answer does and does not mean.
//
// A ZERO FROM A CURATED TABLE IS THE TABLE'S SILENCE, NOT THE ENTITY'S HISTORY.
// The edges are a hand-checked seed (internal/evolseed), not a reading of the
// specifications: an entity outside it answers 0 whether or not it has a
// predecessor, and a caller who reads that 0 as "X replaced nothing" has been told
// something the corpus never said.
func evolutionNote(entity string, count int, held map[string]int, etsiAttached bool) string {
	var b strings.Builder
	if count > 0 {
		b.WriteString("Curated NE↔NF seed (V1 relational), each edge anchored to the clause that justifies it. " +
			"Full corpus-mined graph (KuzuDB) is V2.")
	} else {
		b.WriteString("no evolution edge names " + entity + " in this corpus. trace_evolution reads a CURATED " +
			"table of network-element → network-function edges, not the specification text")
		b.WriteString(heldClause(held, etsiAttached))
		b.WriteString(". A count of 0 means \"no curated edge\", not \"this entity has no predecessor or " +
			"successor\"; search_spec finds where the corpus itself describes it.")
	}
	if reDocumentID.MatchString(strings.TrimSpace(entity)) {
		b.WriteString(" " + entity + " names a DOCUMENT, not a network entity: for how a document changed, " +
			"use get_changelog (its change requests), list_releases (its versions) or trace_clause (its text " +
			"between two releases or versions).")
	}
	return b.String()
}

// heldClause renders the per-half edge counts for the note: " (the 3GPP half
// holds 45 edges, the ETSI half holds 0)".
func heldClause(held map[string]int, etsiAttached bool) string {
	part := func(name, label string) string {
		n, ok := held[name]
		switch {
		case !ok:
			return ""
		case n < 0:
			return "the " + label + " half's edges could not be counted"
		default:
			return fmt.Sprintf("the %s half holds %d", label, n)
		}
	}
	parts := []string{}
	if p := part("3gpp", "3GPP"); p != "" {
		parts = append(parts, p)
	}
	if etsiAttached {
		if p := part("etsi", "ETSI"); p != "" {
			parts = append(parts, p)
		}
	} else {
		parts = append(parts, "the ETSI half is not attached")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
