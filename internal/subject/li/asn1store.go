package li

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject/li/asn1"
)

// ASN1Type is a stored ASN.1 type definition (axis #8).
type ASN1Type struct {
	SpecID   string        `json:"spec_id"`
	Release  string        `json:"release"`
	TypeName string        `json:"type_name"`
	Kind     string        `json:"kind"`
	Members  []asn1.Member `json:"members,omitempty"`
}

// InsertASN1Types bulk-inserts the parsed ASN.1 type catalogue for a release.
// Members are stored as JSON to preserve recursive SEQUENCE/CHOICE structure.
func InsertASN1Types(st *store.Store, specID, release string, types []asn1.TypeDef) error {
	if len(types) == 0 {
		return nil
	}
	return withTx(st, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO asn1_types (spec_id, release, type_name, kind, members)
			 VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, t := range types {
			var members string
			if len(t.Members) > 0 {
				if b, e := json.Marshal(t.Members); e == nil {
					members = string(b)
				}
			}
			if _, err := stmt.Exec(specID, release, t.Name, t.Kind, members); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetASN1Type returns a type definition by name (optionally scoped to a release;
// newest indexed release wins when release is empty). ok=false if absent.
func GetASN1Type(ctx context.Context, st *store.Store, name, release string) (ASN1Type, bool) {
	q := `SELECT spec_id, release, type_name, kind, COALESCE(members,'')
	      FROM asn1_types WHERE type_name = ?`
	args := []any{name}
	if release != "" {
		q += ` AND release = ?`
		args = append(args, release)
	}
	q += ` ORDER BY release DESC LIMIT 1`
	var t ASN1Type
	var members string
	if err := st.DB().QueryRowContext(ctx, q, args...).Scan(
		&t.SpecID, &t.Release, &t.TypeName, &t.Kind, &members); err != nil {
		return ASN1Type{}, false
	}
	if members != "" {
		_ = json.Unmarshal([]byte(members), &t.Members)
	}
	return t, true
}
