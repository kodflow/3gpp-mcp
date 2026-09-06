package store

import (
	"database/sql"
	"fmt"
)

// PROVING THE CONVERSION IS EXPENSIVE. PROVING THE PROOF STILL HOLDS IS NOT.
//
// `migrate-paragraphs --verify` rebuilds every body from its paragraphs and
// compares byte for byte. That is the right check and it is not cheap: on the
// shipped corpus it reads 23 GB and takes ~8 minutes, and the pipeline
// instantiates the step for BOTH halves, so it is ~15 minutes per build.
//
// It ran on every plan, including for a step that would be skipped. That is
// exactly the trade validatePublished refuses, in this repository's own words:
//
//	"Validate runs on EVERY plan, including for steps that would be skipped …
//	 What the record claims is confirmed at the moment it is written instead."
//
// So the full rebuild moves to the moment the conversion is written, and it
// leaves an ATTESTATION behind: the four counters the proof covered. Validate
// then re-counts and compares, which DuckDB answers from table metadata in
// milliseconds.
//
// WHAT THIS TRADE GIVES UP, stated rather than glossed. Counters cannot catch a
// body whose TEXT changed without its row count changing. It is a narrower
// guarantee than the rebuild. It is not a blind one: the attestation is only
// written after a rebuild passed, it names the shape that was proven, and it is
// invalidated by anything that adds, drops or re-derives a row — which is what
// every write in this pipeline actually does. The steps that touch the corpus
// after `paragraphs` (sparse, compact, index, embed-io) write vectors, indexes
// and metadata; none of them rewrites body text.
//
// The failure this replaces was not hypothetical either: an 8-minute read of a
// 23 GB file, twice, on every `make plan`, is what made planning cost more than
// most of the steps it was planning.

// ParagraphAttestationKey is where the attestation lives in the corpus itself,
// so it travels with the file rather than sitting in a sidecar that a copy,
// a compaction or a restore would leave behind.
const ParagraphAttestationKey = "paragraphs_verified"

// ParagraphCounters is the shape a verification covered.
type ParagraphCounters struct {
	Paragraphs, Bodies, BodySeq, ClauseOcc int64
}

func (c ParagraphCounters) String() string {
	return fmt.Sprintf("paragraphs=%d bodies=%d body_seq=%d clause_occ=%d",
		c.Paragraphs, c.Bodies, c.BodySeq, c.ClauseOcc)
}

// ReadParagraphCounters counts the four content-addressed tables. DuckDB answers
// count(*) from metadata, so this is milliseconds on a 23 GB corpus — the whole
// reason the attestation is worth having.
func ReadParagraphCounters(h *sql.DB) (ParagraphCounters, error) {
	var c ParagraphCounters
	err := h.QueryRow(`SELECT (SELECT count(*) FROM paragraphs),
	                          (SELECT count(*) FROM bodies),
	                          (SELECT count(*) FROM body_seq),
	                          (SELECT count(*) FROM clause_occ)`).
		Scan(&c.Paragraphs, &c.Bodies, &c.BodySeq, &c.ClauseOcc)
	if err != nil {
		return c, fmt.Errorf("counting the content-addressed tables: %w", err)
	}
	return c, nil
}

// StampParagraphAttestation records what a PASSING verification just covered.
//
// Call it only after the full rebuild succeeded. Stamping before, or stamping
// unconditionally, would produce the one failure mode worth fearing here: a
// record that outlives the thing it describes.
func StampParagraphAttestation(h *sql.DB) (ParagraphCounters, error) {
	c, err := ReadParagraphCounters(h)
	if err != nil {
		return c, err
	}
	if c.ClauseOcc == 0 {
		return c, fmt.Errorf("refusing to attest a corpus with no occurrences")
	}
	_, err = h.Exec(
		`INSERT INTO schema_meta(key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		ParagraphAttestationKey, c.String())
	if err != nil {
		return c, fmt.Errorf("stamping the attestation: %w", err)
	}
	return c, nil
}

// ErrNoParagraphAttestation says the corpus has never been attested — an older
// corpus, or one a restore reset. It is not a corruption signal: the caller runs
// the full verification and stamps, once.
type ErrNoParagraphAttestation struct{}

func (ErrNoParagraphAttestation) Error() string {
	return "the corpus carries no paragraph attestation"
}

// CheckParagraphAttestation is the CHEAP half: the corpus still has the shape a
// verification proved.
//
// It deliberately does NOT re-run the rebuild. It answers "is the proof still
// about this corpus", not "is this corpus correct" — and the difference is the
// point of splitting them.
func CheckParagraphAttestation(h *sql.DB) error {
	var stored string
	err := h.QueryRow(`SELECT COALESCE(MAX(value), '') FROM schema_meta WHERE key = ?`,
		ParagraphAttestationKey).Scan(&stored)
	if err != nil {
		return fmt.Errorf("reading the attestation: %w", err)
	}
	if stored == "" {
		return ErrNoParagraphAttestation{}
	}
	now, err := ReadParagraphCounters(h)
	if err != nil {
		return err
	}
	if now.String() != stored {
		return fmt.Errorf("the corpus no longer matches what was verified:\n  attested %s\n  now      %s",
			stored, now.String())
	}
	return nil
}
