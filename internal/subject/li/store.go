package li

import (
	"context"

	"github.com/kodflow/3gpp-mcp/internal/store"
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

// HasEvents reports whether the authoritative ASN.1 registry is populated for a
// release (so callers prefer it over the prose/heading fallback).
func HasEvents(ctx context.Context, st store.Reader, release string) bool {
	var n int
	q := `SELECT count(*) FROM li_events`
	args := []any{}
	if release != "" {
		q += ` WHERE release = ?`
		args = append(args, release)
	}
	_ = st.QueryRowContext(ctx, q, args...).Scan(&n)
	return n > 0
}

// GetEvents returns authoritative LI events filtered by NF / release / interface
// (any empty = unfiltered), ordered by interface then tag.
func GetEvents(ctx context.Context, st store.Reader, nf, release, iface string) ([]LIEvent, error) {
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
	rows, err := st.QueryContext(ctx, q, args...)
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
