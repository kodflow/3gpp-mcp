package li

import (
	"context"
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

// GetASN1Type returns a type definition by name (optionally scoped to a release;
// newest indexed release wins when release is empty). ok=false if absent.
func GetASN1Type(ctx context.Context, st store.Reader, name, release string) (ASN1Type, bool) {
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
	if err := st.QueryRowContext(ctx, q, args...).Scan(
		&t.SpecID, &t.Release, &t.TypeName, &t.Kind, &members); err != nil {
		return ASN1Type{}, false
	}
	if members != "" {
		_ = json.Unmarshal([]byte(members), &t.Members)
	}
	return t, true
}
