package store

import (
	"context"
	"database/sql"
	"time"
)

// ReleaseRow is one row of the releases calendar (axis #3).
type ReleaseRow struct {
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	StartDate     *time.Time `json:"start_date,omitempty"`
	FreezeDate    *time.Time `json:"freeze_date,omitempty"`
	FreezeMeeting string     `json:"freeze_meeting,omitempty"`
}

// UpsertReleases replaces the releases calendar.
func (s *Store) UpsertReleases(releases []ReleaseRow) error {
	return s.tx(func(tx *sql.Tx) error {
		for _, r := range releases {
			if _, err := tx.Exec(
				`INSERT INTO releases (code, name, status, start_date, freeze_date, freeze_meeting)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (code) DO UPDATE SET
				   name=excluded.name, status=excluded.status, start_date=excluded.start_date,
				   freeze_date=excluded.freeze_date, freeze_meeting=excluded.freeze_meeting`,
				r.Code, r.Name, r.Status, nullTime(r.StartDate), nullTime(r.FreezeDate), r.FreezeMeeting); err != nil {
				return err
			}
		}
		return nil
	})
}

// ApplyReleaseFreeze stamps each spec_versions row with its release's freeze_date
// + status (the DynaReport overlay; CLAUDE.md §8 piège #3 ordering fix). Returns
// the number of version rows updated.
func (s *Store) ApplyReleaseFreeze(ctx context.Context, releases []ReleaseRow) (int, error) {
	var n int
	for _, r := range releases {
		res, err := s.db.ExecContext(ctx,
			`UPDATE spec_versions SET freeze_date = ?, status = ? WHERE release = ?`,
			nullTime(r.FreezeDate), r.Status, r.Code)
		if err != nil {
			return n, err
		}
		if c, e := res.RowsAffected(); e == nil {
			n += int(c)
		}
	}
	return n, nil
}

// SetVersionMetadataSource tags every spec_versions row with its provenance.
func (s *Store) SetVersionMetadataSource(ctx context.Context, source string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE spec_versions SET metadata_source = ? WHERE metadata_source IS NULL`, source)
	return err
}

// SpecExists reports whether a spec row is present (catalog overlay only updates
// specs we actually have on disk — never invents catalogue-only specs).
func (s *Store) SpecExists(ctx context.Context, specID string) bool {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM specs WHERE spec_id = ?`, specID).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// UpdateSpecMeta overlays authoritative title/doc_type/working_group onto an
// existing spec row (DynaReport authoritative for metadata, §2.3). No-op if the
// spec is not on disk.
func (s *Store) UpdateSpecMeta(ctx context.Context, specID, title, docType, wg string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE specs SET title = ?, doc_type = ?, working_group = ? WHERE spec_id = ?`,
		title, docType, wg, specID)
	if err != nil {
		return false, err
	}
	c, _ := res.RowsAffected()
	return c > 0, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
