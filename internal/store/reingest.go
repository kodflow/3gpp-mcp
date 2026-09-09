package store

import (
	"context"
	"fmt"
)

// THE RULE LIVES HERE, ONCE.
//
// A deliverable whose EVERY distinct clause row is stored more than once was
// written by more than one ingest. Two gates ask that question — cmd/validate on
// the build machine and `mcp-3gpp check-data` inside the image — and
// cmd/repair-reingest acts on it. Writing the SQL at each call site is the same
// rule written three times, and the copies drift the first time one is touched.
//
// WHY THE PREDICATE IS "every distinct row repeats" AND NOT "some row repeats".
// A document may legitimately repeat a clause: ETSI EN 300 607-1 is a GSM test
// spec whose tables restate the same numbered step, and the published corpus
// holds 831 192 such repeats across 3.1 M rows. Only a WHOLE document written
// again makes every one of its distinct rows appear at least twice.
//
// WHY RELEASE IS PART OF THE IDENTITY. A 3GPP TR is catalogued under every
// release it spans — 30.531 v1.62.0 exists under nine — so keying on
// (spec_id, version) alone reported one legitimate document as nine copies.
// Measured on the published corpus before this check was wired anywhere; it
// would have failed every 3GPP build on nothing.

// Reingested names one deliverable stored more than once. Copies is the
// multiplicity, and it is worth reporting because it dates the defect: 15 means
// fifteen ingests.
type Reingested struct {
	SpecID, Release, Version string
	Copies, Held             int
}

// String renders one offender for a gate message.
func (r Reingested) String() string {
	return fmt.Sprintf("%s %s v%s (%d copies, %d rows)", r.SpecID, r.Release, r.Version, r.Copies, r.Held)
}

// Excess is what the extra writes added. It is held - held/copies, NOT
// held - distinct_rows: a document that legitimately repeats a clause carries
// those repeats in every copy, so counting distinct rows over-reports the damage.
func (r Reingested) Excess() int { return r.Held - r.Held/r.Copies }

// ReingestedDeliverables scans the served view. Used by the two gates.
//
// It returns an error rather than an empty slice when the scan fails: a gate that
// turns a failed read into a pass is worse than no gate, because it reports the
// corpus as checked.
func (s *Store) ReingestedDeliverables(ctx context.Context) ([]Reingested, error) {
	return s.scanReingested(ctx, `
		SELECT spec_id, release, version, min(c) AS copies, sum(c) AS held
		FROM (
			SELECT spec_id, release, version, clause_path, heading, text, count(*) AS c
			FROM clauses GROUP BY 1, 2, 3, 4, 5, 6
		)
		GROUP BY 1, 2, 3 HAVING min(c) > 1
		ORDER BY sum(c) DESC`)
}

// ReingestedOccurrences asks the same question of clause_occ, which is the table
// a repair edits. body_id stands in for (heading, text) — that is exactly what a
// body is — so the two queries select the same deliverables while this one counts
// the rows that would actually be deleted.
func (s *Store) ReingestedOccurrences(ctx context.Context) ([]Reingested, error) {
	return s.scanReingested(ctx, `
		SELECT spec_id, release, version, min(c) AS copies, sum(c) AS held
		FROM (
			SELECT spec_id, release, version, clause_path, is_normative, body_id, count(*) AS c
			FROM clause_occ GROUP BY 1, 2, 3, 4, 5, 6
		)
		GROUP BY 1, 2, 3 HAVING min(c) > 1
		ORDER BY sum(c) DESC`)
}

func (s *Store) scanReingested(ctx context.Context, q string) ([]Reingested, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("scan for re-ingested deliverables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Reingested
	for rows.Next() {
		var r Reingested
		// A DISCARDED SCAN ERROR IS A GREEN GATE. Skipping the row on error dropped
		// the offender from the report, and if it was the only one the gate said
		// "no deliverable is stored more than once" about a corpus it had failed to
		// read. Same for rows.Err(): iteration that stops early on a driver error
		// leaves the slice empty, which is indistinguishable from a clean corpus.
		if err := rows.Scan(&r.SpecID, &r.Release, &r.Version, &r.Copies, &r.Held); err != nil {
			return nil, fmt.Errorf("read a re-ingest row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate re-ingest rows: %w", err)
	}
	return out, nil
}

// SummariseReingested renders a gate's detail line: the TOTAL group count, the
// total excess, and at most `sample` named offenders.
//
// The count and the sample are separate on purpose. Reporting len(sample) as the
// number of deliverables said "10 deliverable(s)" beside an excess taken from all
// thirty — a message that is self-contradictory exactly when the damage is worst.
func SummariseReingested(rs []Reingested, sample int) (groups, excess int, named string) {
	groups = len(rs)
	for _, r := range rs {
		excess += r.Excess()
	}
	for i, r := range rs {
		if i >= sample {
			named += fmt.Sprintf(", and %d more", len(rs)-sample)
			break
		}
		if i > 0 {
			named += ", "
		}
		named += r.String()
	}
	return groups, excess, named
}
