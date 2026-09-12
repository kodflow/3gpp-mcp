// Command repair-release-filing moves a stored document to the release its own
// version number names, when the corpus files it under one that number contradicts.
//
// WHAT IT REPAIRS. The 3GPP archive URL carries no release —
// .../archive/26_series/26.510/26510-i40.zip — so `spec_versions.release` was
// chosen by the DynaReport section that listed the row, copied into the work
// list, into data/sources/convert/<Rel-NN>/, and read straight back off the
// directory by the ingest (PR #350 fixed the choosing). The catalogue carries a
// version FORWARD into a release that never re-issued the spec, which is right
// and must be kept — but applied to a release still opening it filed Rel-18 and
// Rel-19 documents under Rel-20. Measured on the corpus published 2026-09-12:
// 12 filings hold 4 112 clause occurrences of Rel-18/Rel-19 text cited as
// Rel-20, and in all twelve that filing is the ONLY copy of the version in the
// corpus — get_spec(26.510, release=Rel-18, version=18.4.0) finds nothing while
// get_spec(26.510, release=Rel-20) serves 364 clauses of a Release-18 document.
//
// IT MOVES, IT NEVER DELETES. Three tables could hold the release axis and only
// two do: `spec_versions` and `clause_occ`. `clauses` is a VIEW over clause_occ
// joined to bodies; `clause_sparse` is keyed by chunk_id alone; bodies,
// paragraphs and body_seq are content-addressed and carry no release; the BM25
// index is fts_main_paragraphs, over paragraphs(para_id, part). So the repair is
// an UPDATE of clause_occ.release plus, where the destination filing does not yet
// exist, an INSERT of its spec_versions row. No chunk_id moves, no body moves, no
// vector moves, no posting is rewritten, and nothing is removed.
//
// The carrying spec_versions row is deliberately LEFT IN PLACE. It is what the
// catalogue said, and with its text gone it becomes exactly the bookkeeping row
// cmd/anchorcheck already expects — its NonContent verdict, "3GPP routinely lists
// a spec's Rel-N entry at the Rel-(N-1) version, so this is bookkeeping, not a
// gap". Deleting it would throw away a catalogue fact to fix a text fact.
//
// WHAT IT REFUSES, and each refusal is doing work on the published corpus:
//
//   - a filing with no occurrences: catalogue bookkeeping, there is nothing to
//     move (8 filings, including the four empty Rel-20 rows);
//   - a DRAFT (version major < 3): legitimately older than the release it is
//     drafted for, and "Rel-0" is not a release;
//   - a spec the corpus does not file under the destination release at all: the
//     51 Rel-4 filings of a 3.x.y Rel-99 document are the ONLY copy of 3 181
//     clauses and the corpus has no Rel-99 section, so Rel-4 is their only home —
//     moving them would be a deletion by another name. This is also what refuses
//     32.153-031 @ 27.14.31, the filename-parser artefact, whose "Rel-27" does
//     not exist;
//   - a destination that already holds occurrences of that same version: two
//     copies of one document, not a move (33.816 is filed at 10.0.0 under both
//     Rel-10 and Rel-11, 215 occurrences each, and today's catalogue says so).
//
// THE ATTESTATION SURVIVES. cmd/migrate-paragraphs attests the corpus with four
// counts — paragraphs, bodies, body_seq, clause_occ (internal/store.
// ParagraphCounters). This repair creates and destroys no row in any of them, so
// `migrate-paragraphs --attested` still passes and the `paragraphs` step keeps
// its 0.2 s no-op path. Proven on a copy, before and after.
//
// ALL TWELVE OR NONE. One transaction covers every move. It is run by hand, once,
// on a corpus that is then published, so a failure on the seventh filing must not
// leave six committed and an operator working out which six from the log. Recovery
// is "fix the cause and run it again": the tool is idempotent.
//
//	repair-release-filing --db data/3gpp.duckdb                  # dry run, reports
//	repair-release-filing --db data/3gpp.duckdb --apply          # writes
//	repair-release-filing --db data/3gpp.duckdb --report json    # for a machine
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "", "corpus DuckDB to repair (required)")
	apply := flag.Bool("apply", false, "actually move the filings; without it this only reports")
	report := flag.String("report", "text", `"text" for an operator, "json" for a machine (cmd/CLAUDE.md)`)
	flag.Parse()
	if *db == "" {
		fmt.Fprintln(os.Stderr, "repair-release-filing: --db is required")
		os.Exit(2)
	}
	if *report != "text" && *report != "json" {
		fmt.Fprintf(os.Stderr, "repair-release-filing: --report %q is neither text nor json\n", *report)
		os.Exit(2)
	}
	res, err := run(*db, *apply, *report == "json", os.Stdout)
	if err != nil {
		// A FAILURE IS A RESULT TOO. `--report json` promises a self-contained JSON
		// response on every path, so the error goes into it rather than beside it on
		// stderr, where a caller parsing stdout would see a truncated object and no
		// reason.
		if *report == "json" {
			if res == nil {
				res = &result{DB: *db, Applied: *apply}
			}
			res.Error = err.Error()
			_ = writeJSON(os.Stdout, res)
		}
		fmt.Fprintln(os.Stderr, "repair-release-filing:", err)
		os.Exit(1)
	}
	if *report == "json" {
		if err := writeJSON(os.Stdout, res); err != nil {
			fmt.Fprintln(os.Stderr, "repair-release-filing:", err)
			os.Exit(1)
		}
	}
}

// result is the machine-readable answer: every filing considered, what was decided
// about it and why, and whether the decision was carried out. `--report json`
// prints exactly this, on the success, dry-run, no-op and failure paths alike.
type result struct {
	DB          string      `json:"db"`
	Applied     bool        `json:"applied"`
	Candidates  int         `json:"candidates"`
	Moved       int         `json:"moved"`
	Occurrences int         `json:"occurrences"`
	LeftAlone   int         `json:"left_alone"`
	Filings     []filingOut `json:"filings"`
	Error       string      `json:"error,omitempty"`
}

// filingOut is one filing's decision. `action` is "move" or "keep"; a "keep"
// always carries its reason, because a repair that skips silently cannot be told
// apart from one that misses silently.
type filingOut struct {
	SpecID      string `json:"spec_id"`
	Release     string `json:"release"`
	Version     string `json:"version"`
	Target      string `json:"target"`
	Occurrences int    `json:"occurrences"`
	Action      string `json:"action"`
	Reason      string `json:"reason,omitempty"`
	Destination string `json:"destination,omitempty"`
}

func writeJSON(out *os.File, res *result) error { return writeJSONTo(out, res) }

func writeJSONTo(out io.Writer, res *result) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// filing is one (spec, release, version) row of spec_versions whose release the
// version's major contradicts, with everything the decision needs.
type filing struct {
	SpecID, Release, Version string
	Target                   string // the release the version's major names
	Occ                      int    // occurrences under Release
	OccAtTarget              int    // occurrences of the SAME version under Target
	SpecRowsAtTarget         int    // spec_versions rows for (spec, Target), any version
	ExactRowAtTarget         bool   // (spec, Target, version) already exists
	SourceURL                string // docx_url of the carrying row — the archive the text came from
	TargetURLEmpty           bool   // the destination filing exists and names no archive
}

// verdict says whether the filing moves, and why not when it does not. The reason
// is printed for every refusal: a repair that silently skips is indistinguishable
// from one that silently misses.
func (f filing) verdict() (move bool, why string) {
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

// run reports, and with apply=true writes. It opens READ-ONLY until it knows
// there is work, so a second pass over a repaired corpus cannot touch the file
// at all — it is run by hand on a 23 GB corpus whose every byte is an image layer.
//
// Measured, because the obvious justification for this turned out to be false:
// opening that corpus read-write and closing it without executing a statement
// leaves it identical to the byte on this DuckDB build. So the read-only open is
// a guarantee, not a repair of something observed — and TestASecondPassIsANoOp
// cannot falsify it, which is why the test asserts the file bytes rather than how
// the file was opened.
func run(dbPath string, apply, asJSON bool, out *os.File) (*result, error) {
	res := &result{DB: dbPath, Applied: apply}
	say := func(format string, args ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, args...)
		}
	}
	ro, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return res, fmt.Errorf("open %s: %w", dbPath, err)
	}
	ctx := context.Background()
	filings, err := candidates(ctx, ro.DB())
	_ = ro.Close()
	if err != nil {
		return res, err
	}

	var moving []filing
	var occ int
	// TWO FILINGS OF ONE VERSION CAN AIM AT THE SAME DESTINATION. The candidate
	// snapshot is read once, before anything moves, so "the destination already
	// holds occurrences of this version" is answered against the corpus as it was —
	// and a second filing of the same (spec, version) would pass that test on a
	// destination the first one has just filled, merging two documents' text under
	// one release. It does not happen on the corpus published 2026-09-12 (29.486 is
	// filed at 18.3.0 under both Rel-19 and Rel-20, but the Rel-19 filing carries no
	// text and is refused before this), which is exactly why it needs a guard rather
	// than a measurement: nothing would catch it the day it does.
	claimed := map[string]string{}
	for _, f := range filings {
		ok, why := f.verdict()
		if ok {
			dest := f.SpecID + "|" + f.Target + "|" + f.Version
			if from, taken := claimed[dest]; taken {
				ok, why = false, fmt.Sprintf("%s %s is already being moved to %s from %s — two filings, one destination",
					f.SpecID, f.Version, f.Target, from)
			} else {
				claimed[dest] = f.Release
			}
		}
		row := filingOut{
			SpecID: f.SpecID, Release: f.Release, Version: f.Version,
			Target: f.Target, Occurrences: f.Occ,
		}
		if ok {
			moving = append(moving, f)
			occ += f.Occ
			where, dest := "its filing is already there", "existing"
			switch {
			case !f.ExactRowAtTarget:
				where, dest = "its filing will be created", "created"
			case f.TargetURLEmpty && f.SourceURL != "":
				where, dest = "its filing is there but names no archive — the URL moves with the text", "existing, archive URL carried"
			}
			row.Action, row.Destination = "move", dest
			say("  MOVE %-11s %-7s %-9s -> %-7s %5d occurrence(s), %s\n",
				f.SpecID, f.Release, f.Version, f.Target, f.Occ, where)
		} else {
			row.Action, row.Reason = "keep", why
			say("  KEEP %-11s %-7s %-9s    %s\n", f.SpecID, f.Release, f.Version, why)
		}
		res.Filings = append(res.Filings, row)
	}
	res.Candidates, res.Occurrences, res.LeftAlone = len(filings), occ, len(filings)-len(moving)
	say("repair-release-filing: %d filing(s) whose release the version's major contradicts; %d to move, %d occurrence(s); %d left alone\n",
		len(filings), len(moving), occ, len(filings)-len(moving))

	if len(moving) == 0 {
		say("repair-release-filing: nothing to move — corpus untouched\n")
		return res, nil
	}
	if !apply {
		say("repair-release-filing: DRY RUN — pass --apply to write\n")
		return res, nil
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return res, fmt.Errorf("open %s read-write: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	if err := moveAll(ctx, st.DB(), moving); err != nil {
		return res, err
	}
	// Counted only once the commit succeeded: `moved` is what the corpus now holds,
	// not what was planned. On the failure path moveAll rolls everything back, so
	// `moved` stays 0 beside the error — which is the true report.
	res.Moved = len(moving)
	say("repair-release-filing: moved %d filing(s), %d occurrence(s)\n", len(moving), occ)
	return res, nil
}

// moveAll moves every filing in ONE transaction: all twelve or none.
//
// A transaction per filing was the first shape, and it is wrong for this tool.
// It is run by hand, once, on a 23 GB corpus that is then published — and a
// failure on the seventh filing would leave six moves committed, a corpus in a
// state nobody chose, and an operator who has to work out which six from the log.
// The set is twelve statements over 4 112 rows; there is no reason to give that up
// for a partial result. The command's own report is then true or the corpus is
// untouched, with nothing in between.
//
// Re-running after a rollback is safe and does the whole job: the tool is
// idempotent, so the recovery is "fix the cause and run it again".
func moveAll(ctx context.Context, db *sql.DB, moving []filing) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range moving {
		if err := moveOne(ctx, tx, f); err != nil {
			return fmt.Errorf("move %s %s v%s -> %s (nothing was committed): %w",
				f.SpecID, f.Release, f.Version, f.Target, err)
		}
	}
	return tx.Commit()
}

// moveOne re-files one document: the destination row first, so no occurrence is
// ever left pointing at a release spec_versions does not carry, then the
// occurrences. It verifies the count it moved and returns an error otherwise,
// which rolls the whole set back — see moveAll.
func moveOne(ctx context.Context, tx *sql.Tx, f filing) error {
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
		// cite where it was downloaded. Measured on a copy before this branch
		// existed: 6 of the 12 destinations had none. Only ever fills an empty
		// one; a destination that already names an archive keeps it.
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
// THE MAPPING IS DECIDED IN GO, ONCE. An earlier draft of this file expressed it
// in SQL as well, in a CASE that said the same thing — which is how the two come
// to disagree later. spec_versions is 20 163 rows: reading it whole and deciding
// here costs nothing and leaves releaseOfMajor the only definition.
//
// Drafts are excluded here rather than in the verdict: a draft is not a mis-filing
// to report and refuse, it is not a candidate at all, and "Rel-0" would print as a
// destination that never existed.
func candidates(ctx context.Context, db *sql.DB) ([]filing, error) {
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

	var out []filing
	specs := map[string]bool{}
	for _, k := range all {
		rmaj, ok := releaseMajor(k.rel)
		vmaj := versionMajor(k.ver)
		if !ok || vmaj < 3 || vmaj == rmaj {
			continue
		}
		target := releaseOfMajor(vmaj)
		dest := key{k.spec, target, k.ver}
		out = append(out, filing{
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
		am, _ := releaseMajor(a.Release)
		bm, _ := releaseMajor(b.Release)
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

// releaseMajor maps a release label to the version major it publishes, and says
// whether it recognised the label. Rel-99 ships 3.x.y — the release was named for
// the year and the numbering only lined up afterwards. Twin of internal/store.
// releaseMajor; kept here because this tool must not drag internal/store into its
// own change surface (that package is in the Impl of both validate steps).
func releaseMajor(rel string) (int, bool) {
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

// releaseOfMajor is the inverse, and the only place this tool decides where a
// document belongs: 3 -> Rel-99, else Rel-<major>. Twin of
// rust/parse::release_from_major and discover3gpp::release_from_major.
func releaseOfMajor(major int) string {
	if major == 3 {
		return "Rel-99"
	}
	return "Rel-" + strconv.Itoa(major)
}

// versionMajor is the leading component of a dotted version, or -1.
func versionMajor(v string) int {
	n, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil {
		return -1
	}
	return n
}
