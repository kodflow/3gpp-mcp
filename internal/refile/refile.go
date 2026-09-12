// Package refile decides where a stored 3GPP document belongs when the corpus
// files it under a release its own version number contradicts, and carries out
// the move.
//
// WHAT THIS IS FOR. The 3GPP archive URL carries no release —
// .../archive/26_series/26.510/26510-i40.zip — so `spec_versions.release` was
// chosen by the DynaReport section that listed the row, copied into the work
// list, into data/sources/convert/<Rel-NN>/, and read straight back off the
// directory by the ingest (PR #350 fixed the choosing). The catalogue carries a
// version FORWARD into a release that never re-issued the spec, which is right
// and must be kept — but applied to a release still opening, it filed Rel-18 and
// Rel-19 documents under Rel-20. Measured on the corpus published 2026-09-12:
// 12 filings hold 4 112 clause occurrences of Rel-18/Rel-19 text cited as
// Rel-20, and in all twelve that filing is the ONLY copy of the version in the
// corpus — get_spec(26.510, release=Rel-18, version=18.4.0) found nothing while
// get_spec(26.510, release=Rel-20) served 364 clauses of a Release-18 document.
//
// IT MOVES, IT NEVER DELETES. Three tables could hold the release axis and only
// two do: `spec_versions` and `clause_occ`. `clauses` is a VIEW over clause_occ
// joined to bodies; `clause_sparse` is keyed by chunk_id alone; bodies,
// paragraphs and body_seq are content-addressed and carry no release; the BM25
// index is fts_main_paragraphs, over paragraphs(para_id, part). So the move is an
// UPDATE of clause_occ.release plus, where the destination filing does not yet
// exist, an INSERT of its spec_versions row. No chunk_id moves, no body moves, no
// vector moves, no posting is rewritten, and nothing is removed.
//
// The carrying spec_versions row is deliberately LEFT IN PLACE. It is what the
// catalogue said, and with its text gone it becomes exactly the bookkeeping row
// cmd/anchorcheck already expects — its NonContent verdict, "3GPP routinely lists
// a spec's Rel-N entry at the Rel-(N-1) version, so this is bookkeeping, not a
// gap". Deleting it would throw away a catalogue fact to fix a text fact, and
// would leave twelve anchor keys resolving to nothing.
//
// THE ATTESTATION SURVIVES. cmd/migrate-paragraphs attests the corpus with four
// row COUNTS (ParagraphCounters: paragraphs, bodies, body_seq, clause_occ). This
// creates and destroys no row in any of them, so `migrate-paragraphs --attested`
// still passes and the `paragraphs` step keeps its 0.2 s no-op path.
//
// Separate from internal/store on purpose: this is a bounded historical repair,
// not part of the serve path, and internal/store is in the Impl of both validate
// steps, of publish's serverImplPackages, and two of its files are in enrich's
// and index's. Nothing else imports this package, so it is compiled into the one
// binary that runs it.
package refile

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Filing is one (spec, release, version) row of spec_versions whose release the
// version's major contradicts, with everything the decision rests on.
type Filing struct {
	SpecID, Release, Version string
	Target                   string // the release the version's major names
	Occ                      int    // occurrences under Release
	OccAtTarget              int    // occurrences of the SAME version under Target
	SpecRowsAtTarget         int    // spec_versions rows for (spec, Target), any version
	ExactRowAtTarget         bool   // (spec, Target, version) already exists
	SourceURL                string // docx_url of the carrying row — the archive the text came from
	TargetURLEmpty           bool   // the destination filing exists and names no archive
}

// Decision is a Filing and what is to be done about it. A refusal ALWAYS carries
// its reason: a repair that skips silently cannot be told apart from one that
// misses silently.
type Decision struct {
	Filing
	Move bool
	Why  string // why not, when Move is false
	Dest string // what happens to the destination filing, when Move is true
}

// Destinations, as reported.
const (
	DestCreated     = "created"
	DestExisting    = "existing"
	DestURLCarried  = "existing, archive URL carried"
	destURLCarrying = "its filing is there but names no archive — the URL moves with the text"
)

// Detail is the operator-facing phrase for a move's destination.
func (d Decision) Detail() string {
	switch d.Dest {
	case DestCreated:
		return "its filing will be created"
	case DestURLCarried:
		return destURLCarrying
	default:
		return "its filing is already there"
	}
}

// verdict answers whether one filing moves, before the plan-wide guard below.
func (f Filing) verdict() (move bool, why string) {
	switch {
	case f.Occ == 0:
		return false, "catalogue bookkeeping — no occurrence to move"
	case f.SpecRowsAtTarget == 0:
		return false, fmt.Sprintf("the corpus files %s under no %s — %s is its only home", f.SpecID, f.Target, f.Release)
	case f.OccAtTarget > 0:
		return false, fmt.Sprintf("%s already holds %d occurrence(s) of %s — two copies, not a move", f.Target, f.OccAtTarget, f.Version)
	}
	return true, ""
}

// Plan reads every filing whose release the version's major contradicts and
// decides, for each, whether it moves. It writes nothing; give it a read-only
// handle.
//
// Each refusal does work on the published corpus. 8 filings carry no occurrence
// at all — catalogue bookkeeping, nothing to move. 51 Rel-4 filings of a 3.x.y
// Rel-99 document are the ONLY copy of 3 181 clauses and the corpus has no Rel-99
// section, so Rel-4 is their only home and moving them would be a deletion by
// another name; the same rule refuses 32.153-031 @ 27.14.31, the filename-parser
// artefact, whose "Rel-27" does not exist. 33.816 is filed at 10.0.0 under both
// Rel-10 and Rel-11 with 215 occurrences under each, which is two copies of one
// document and not a move. Drafts are not candidates at all.
func Plan(ctx context.Context, db *sql.DB) ([]Decision, error) {
	filings, err := candidates(ctx, db)
	if err != nil {
		return nil, err
	}
	// TWO FILINGS OF ONE VERSION CAN AIM AT THE SAME DESTINATION. The snapshot is
	// read once, before anything moves, so "the destination already holds
	// occurrences of this version" is answered against the corpus as it WAS — and a
	// second filing of the same (spec, version) would pass that test on a
	// destination the first has just filled. Both halves of that are live: with the
	// destination row absent on both, the second INSERT hits the primary key and the
	// run fails; with it present on both, there is no error at all and two
	// documents' occurrences merge under one release, which is worse because nothing
	// reports it.
	//
	// It does not happen on the corpus published 2026-09-12 — 29.486 is filed at
	// 18.3.0 under both Rel-19 and Rel-20, but the Rel-19 filing carries no text and
	// is refused above — which is exactly why it needs a guard rather than a
	// measurement: nothing would catch it the day it does.
	claimed := map[string]string{}
	out := make([]Decision, 0, len(filings))
	for _, f := range filings {
		d := Decision{Filing: f}
		d.Move, d.Why = f.verdict()
		if d.Move {
			key := f.SpecID + "|" + f.Target + "|" + f.Version
			if from, taken := claimed[key]; taken {
				d.Move = false
				d.Why = fmt.Sprintf("%s %s is already being moved to %s from %s — two filings, one destination",
					f.SpecID, f.Version, f.Target, from)
			} else {
				claimed[key] = f.Release
			}
		}
		if d.Move {
			switch {
			case !f.ExactRowAtTarget:
				d.Dest = DestCreated
			case f.TargetURLEmpty && f.SourceURL != "":
				d.Dest = DestURLCarried
			default:
				d.Dest = DestExisting
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// Apply moves every decision marked Move, in ONE transaction: all of them or none.
//
// A transaction per filing was the first shape and it is wrong for this repair.
// It is run by hand, once, on a 23 GB corpus that is then published — and a
// failure on the seventh filing would leave six moves committed, a corpus in a
// state nobody chose, and an operator working out which six from the log. The set
// is twelve statements over 4 112 rows; there is no reason to buy a partial result
// with that. The caller's report is then true, or the corpus is untouched, with
// nothing in between.
//
// Re-running after a rollback does the whole job: the repair is idempotent, so the
// recovery is "fix the cause and run it again".
func Apply(ctx context.Context, db *sql.DB, plan []Decision) (moved, occurrences int, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, d := range plan {
		if !d.Move {
			continue
		}
		if err := moveOne(ctx, tx, d.Filing); err != nil {
			return 0, 0, fmt.Errorf("move %s %s v%s -> %s (nothing was committed): %w",
				d.SpecID, d.Release, d.Version, d.Target, err)
		}
		moved++
		occurrences += d.Occ
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return moved, occurrences, nil
}

// moveOne re-files one document: the destination row first, so no occurrence is
// ever left pointing at a release spec_versions does not carry, then the
// occurrences. It verifies the count it moved and returns an error otherwise,
// which rolls the whole set back — see Apply.
func moveOne(ctx context.Context, tx *sql.Tx, f Filing) error {
	switch {
	case !f.ExactRowAtTarget:
		// Copy the carrying row's metadata rather than inventing it: docx_url is
		// the archive URL this very document came from.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO spec_versions (spec_id, release, version, freeze_date, docx_url, status, metadata_source)
			 SELECT spec_id, ?, version, freeze_date, docx_url, status, metadata_source
			 FROM spec_versions WHERE spec_id = ? AND release = ? AND version = ?`,
			f.Target, f.SpecID, f.Release, f.Version); err != nil {
			return fmt.Errorf("creating the destination filing: %w", err)
		}
	case f.TargetURLEmpty && f.SourceURL != "":
		// THE DESTINATION IS OFTEN THE ROW WITH NO FILE BEHIND IT. Six of the
		// twelve filings on the published corpus land on a bookkeeping row the
		// catalogue left with an empty docx_url — 26.510 Rel-18 18.4.0 is one —
		// while the mis-filed row carries the archive URL the text actually came
		// from. Moving the text without it leaves the served document unable to
		// cite where it was downloaded. Only ever fills an empty one; a destination
		// that already names an archive keeps it.
		if _, err := tx.ExecContext(ctx,
			`UPDATE spec_versions SET docx_url = ?
			 WHERE spec_id = ? AND release = ? AND version = ?
			   AND (docx_url IS NULL OR docx_url = '')`,
			f.SourceURL, f.SpecID, f.Target, f.Version); err != nil {
			return fmt.Errorf("carrying the archive URL to the destination filing: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE clause_occ SET release = ? WHERE spec_id = ? AND release = ? AND version = ?`,
		f.Target, f.SpecID, f.Release, f.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != int64(f.Occ) {
		return fmt.Errorf("moved %d occurrence(s), expected %d — refusing to commit", n, f.Occ)
	}
	return nil
}

// candidates reads every filing whose release the version's major contradicts,
// with the four facts the verdict rests on.
//
// THE MAPPING IS DECIDED IN GO, ONCE. An earlier draft expressed it in SQL as
// well, in a CASE that said the same thing — which is how two copies come to
// disagree later. spec_versions is 20 163 rows: reading it whole and deciding here
// costs nothing and leaves ReleaseOfMajor the only definition.
//
// Drafts are excluded here rather than in the verdict: a draft is not a mis-filing
// to report and refuse, it is not a candidate at all, and "Rel-0" would print as a
// destination that never existed.
func candidates(ctx context.Context, db *sql.DB) ([]Filing, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT spec_id, release, version, COALESCE(docx_url, '') FROM spec_versions`)
	if err != nil {
		return nil, fmt.Errorf("reading spec_versions: %w", err)
	}
	type key struct{ spec, rel, ver string }
	var all []key
	specRelRows := map[string]int{} // "spec|release" -> rows, any version
	url := map[key]string{}         // (spec, release, version) -> docx_url, "" when it names none
	exact := map[key]bool{}         // (spec, release, version) present
	for rows.Next() {
		var k key
		var u string
		if err := rows.Scan(&k.spec, &k.rel, &k.ver, &u); err != nil {
			_ = rows.Close()
			return nil, err
		}
		all = append(all, k)
		specRelRows[k.spec+"|"+k.rel]++
		url[k] = u
		exact[k] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Filing
	specs := map[string]bool{}
	for _, k := range all {
		rmaj, ok := ReleaseMajor(k.rel)
		vmaj := VersionMajor(k.ver)
		if !ok || vmaj < 3 || vmaj == rmaj {
			continue
		}
		target := ReleaseOfMajor(vmaj)
		dest := key{k.spec, target, k.ver}
		out = append(out, Filing{
			SpecID: k.spec, Release: k.rel, Version: k.ver, Target: target,
			SpecRowsAtTarget: specRelRows[k.spec+"|"+target],
			ExactRowAtTarget: exact[dest],
			SourceURL:        url[k],
			TargetURLEmpty:   exact[dest] && url[dest] == "",
		})
		specs[k.spec] = true
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		am, _ := ReleaseMajor(a.Release)
		bm, _ := ReleaseMajor(b.Release)
		if am != bm {
			return am < bm
		}
		if a.SpecID != b.SpecID {
			return a.SpecID < b.SpecID
		}
		return a.Version < b.Version
	})
	if len(out) == 0 {
		return out, nil
	}
	occ, err := occurrences(ctx, db, specs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		f := &out[i]
		f.Occ = occ[f.SpecID+"|"+f.Release+"|"+f.Version]
		f.OccAtTarget = occ[f.SpecID+"|"+f.Target+"|"+f.Version]
	}
	return out, nil
}

// occurrences counts clause_occ per (spec, release, version) for the specs that
// have a candidate filing — never for the whole corpus, which is 2.7 M rows.
func occurrences(ctx context.Context, db *sql.DB, specs map[string]bool) (map[string]int, error) {
	list := make([]string, 0, len(specs))
	args := make([]any, 0, len(specs))
	for s := range specs {
		list = append(list, "?")
		args = append(args, s)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT spec_id, release, version, count(*) FROM clause_occ
		 WHERE spec_id IN (`+strings.Join(list, ",")+`) GROUP BY 1, 2, 3`, args...)
	if err != nil {
		return nil, fmt.Errorf("counting occurrences: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var spec, rel, ver string
		var n int
		if err := rows.Scan(&spec, &rel, &ver, &n); err != nil {
			return nil, err
		}
		out[spec+"|"+rel+"|"+ver] = n
	}
	return out, rows.Err()
}

// ReleaseMajor maps a release label to the version major it publishes, and says
// whether it recognised the label. Rel-99 ships 3.x.y — the release was named for
// the year and the numbering only lined up afterwards. Twin of
// internal/store.releaseMajor; kept here because this package must not drag
// internal/store into its own change surface.
func ReleaseMajor(rel string) (int, bool) {
	r := strings.TrimSpace(rel)
	if !strings.HasPrefix(r, "Rel-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(r, "Rel-"))
	if err != nil {
		return 0, false
	}
	if n == 99 {
		return 3, true
	}
	return n, true
}

// ReleaseOfMajor is the inverse, and the only place this package decides where a
// document belongs: 3 -> Rel-99, else Rel-<major>. Twin of
// rust/parse::release_from_major and discover3gpp::release_from_major.
func ReleaseOfMajor(major int) string {
	if major == 3 {
		return "Rel-99"
	}
	return "Rel-" + strconv.Itoa(major)
}

// VersionMajor is the leading component of a dotted version, or -1.
func VersionMajor(v string) int {
	n, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil {
		return -1
	}
	return n
}
