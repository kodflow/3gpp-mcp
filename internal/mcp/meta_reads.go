package mcp

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// AN IDENTITY THAT COULD NOT BE READ IS NOT AN EMPTY IDENTITY.
//
// server_info and help report each half's embedding identity from schema_meta,
// and they read it through Store.GetMeta, which returns "" for an absent key AND
// for a failed read — it discards the error. So an ETSI half that could not be
// queried (a closed handle, a DuckDB memory limit it has run out of) was reported
// as `"embedding_model": ""` with `"embedding_model_ok": false`: a value, and a
// wrong one. It reads as "this corpus carries no vectors", which is a statement
// about the corpus, when the truth is that the server could not ask.
//
// metaReads keeps the two apart: an absent key is "" (the corpus states nothing),
// a failed read is an error, kept by key so the answer can show it — the field
// itself is then null rather than a value.
//
// Read through Reader.QueryRowContext rather than a new error-returning method on
// internal/store, for the reason countEvolutions gives: the store is in the Impl
// of the data steps (validate replays ~8 min), and the SQL is one line.
type metaReads struct {
	ctx  context.Context
	st   store.Reader
	vals map[string]string
	errs map[string]string
}

func newMetaReads(ctx context.Context, st store.Reader) *metaReads {
	return &metaReads{ctx: ctx, st: st, vals: map[string]string{}, errs: map[string]string{}}
}

// get reads one key, once per answer: a key asked for twice (a reason, then the
// field) gives the same result both times. ok is false when the read FAILED —
// not when the key is absent, which is "" and ok.
func (m *metaReads) get(key string) (string, bool) {
	if v, ok := m.vals[key]; ok {
		return v, true
	}
	if _, failed := m.errs[key]; failed {
		return "", false
	}
	var v sql.NullString
	err := m.st.QueryRowContext(m.ctx, `SELECT value FROM schema_meta WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		m.vals[key] = ""
	case err != nil:
		m.errs[key] = firstLine(err.Error(), errorTextLimit)
		return "", false
	default:
		m.vals[key] = v.String
	}
	return m.vals[key], true
}

// value is get for a response field: the string, or nil when it could not be
// read — never "" standing in for an error.
func (m *metaReads) value(key string) any {
	if v, ok := m.get(key); ok {
		return v
	}
	return nil
}

// reason renders a failed key for a *_reason field.
func (m *metaReads) reason(key string) string {
	return key + "_unreadable (" + m.errs[key] + ")"
}

// report adds the failed reads to a response block, when there are any.
func (m *metaReads) report(block map[string]any) {
	if len(m.errs) > 0 {
		block["read_errors"] = m.errs
	}
}
