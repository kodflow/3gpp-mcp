package li

import (
	"context"
	"database/sql"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject/li/asn1"
)

// LIEvent is a row of the authoritative li_events registry (TS 33.128 ASN.1).
type LIEvent struct {
	Release       string `json:"release"`
	ModuleVersion string `json:"module_version"`
	Interface     string `json:"interface"`
	EventName     string `json:"event_name"`
	ASN1Type      string `json:"asn1_type"`
	ASN1Tag       int    `json:"asn1_tag"`
	NF            string `json:"originating_nf"`
	Domain        string `json:"domain"`
	Clause        string `json:"spec_clause"`
	FieldCount    int    `json:"field_count"`
}

// withTx runs fn in a transaction on the store's DB, rolling back on error.
func withTx(st *store.Store, fn func(*sql.Tx) error) error {
	tx, err := st.DB().Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Purge deletes every LI-owned row for one (spec_id, release) so a --resume redo
// of TS 33.128 starts from a clean slate. The generic store.PurgeSpecScope only
// knows the core tables; without this the resume redo would re-run the inserts
// over stale rows (a corrected asn1_tag/spec_clause kept by ON CONFLICT, removed
// events lingering). Release-scoped: LI's tables are keyed by release, not
// version (version is accepted to satisfy the subject.Purger contract).
func Purge(ctx context.Context, st *store.Store, release string) error {
	db := st.DB()
	for _, table := range []string{"li_events", "li_event_fields", "li_nf_clauses", "asn1_types"} {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE spec_id='33.128' AND release=?`, release); err != nil {
			return err
		}
	}
	return nil
}

// InsertEvents bulk-inserts parsed ASN.1 events for one TS 33.128 release.
// INSERT OR REPLACE (not DO NOTHING): a corrected norm re-ingested on --resume
// must overwrite the stale row keyed by (spec_id, release, interface, event_name)
// rather than be silently dropped (which would drift li_events from the freshly
// re-ingested clause text). Purge handles removed/renamed events.
func InsertEvents(st *store.Store, release, moduleVersion string, evs []asn1.Event) error {
	return withTx(st, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO li_events
			(spec_id, release, module_version, interface, event_name, asn1_type, asn1_tag, originating_nf, domain, spec_clause, field_count)
			VALUES ('33.128', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, e := range evs {
			if _, err := stmt.Exec(release, moduleVersion, e.Interface, e.Name, e.Type, e.Tag, e.NF, e.Domain, e.Clause, e.FieldCount); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertFields bulk-inserts event IE rows for one release.
func InsertFields(st *store.Store, release string, fs []asn1.Field) error {
	return withTx(st, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO li_event_fields
			(spec_id, release, interface, event_name, field_name, asn1_type, asn1_tag, is_optional, ordinal)
			VALUES ('33.128', ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, f := range fs {
			if _, err := stmt.Exec(release, f.Interface, f.EventName, f.FieldName, f.Type, f.Tag, f.Optional, f.Ordinal); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertNFClauses bulk-inserts NF→clause anchors for one release.
func InsertNFClauses(st *store.Store, release string, ncs []asn1.NFClause) error {
	return withTx(st, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT OR REPLACE INTO li_nf_clauses
			(spec_id, release, originating_nf, interface, spec_clause) VALUES ('33.128', ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, n := range ncs {
			if _, err := stmt.Exec(release, n.NF, n.Interface, n.Clause); err != nil {
				return err
			}
		}
		return nil
	})
}

// HasEvents reports whether the authoritative ASN.1 registry is populated for a
// release (so callers prefer it over the prose/heading fallback).
func HasEvents(ctx context.Context, st *store.Store, release string) bool {
	var n int
	q := `SELECT count(*) FROM li_events`
	args := []any{}
	if release != "" {
		q += ` WHERE release = ?`
		args = append(args, release)
	}
	_ = st.DB().QueryRowContext(ctx, q, args...).Scan(&n)
	return n > 0
}

// GetEvents returns authoritative LI events filtered by NF / release / interface
// (any empty = unfiltered), ordered by interface then tag.
func GetEvents(ctx context.Context, st *store.Store, nf, release, iface string) ([]LIEvent, error) {
	q := `SELECT release, module_version, interface, event_name, asn1_type, asn1_tag,
	             originating_nf, domain, spec_clause, field_count
	      FROM li_events WHERE 1=1`
	var args []any
	if nf != "" {
		q += ` AND upper(originating_nf) = upper(?)`
		args = append(args, nf)
	}
	if release != "" {
		q += ` AND release = ?`
		args = append(args, release)
	}
	if iface != "" {
		q += ` AND interface = ?`
		args = append(args, iface)
	}
	q += ` ORDER BY interface, asn1_tag`
	rows, err := st.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LIEvent
	for rows.Next() {
		var e LIEvent
		if err := rows.Scan(&e.Release, &e.ModuleVersion, &e.Interface, &e.EventName, &e.ASN1Type,
			&e.ASN1Tag, &e.NF, &e.Domain, &e.Clause, &e.FieldCount); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
