package store

// THE GO GLOSSARY WRITE PATH, IN A FILE OF ITS OWN SO THE ONE STEP THAT RUNS IT
// CAN DECLARE IT EXACTLY.
//
// `enrich` runs cmd/seed-glossary and declares cmd/seed-glossary,
// internal/glossaryseed and internal/abbrev as its implementation. The rows those
// packages produce reach the corpus through the methods below — and until
// 2026-09-11 these methods sat in store.go, which no data step declares. So a
// change to how the glossary is WRITTEN changed what enrich puts in the corpus
// without enrich replaying: the pipeline would have shipped the old glossary and
// reported it current. #323 placed its fix in internal/glossaryseed rather than
// in the store for exactly that reason, and said so in readSpec.
//
// WHY NOT DECLARE internal/store WHOLE. Every Go binary links store.go. Declaring
// it would put every serve-path edit, every counter and every ranking tweak on
// enrich's fingerprint, and enrich drags paragraphs, sparse, index and a corpus
// re-push behind it — to rewrite a glossary the edit did not touch. The Rust side
// made the same trade for the same reason: rust/store/src/changes.rs holds the
// changelog writer so that `enrich` can name it and `merge` need not replay.
// This file is its Go twin.
//
// WHAT LIVES HERE: every statement in this package that writes a row of
// `acronyms`, and SpecsWithAbbreviations — the sweep's scope. Since the
// replacement below, the scope decides which rows are DELETED, so it belongs to
// the write path even though it issues no write. GeneralVocabulary lives here for
// the same reason: since 2026-09-11 it decides whether a row the sweep dropped is
// deleted or handed back to TS 21.905.
//
// WHAT DOES NOT, AND STAYS UNDECLARED: GetClauses (store.go), through which the
// miner reads every clause — and glossaryseed.readGeneral reads TS 21.905. It is
// the serve path's main read, so moving it here would make every clause-lookup
// edit replay enrich. That residual is the one #323 documented in readSpec, and
// this file does not change it; readGeneral sorts the clauses it gets back itself
// rather than trusting their order.
//
// internal/goal/enrich_glossary_test.go holds both halves of the trade: enrich
// declares this file, and the methods in it have exactly the callers the
// declaration assumes.

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// seededSource is the provenance seed-glossary stamps on the rows it OWNS: the
// spec id of the spec whose Abbreviations clause declares the row — "23.501", or
// a multi-part id such as "38.101-1".
//
// OWNERSHIP IS READ FROM THE ROW, and it can be, because every writer of this
// table stamps a shape no other writer uses. Read from each writer on 2026-09-11:
//
//	seed-glossary (this file)       the declaring spec's id        "23.501"
//	  … handing a row back          TS 21.905's row, as its writer stamps it: "21"
//	rust ingest, TS 21.905 only     the two-digit series           "21"
//	rust ingest-glossary (ETSI)     the deliverable, etsi.duckdb   "ETSI TS 103 221-1"
//	rust merge, overlay; cmd/split  copy rows verbatim, stamp nothing of their own
//	UpsertAcronym below             whatever it is handed — tests only, no caller ships
//
// The second line is the one exception to "the seed stamps spec ids", and it is
// not a second owned shape: a row handed back is TS 21.905's again, byte for byte
// what the Rust ingest writes for it (see GeneralVocabulary), and the seed never
// touches it after that unless a spec declares its key again — or the newest
// TS 21.905 stops storing it, when it is retired (GlossaryDiff.Retired).
//
// Measured the same day on the shipped corpus: 14 127 rows, of which 13 722 cite a
// spec id (13 027 "NN.NNN" over 1 857 specs, 695 "NN.NNN-N" over 127) and 405
// cite "21". Nothing else. A second column agrees without being asked:
// declared_by is set on exactly those 13 722 and NULL on exactly those 405,
// because seed-glossary is the only 3GPP writer that counts declarations. And
// every one of the 13 722 names a spec present in `specs`.
//
// The grammar is the corpus's own: all 3 568 spec ids in `specs` match it (3 270
// plain, 298 multi-part). A spec id that did not would not be silently lost:
// ReplaceSeededAcronyms refuses to write a row it could never later remove.
var seededSource = regexp.MustCompile(`^[0-9]{2}\.[0-9]{3}(-[0-9]+)?$`)

// generalSource is the provenance of TS 21.905's rows: its two-digit series, as
// rust/parse/src/glossary.rs stamps it (GLOSSARY_SPEC_ID[..2]).
const generalSource = "21"

// seededBySpec reports whether a glossary row is seed-glossary's to replace.
//
// WHAT IT CANNOT SAY IS WHO ELSE DECLARES THE SAME KEY. The primary key is
// (term, expansion, domain), one row per key, and the seed REPLACES the row of a
// key it declares, provenance included — it has to, because TS 23.501 §3.2's
// precedence is read off that provenance (Store.ResolveTerm): an expansion a spec
// declares, left stamped "21", would rank below a rarer spelling some other spec
// declares. So when a spec and TS 21.905 declare the same key, the "21" row is
// overwritten and the corpus stops recording that TS 21.905 declared it.
//
// Measured 2026-09-11: re-parsing TS 21.905 v19.2.0's Abbreviations region from
// the stored clause text yields 1 300 keys — the 405 still stamped "21" plus 895
// now stamped with a spec id, and none missing. Releasing one of those 895 deletes
// a TS 21.905 entry that no step puts back: merge skips a shard with no changed
// bucket, and its fold is ON CONFLICT DO NOTHING in any case.
//
// This paragraph used to end "so the gap is latent today", and it was: the first
// sweep under the replacement released ONE row, "Multicast = MBS session" from
// 23.783, which is not among the 895. It was reachable by ordinary operation all
// the same — a spec takes a key over, a later issue of that spec drops the pair,
// and TS 21.905's entry leaves resolve_term while TS 21.905 still prints it.
//
// THE CORPUS DOES NOT REMEMBER THE CLAIM, BUT TS 21.905'S TEXT DOES. So the
// release no longer rests on this method alone: planSeededAcronyms asks
// GeneralVocabulary whether TS 21.905's writer stores the key, and a row it does
// store is HANDED BACK — re-stamped as TS 21.905's own — instead of deleted. A
// run that could not read TS 21.905 releases nothing at all.
func seededBySpec(source string) bool { return seededSource.MatchString(source) }

// GeneralPair is one (term, expansion) TS 21.905's writer stores. The domain is
// not part of it: every row of the general glossary carries domain "".
type GeneralPair struct{ Term, Expansion string }

// GeneralVocabulary is what TS 21.905's writer stores NOW, as glossaryseed reads
// it back from the corpus — the evidence a release is checked against.
//
// WHY A RELEASE NEEDS IT. A seeded row the sweep no longer declares used to be
// deleted on seededBySpec's word alone, and that word is only "a spec stamped
// this row last". When the row was TS 21.905's before a spec took its key over,
// the delete removed an entry TS 21.905 still declares — see seededBySpec. With
// this, such a row goes back to TS 21.905 instead, and a row TS 21.905's writer
// does not store is released exactly as before.
//
// WHAT THE WRITER STORES, NOT WHAT THE TEXT PRINTS. A hand-back re-creates a "21"
// row, so it may only re-create one TS 21.905's writer holds: a key a spec took
// over from it. Until 2026-09-11 Pairs was the text as the seed's own parser
// reads it, and on the local corpus that set held 9 keys the writer never stored
// (measured against rust/parse's line rule, which reproduces all 1 300 keys the
// writer did store). ADM, BOIC-exHC, BIC-Roam, GLONASS and SCF wrap onto a second
// line: the writer keeps the first and holds a "21" row cut there, the seed joins
// the wrap. CFNRc's "21" row keeps the double space TS 21.905 prints, which the
// seed collapses. "JAR file", "WLAN UE" and "O&M" have no "21" row at all: the
// writer's pattern refuses a space or an ampersand in a term. Handed back, each
// became a "21" row the writer never wrote — for the first six a SECOND one,
// beside the writer's own — served by ResolveTerm as a TS 21.905 entry for good,
// and the glossary then depended on which spec had once declared what.
//
// AND NOT A GUESS FROM THE TABLE. Asking the stored "21" rows whether one of them
// already "is" the dropped entry — the same expansion give or take a space, or
// one cut from the other — catches six of those 9, still hands back the three
// with no "21" row, and gets EF wrong. TS 21.905 prints EF twice, as "Elementary
// File" and as "Elementary File (on the UICC)", each on a line of its own, and its
// writer stored both; the first is a spec's today. Released, it reads as a cut of
// the second and is deleted — the defect this type exists to close. The mirror,
// the longer one taken over while the shorter is TS 21.905's, fails the other
// direction of the rule the same way. So a key is matched exactly against what
// the writer stores, and against nothing else.
//
// MEASURED 2026-09-11, read-only, on the local corpus as it stood that evening
// (13 793 seeded rows, 404 stamped "21"): 896 seeded rows carry a key TS 21.905's
// writer stored — every one taken over from it. Planned as if every spec holding
// one of those, or one of the 9 above, dropped it at once: the replacement before
// this type deleted all 905; with it the 896 are handed back and the 9 removed,
// and with TS 21.905 unread all 905 are withheld. The next real run on that
// corpus releases nothing: removed 0, restored 0, withheld 0 (seed-glossary
// --check-only).
//
// THE ZERO VALUE RELEASES NOTHING. Read is false unless a caller says the read
// succeeded, and an unread vocabulary cannot clear any key — so a caller that
// forgets to read TS 21.905, or whose read failed, withholds every release
// (GlossaryDiff.Withheld) rather than deleting on no evidence. Fail-open would be
// one forgotten argument away from the defect this type exists to close.
type GeneralVocabulary struct {
	// Read is true when Pairs is TS 21.905's whole vocabulary, read and parsed in
	// full. glossaryseed decides that, floor included; the store only obeys it.
	Read bool
	// Pairs are the keys TS 21.905's writer stores for its newest version — byte for
	// byte, a double space included — and no others. See glossaryseed.readGeneral.
	Pairs map[GeneralPair]bool
	// Release is that version's release — "Rel-19" — which the Rust ingest stamps
	// on both ends of each TS 21.905 row, and a row handed back is stamped with.
	Release string
}

// declares reports whether the key a stored row carries is one TS 21.905's writer
// stores — the only keys a hand-back may re-create.
func (g GeneralVocabulary) declares(k acronymKey) bool {
	return k.domain == "" && g.Pairs[GeneralPair{Term: k.term, Expansion: k.expansion}]
}

// handedBack is the row TS 21.905's own writer stores for a pair: rust/ingest's
// write_spec stamps the series, the release on both ends, domain "" and no count
// — declared_by NULL, which is what the 405 rows it wrote carry. Anything else
// would leave a restored row distinguishable from the entry it restores.
func (g GeneralVocabulary) handedBack(a model.Acronym) model.Acronym {
	return model.Acronym{Term: a.Term, Expansion: a.Expansion, Domain: a.Domain,
		FirstRelease: g.Release, LastRelease: g.Release, SourceSeries: generalSource}
}

// UpsertAcronym inserts or updates a glossary entry.
func (s *Store) UpsertAcronym(a model.Acronym) error {
	_, err := s.db.Exec(
		`INSERT INTO acronyms (term, expansion, domain, first_release, last_release, source_series, declared_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (term, expansion, domain) DO UPDATE SET
		   first_release=excluded.first_release, last_release=excluded.last_release,
		   source_series=excluded.source_series, declared_by=excluded.declared_by`,
		a.Term, a.Expansion, a.Domain, a.FirstRelease, a.LastRelease, a.SourceSeries,
		declaredBy(a))
	return err
}

// declaredBy renders the count for storage: a caller that did not count writes
// NULL, not 0.
//
// The distinction is the whole point of the column. Zero would mean "no document
// declares this", which is false of a row that exists at all, and it would sort
// BELOW every counted row — so a 3GPP writer that simply does not populate the
// field, as seed-glossary does not, would silently demote the spec-sourced rows
// the 3GPP ranking exists to promote. NULL reads as 1: one document, uncounted.
func declaredBy(a model.Acronym) any {
	if a.DeclaredBy <= 0 {
		return nil
	}
	return a.DeclaredBy
}

// GlossaryDiff is what replacing the seeded glossary does — or, from
// PlanSeededAcronyms, would do.
//
// A seeded row the batch no longer declares lands in exactly ONE of Removed,
// Restored and Withheld, and which one is decided by GeneralVocabulary alone.
type GlossaryDiff struct {
	// Written counts the rows the batch inserts or rewrites: keys the table does
	// not hold, and keys whose stored values differ from the batch's.
	Written int
	// Removed are the seeded rows the batch no longer declares, ordered by
	// (term, expansion, domain) so two runs over one corpus list them alike.
	Removed []model.Acronym
	// Restored are the seeded rows the batch no longer declares whose key
	// TS 21.905's writer stores, as they stand BEFORE the write — stamped with the
	// spec that took the key over. The write hands each back to TS 21.905 instead
	// of deleting it (GeneralVocabulary.handedBack). Same order as Removed.
	//
	// Not a deletion either: the key stays in resolve_term, and only its
	// precedence moves. The guard does not count it.
	Restored []model.Acronym
	// Withheld are the seeded rows the batch no longer declares that the write
	// leaves exactly as they are, because TS 21.905 could not be read and so no
	// key could be cleared. Same order as Removed; empty whenever it was read.
	//
	// NOT A DELETION, so the mass-removal guard does not count it — see Vanished
	// for the other half. A withheld row stays seeded, and every later run that
	// cannot read TS 21.905 withholds it again: the list grows until TS 21.905 is
	// readable. Counted as a release, as the hand-back was first written, it
	// refused the SECOND such run and every one after: 30 rows withheld, then 30
	// more against a bound of 53, came back "release 60 (0 removed, 0 handed back,
	// 60 withheld)", exit 1, from a run that changed nothing — and
	// --allow-mass-removal could not clear it, because there was nothing to remove.
	// A withheld row is judged the first time TS 21.905 is read again: it is then
	// removed or handed back, and the guard counts the removals.
	Withheld []model.Acronym
	// Retired are TS 21.905's OWN rows — stamped "21" — whose key the newest
	// version of TS 21.905 no longer stores, and that no spec of the batch
	// declares either. The write deletes them. Same order as Removed; empty
	// whenever TS 21.905 could not be read, because no key can be cleared then.
	//
	// WHY THE SEED, WHICH OTHERWISE NEVER TOUCHES A "21" ROW, TAKES THESE OUT.
	// The Rust ingest writes TS 21.905's rows when a version of it is ingested,
	// and the fold copies them into the corpus ON CONFLICT DO NOTHING: a new
	// version's keys are ADDED, and a key it corrected or dropped keeps its old
	// row for good, served by resolve_term as TS 21.905's entry beside the
	// current one. Measured 2026-09-11 over the 16 stored versions: 7 of the 15
	// successive issues dropped keys (1 to 8 each, 3 at v16.1.0, 2 at v17.2.0),
	// and a corpus built from all 16 would carry 25 keys v19.2.0 no longer
	// prints. Nothing else removes them: the ingest never deletes an acronym and
	// the seed owned only spec-stamped rows. This is the one reader that already
	// knows what the newest TS 21.905 stores (GeneralVocabulary), with the floor
	// and the unread state that make that knowledge safe to act on.
	//
	// A DELETION, and the mass-removal guard counts it as one.
	Retired []model.Acronym
	// Owned is how many seeded rows the table held BEFORE the replacement — the
	// base a removal is measured against.
	Owned int
	// Vanished are the specs that own seeded rows today, declare NOTHING in this
	// batch, are still listed in `specs`, and would lose at least one row to the
	// DELETE. Ordered by spec id.
	//
	// That is the signature of a broken read, not of an editorial change: the
	// catalogue says the spec is there, and the sweep came back with not one row
	// from it. It is deliberately NOT "ends with zero rows". A spec whose every
	// pair is also declared by a spec read after it ends with zero rows of its own
	// while being read perfectly — the corpus grew, the citation moved — and a
	// guard keyed on that would refuse ordinary growth.
	//
	// AND A DELETION MUST BE COMING, because deletions are what the guard stops. A
	// silent spec whose rows are all withheld, handed back to TS 21.905, or passed
	// to another spec that declares the same pair takes nothing out of
	// resolve_term: every key stays, cited by a document that declares it. Named,
	// it refused a run that deletes nothing — and, withheld rows never leaving
	// while TS 21.905 stays unread, every run after it, the same pile-up Withheld
	// describes. The run that would delete its rows names it; until then those
	// rows are reported as what they are.
	Vanished []VanishedSpec
}

// VanishedSpec is a spec the batch no longer hears from at all, and whose rows
// the write would delete.
type VanishedSpec struct {
	Spec string
	// Owned is how many seeded rows cite it today; Removed how many of those the
	// replacement deletes — never 0, or the spec is not listed — and Restored how
	// many it hands back to TS 21.905. The rest pass to another spec that
	// declares the same pair, which is why a vanished spec is shown by what it
	// owns. None is withheld: a deletion needs TS 21.905 read, and a run that read
	// it withholds nothing.
	Owned, Removed, Restored int
}

// Changed reports whether applying the diff moves the corpus at all. A withheld
// row is not a change: it is exactly the row the table already holds.
func (d GlossaryDiff) Changed() bool {
	return d.Written > 0 || len(d.Removed) > 0 || len(d.Restored) > 0 || len(d.Retired) > 0
}

// PlanSeededAcronyms computes what ReplaceSeededAcronyms would do, and writes
// nothing. It is the SAME computation, not a second copy of it: a check-only run
// that predicted the deletions with its own query would drift from the write the
// first time either was touched.
func (s *Store) PlanSeededAcronyms(as []model.Acronym, general GeneralVocabulary) (GlossaryDiff, error) {
	d, _, err := s.planSeededAcronyms(as, general)
	return d, err
}

// ReplaceSeededAcronyms makes the rows seed-glossary owns EQUAL to the batch, in
// ONE transaction: what the batch declares is written, what it no longer declares
// is released, and every row the seed does not own — see seededSource — is left
// exactly as it was.
//
// RELEASED MEANS ONE OF THREE THINGS, decided by general and nothing else: a row
// whose key TS 21.905's writer does not store is removed; a row whose key it does
// store is handed back to it, re-stamped exactly as that writer stamps it; and
// when TS 21.905 could not be read, every such row is withheld — left as it is —
// because no key could be cleared. See GeneralVocabulary and seededBySpec for the
// entry this protects. Only the first is a deletion.
//
// WHY REPLACE. This used to upsert and nothing else, so it could only ever grow
// the table: an expansion a spec corrected, or a clause the miner stopped
// choosing, kept its row for ever, served by resolve_term at the HIGHEST
// precedence. Measured 2026-09-11 on the shipped corpus, against the miner as
// #323 left it: 23.783 v18.0.0 carries clauses headed "Abbreviations" at both
// §3.2 — bare terms borrowed from TS 23.247, with no expansions — and §3.3.
// #323 ranks candidates by what they yield and now mines §3.3's 60 entries, so
// the row §3.2 produced, a split of the bare term "Multicast MBS session",
//
//	Multicast | MBS session      source 23.783, declared_by 1
//
// is declared by nothing the miner reads any more — and it was still there,
// because nothing could take it out. Two corpora with identical clauses and
// different seeding histories held different glossaries. The Rust ETSI miner
// learned the same lesson first (replace_mined_acronyms): the vocabulary is a
// SNAPSHOT of what the specs declare now, and an additive writer cannot express
// that.
//
// WHAT IT DOES NOT REMOVE IS A BAD ROW THE MINER STILL PRODUCES. The same
// measurement found
//
//	UA | User Agent ***** END SET OF CHANGES ***** ***** BEGIN SET OF CHANGES *****
//
// and it stays: it is the CURRENT output of 33.802 v0.2.0 §3.3, the clause #323
// chooses, whose continuation lines internal/abbrev glues onto the entry above
// them. That is a parser defect and its fix belongs there. What this method adds
// is that such a fix now TAKES: before it, a corrected parser would have written
// the clean row beside the corrupt one and left the corrupt one for ever.
//
// THE CALLER MUST HAND IT A FULL SWEEP, and only after the sweep was checked.
// A batch is read as the COMPLETE set of seeded rows, so a batch cut short by a
// broken read would delete everything it missed. glossaryseed.Run calls this only
// after its floor has passed and every spec has been read, and never on
// --check-only; the floor is what separates "the specs declare less" from "the
// read found less". An EMPTY batch is a no-op for the same reason: the only way
// to produce one is a read that found nothing, and a caller that turned the floor
// off (--min 0) must not be able to turn that into a wipe.
//
// AND THE FLOOR IS NOT ENOUGH, which is what approve is for. The floor watches
// the six preferred specs; a sweep that silently loses any of the other ~1 980
// passes it and — now that the write deletes — takes their rows with it, where
// the additive writer could only have left them stale. approve sees the planned
// diff, Vanished and Owned included, BEFORE the transaction opens: an error from
// it is returned with the diff and nothing is written. The policy lives with the
// caller (glossaryseed's mass-removal guard) because the opt-out is the caller's
// flag; the enforcement point lives here because it has to sit between the plan
// and the write, with nothing in between. A nil approve approves everything —
// tests use it; the one production caller does not.
//
// The batch form is not an optimisation either. These rows outrank the general
// vocabulary in Store.ResolveTerm, so a run that failed on row 400 of 679 would
// leave 399 rows that outrank the corpus's real answers, having exited non-zero as
// though it had changed nothing. All of them land or none do — and the removal
// commits with them or not at all, because a delete that commits without its
// insert is a glossary with a hole in it.
//
// AND IT WRITES NOTHING when the owned rows already equal the batch.
//
// Measured 2026-09-05 on the shipped 23 GB corpus: re-upserting the 679 seeded
// rows with identical values grew the file by 3 407 872 bytes. An
// ON CONFLICT DO UPDATE still writes a new row version; the old one is dead space
// until a checkpoint reclaims it, and the file has moved either way.
//
// A synthetic probe said otherwise — 5 000 identical upserts into a fresh
// two-megabyte database changed nothing at all — which is exactly why the number
// above was taken on the real corpus instead. A small isolated table is not a
// model of a 23 GB one.
//
// Three megabytes is not the cost. The corpus is one layer of the published
// image, and scripts/local/imgtar zeroes tar mtimes so a layer digest depends on
// CONTENT alone: an unchanged corpus is answered "existing blob" and never
// crosses the wire, one changed byte is an 11 GB upload. So the removal is part
// of the same skip: an owned row is deleted only when the batch does not declare
// it, and a row the batch declares with identical values is neither rewritten nor
// deleted-and-reinserted.
//
// Reading the table first is affordable precisely because it is small — some
// fourteen thousand rows. The same trade would be wrong on clauses.
func (s *Store) ReplaceSeededAcronyms(as []model.Acronym, general GeneralVocabulary,
	approve func(GlossaryDiff) error) (GlossaryDiff, error) {
	diff, pending, err := s.planSeededAcronyms(as, general)
	if err != nil {
		return diff, err
	}
	if approve != nil {
		if err := approve(diff); err != nil {
			return diff, err
		}
	}
	if !diff.Changed() {
		return diff, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return GlossaryDiff{}, err
	}
	// STAGE, THEN SWAP — one conflict check instead of one per row.
	//
	// This ran `INSERT … ON CONFLICT` per row inside the transaction, and DuckDB
	// checks each statement's conflict key against the transaction's OWN
	// uncommitted rows: quadratic in the batch. At the 679 rows six specs produce
	// it is invisible. At the 30 000 a corpus-wide sweep produces it does not
	// finish — measured 2026-09-08, 661 s of CPU and ZERO bytes written, which is
	// the same wall, the same signature and the same cause as the ETSI glossary
	// write killed twice at 2 h 29 earlier the same day.
	//
	// The rows land first in a TEMP table with no key and no index, so nothing is
	// checked against anything, and the swap is then set-based statements inside
	// the same transaction. The batch is already unique on (term, expansion,
	// domain) — planSeededAcronyms guarantees it — so the delete-then-insert has
	// exactly the ON CONFLICT DO UPDATE semantics it replaces.
	//
	// A ROW HANDED BACK TO TS 21.905 RIDES THE SAME SWAP: its TS 21.905 form is
	// staged beside the batch and inserted with it. That keeps the staged rows
	// unique too — a restored key is by definition one the batch does not declare,
	// and the table held it once.
	written := append([]model.Acronym(nil), pending...)
	for _, a := range diff.Restored {
		written = append(written, general.handedBack(a))
	}
	if err := stageAcronyms(tx, "staged_acronyms", written); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, err
	}
	if err := stageAcronyms(tx, "released_acronyms", diff.Removed); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, err
	}
	if err := stageAcronyms(tx, "restored_acronyms", diff.Restored); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, err
	}
	if err := stageAcronyms(tx, "retired_acronyms", diff.Retired); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, err
	}
	// THE RELEASE IS KEYED ON THE PROVENANCE IT WAS PLANNED FROM, not on the key
	// alone. A row whose source_series is no longer the one planSeededAcronyms
	// classified as seeded is not deleted here — so ownership is decided in ONE
	// place, seededBySpec, and this statement cannot widen it.
	//
	// THE PLAN AND THE WRITE MUST AGREE, or the report lies. A tuple comparison
	// that meets a NULL matches nothing and raises nothing: without the count, a
	// release that deleted fewer rows than it listed would print the full list
	// and leave the rows in place — the silent version of the defect this method
	// exists to end.
	//
	// Both kinds of release go through it: the rows removed, and the seeded form
	// of the rows handed back, whose TS 21.905 form the insert below then writes.
	for _, r := range []struct {
		table, what string
		planned     int
	}{
		{"released_acronyms", "remove", len(diff.Removed)},
		{"restored_acronyms", "hand back to TS 21.905", len(diff.Restored)},
		// Keyed on "21" like the others on their spec id: a row that stopped being
		// TS 21.905's between the plan and the write is not deleted.
		{"retired_acronyms", "retire from TS 21.905", len(diff.Retired)},
	} {
		res, err := tx.Exec(`DELETE FROM acronyms WHERE (term, expansion, domain, source_series) IN
			(SELECT term, expansion, domain, source_series FROM ` + r.table + `)`)
		if err != nil {
			_ = tx.Rollback()
			return GlossaryDiff{}, fmt.Errorf("%s the seeded rows no spec declares any more: %w", r.what, err)
		}
		if n, err := res.RowsAffected(); err != nil || n != int64(r.planned) {
			_ = tx.Rollback()
			return GlossaryDiff{}, fmt.Errorf("planned to %s %d seeded row(s) and the delete matched %d (%v) — "+
				"nothing was written", r.what, r.planned, n, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM acronyms WHERE (term, expansion, domain) IN
		(SELECT term, expansion, domain FROM staged_acronyms)`); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, fmt.Errorf("clear the rows this batch replaces: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO acronyms
		(term, expansion, domain, first_release, last_release, source_series, declared_by)
		SELECT term, expansion, domain, first_release, last_release, source_series, declared_by
		  FROM staged_acronyms`); err != nil {
		_ = tx.Rollback()
		return GlossaryDiff{}, fmt.Errorf("write the glossary batch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GlossaryDiff{}, err
	}
	return diff, nil
}

// planSeededAcronyms diffs the batch against the table: the rows to write, and the
// seeded rows to release.
func (s *Store) planSeededAcronyms(as []model.Acronym, general GeneralVocabulary) (GlossaryDiff, []model.Acronym, error) {
	if len(as) == 0 {
		return GlossaryDiff{}, nil, nil
	}
	// A ROW THIS REPLACEMENT COULD NEVER REMOVE IS REFUSED, NOT WRITTEN. The
	// release below reaches only rows seededBySpec recognises, so a batch row
	// carrying any other provenance would land and then outlive every later sweep
	// — the permanence this method exists to end, reintroduced by one caller.
	for _, a := range as {
		if !seededBySpec(a.SourceSeries) {
			return GlossaryDiff{}, nil, fmt.Errorf("refusing to seed %q = %q with provenance %q: "+
				"it is not a spec id, so no later sweep could remove it", a.Term, a.Expansion, a.SourceSeries)
		}
	}
	existing, err := s.acronymIndex()
	if err != nil {
		return GlossaryDiff{}, nil, err
	}
	// DEDUPLICATE THE BATCH FIRST, or the skip below cannot work.
	//
	// The identity is (term, expansion, domain), and two specs can declare the
	// same abbreviation with the same wording — 33.501 and 23.401 both write
	// "UP  User Plane". Those arrive as two rows sharing one key and differing
	// only in provenance, so each overwrites the other within a single call and
	// the last one wins. Comparing each incoming row against the database then
	// finds 145 of 679 "different" on a corpus that is already correct, because
	// half of each pair disagrees with whatever the other left behind.
	//
	// Measured 2026-09-05: that flip-flop moved the 23 GB corpus by ±3.9 MB on
	// every run, which is a new layer digest and an 11 GB push. Keeping the last
	// occurrence — what the database would end up holding anyway — makes the
	// batch converge, so a re-seed of an unchanged glossary writes nothing.
	want := make(map[acronymKey]model.Acronym, len(as))
	order := make([]acronymKey, 0, len(as))
	for _, a := range as {
		k := acronymKey{a.Term, a.Expansion, a.Domain}
		if _, seen := want[k]; !seen {
			order = append(order, k)
		}
		want[k] = a
	}
	var pending []model.Acronym
	for _, k := range order {
		a := want[k]
		if cur, ok := existing[k]; ok && cur == a {
			continue
		}
		pending = append(pending, a)
	}
	// WHERE A DROPPED ROW GOES is decided HERE, once, for the plan and the write
	// alike — so --check-only reports exactly the hand-backs the write performs.
	//
	// The order of the tests is the whole point. An unread TS 21.905 comes FIRST:
	// with no evidence, no key can be cleared, and "we could not tell" must never
	// read as "TS 21.905 does not declare it" — which is what a failed read that
	// returned an empty set would otherwise say, for every row at once.
	var removed, restored, withheld, retired []model.Acronym
	owned := map[string]int{}
	lost := map[string]int{}
	back := map[string]int{}
	for k, cur := range existing {
		// TS 21.905's own row, which the newest TS 21.905 no longer stores and no
		// spec of the batch declares: retired (GlossaryDiff.Retired). One the batch
		// declares is rewritten as the spec's below, exactly as before; with
		// TS 21.905 unread nothing is known, and nothing is retired.
		if cur.SourceSeries == generalSource {
			if _, declared := want[k]; !declared && general.Read && !general.declares(k) {
				retired = append(retired, cur)
			}
			continue
		}
		if !seededBySpec(cur.SourceSeries) {
			continue
		}
		owned[cur.SourceSeries]++
		if _, declared := want[k]; declared {
			continue
		}
		switch {
		case !general.Read:
			withheld = append(withheld, cur)
		case general.declares(k):
			restored = append(restored, cur)
			back[cur.SourceSeries]++
		default:
			removed = append(removed, cur)
			lost[cur.SourceSeries]++
		}
	}
	for _, rows := range [][]model.Acronym{removed, restored, withheld, retired} {
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if a.Term != b.Term {
				return a.Term < b.Term
			}
			if a.Expansion != b.Expansion {
				return a.Expansion < b.Expansion
			}
			return a.Domain < b.Domain
		})
	}
	diff := GlossaryDiff{Written: len(pending), Removed: removed, Restored: restored, Withheld: withheld,
		Retired: retired}
	for _, n := range owned {
		diff.Owned += n
	}
	// WHO IS STILL HEARD FROM is read off the batch BEFORE deduplication: every
	// spec's entries arrive under its own id there, including the pairs a later
	// spec will end up citing. Reading it off `want` instead would call a spec
	// silent merely because its pairs are also declared by one read after it.
	heard := map[string]bool{}
	for _, a := range as {
		heard[a.SourceSeries] = true
	}
	var silent []string
	for spec := range owned {
		if !heard[spec] {
			silent = append(silent, spec)
		}
	}
	if len(silent) > 0 {
		listed, err := s.catalogued()
		if err != nil {
			return GlossaryDiff{}, nil, err
		}
		sort.Strings(silent)
		for _, spec := range silent {
			// A spec the catalogue no longer lists has left the corpus, and its
			// rows leaving with it is the replacement doing its job. A spec losing
			// no row to the delete is not what the guard stops (see Vanished).
			if listed[spec] && lost[spec] > 0 {
				diff.Vanished = append(diff.Vanished, VanishedSpec{Spec: spec, Owned: owned[spec],
					Removed: lost[spec], Restored: back[spec]})
			}
		}
	}
	return diff, pending, nil
}

// catalogued returns the spec ids `specs` lists.
func (s *Store) catalogued() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT spec_id FROM specs`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// stageAcronyms loads rows into a fresh TEMP table shaped like `acronyms`, with no
// key and no index.
//
// Multi-row VALUES in chunks, because the cost removed is PER STATEMENT: DuckDB
// parses, plans and optimises each one.
func stageAcronyms(tx *sql.Tx, table string, rows []model.Acronym) error {
	if _, err := tx.Exec(`CREATE OR REPLACE TEMP TABLE ` + table + `(
		term VARCHAR, expansion VARCHAR, domain VARCHAR,
		first_release VARCHAR, last_release VARCHAR, source_series VARCHAR, declared_by BIGINT)`); err != nil {
		return err
	}
	const chunk = 1000
	for start := 0; start < len(rows); start += chunk {
		end := min(start+chunk, len(rows))
		batch := rows[start:end]
		q := `INSERT INTO ` + table + ` VALUES `
		args := make([]any, 0, len(batch)*7)
		for i, a := range batch {
			if i > 0 {
				q += ","
			}
			q += "(?,?,?,?,?,?,?)"
			args = append(args, a.Term, a.Expansion, a.Domain,
				a.FirstRelease, a.LastRelease, a.SourceSeries, declaredBy(a))
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("stage a batch of %d acronym(s) into %s: %w", len(batch), table, err)
		}
	}
	return nil
}

// acronymKey is the glossary's identity: the same term keeps every distinct
// expansion/domain, which is the whole point of the table.
type acronymKey struct{ term, expansion, domain string }

// acronymIndex reads the glossary into memory so ReplaceSeededAcronyms can tell a
// row that needs writing from one that is already correct, and a seeded row the
// batch still declares from one it has dropped.
//
// declared_by IS SELECTED, and leaving it out would have been the exact defect
// this repository's idempotence rule names: the comparison is a struct equality,
// so a field the reader does not load reads as its zero value on both sides and
// every row whose ONLY change is that field compares equal. The write would be
// skipped, the caller told "nothing to do", and the count would keep whatever a
// previous corpus had — an idempotence key that does not cover the output it
// claims to have written.
//
// UNLESS THE CORPUS HAS NO SUCH COLUMN, which only a read-only handle can see:
// Open migrates the column into place, OpenReadOnly does not, and --check-only
// opens read-only. Every row then compares as uncounted, which is what the write
// would find wrong with it.
func (s *Store) acronymIndex() (map[acronymKey]model.Acronym, error) {
	count := `coalesce(declared_by, 0)`
	if !s.declaredBy {
		count = `0`
	}
	rows, err := s.db.Query(
		`SELECT term, expansion, domain, first_release, last_release,
		        coalesce(source_series, ''), ` + count + `
		   FROM acronyms`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[acronymKey]model.Acronym, 16384)
	for rows.Next() {
		var a model.Acronym
		if err := rows.Scan(&a.Term, &a.Expansion, &a.Domain, &a.FirstRelease,
			&a.LastRelease, &a.SourceSeries, &a.DeclaredBy); err != nil {
			return nil, err
		}
		out[acronymKey{a.Term, a.Expansion, a.Domain}] = a
	}
	return out, rows.Err()
}

// SpecsWithAbbreviations lists every spec in the corpus that carries a clause
// headed exactly "Abbreviations".
//
// IT EXISTS BECAUSE THE GLOSSARY WAS SEEDED FROM SIX SPECS. Measured 2026-09-08
// on the shipped corpus: 3 497 specs carry such a clause and the seed read six of
// them, so the 3GPP half held 1 781 acronym rows while the ETSI half — mined from
// every deliverable — held 28 154. The miner existed on one arm only, which is the
// same shape as ingest-glossary being built and run by no step.
//
// The heading test is EqualFold on the trimmed heading, matching readSpec exactly.
// A LIKE '%abbreviation%' would also match "Definitions, symbols and
// abbreviations", the PARENT clause, whose body is a sentence of introduction —
// the same over-wide anchor that made the ETSI extractor read 8 % of its archive.
//
// IT LIVES IN THIS FILE BECAUSE IT NOW DECIDES DELETIONS. The sweep it returns is
// what ReplaceSeededAcronyms treats as the complete vocabulary, so a spec it stops
// listing has its rows removed on the next run. An edit here changes what enrich
// writes, and enrich must replay for it.
func (s *Store) SpecsWithAbbreviations(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT spec_id FROM clauses
		  WHERE lower(trim(heading)) = 'abbreviations'
		  ORDER BY spec_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
