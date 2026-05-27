package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// InsertAPIOperations bulk-inserts parsed 5GC OpenAPI operations (axis #2).
// op_id is assigned by the caller (ingest loop), mirroring clauses.chunk_id.
func (s *Store) InsertAPIOperations(ops []model.APIOperation) error {
	if len(ops) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO api_operations
			 (op_id, spec_id, release, version, api_doc_version, service, service_family,
			  api_root, path, method, operation_id, summary, tags, request_schema,
			  response_codes, yaml_file, forge_sha, forge_url)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			         string_split(?, '` + arraySep + `'), ?,
			         string_split(?, '` + arraySep + `'), ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, o := range ops {
			if _, err := stmt.Exec(o.OpID, o.SpecID, o.Release, o.Version, o.APIDocVersion,
				o.Service, o.ServiceFamily, o.APIRoot, o.Path, o.Method, o.OperationID,
				o.Summary, strings.Join(o.Tags, arraySep), o.RequestSchema,
				strings.Join(o.ResponseCodes, arraySep), o.YAMLFile, o.ForgeSHA, o.ForgeURL); err != nil {
				return err
			}
		}
		return nil
	})
}

// InsertAPISchemas bulk-inserts parsed 5GC OpenAPI schemas (axis #2).
func (s *Store) InsertAPISchemas(schemas []model.APISchema) error {
	if len(schemas) == 0 {
		return nil
	}
	return s.tx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(
			`INSERT OR REPLACE INTO api_schemas
			 (schema_id, spec_id, release, version, service, schema_name, kind, description,
			  properties, enum_values, refs_out, yaml_file, forge_sha, forge_url)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?,
			         string_split(?, '` + arraySep + `'),
			         string_split(?, '` + arraySep + `'),
			         string_split(?, '` + arraySep + `'), ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, sc := range schemas {
			if _, err := stmt.Exec(sc.SchemaID, sc.SpecID, sc.Release, sc.Version, sc.Service,
				sc.SchemaName, sc.Kind, sc.Description, strings.Join(sc.Properties, arraySep),
				strings.Join(sc.EnumValues, arraySep), strings.Join(sc.RefsOut, arraySep),
				sc.YAMLFile, sc.ForgeSHA, sc.ForgeURL); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearAPI truncates only the API tables — used by the OpenAPI ingest, which is
// additive to the HTML snapshot and must not touch clauses/changes/etc.
func (s *Store) ClearAPI(ctx context.Context) error {
	for _, t := range []string{"api_operations", "api_schemas"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			return err
		}
	}
	return nil
}

// HasAPI reports whether any API rows exist (optionally scoped to a release).
func (s *Store) HasAPI(ctx context.Context, release string) bool {
	q := `SELECT count(*) FROM api_operations`
	var args []any
	if release != "" {
		q += ` WHERE release = ?`
		args = append(args, release)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// SpecsWithAPI returns the set of spec_ids that have any API rows — lets
// list_specs flag 29.5xx specs with has_api=true (§5.2).
func (s *Store) SpecsWithAPI(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT spec_id FROM api_operations`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// APIHit is a scored API operation or schema returned by SearchAPI.
type APIHit struct {
	Kind  string              `json:"kind"` // "operation" | "schema"
	Score float64             `json:"score"`
	Op    *model.APIOperation `json:"operation,omitempty"`
	Sch   *model.APISchema    `json:"schema,omitempty"`
}

// APISearchQuery parameterises SearchAPI.
type APISearchQuery struct {
	Text          string
	Release       string
	SpecID        string
	Service       string
	ServiceFamily string
	Method        string
	Kind          string // "operation" | "schema" | "any"
	TopK          int
}

// SearchAPI does a lexical search over operations and/or schemas, mirroring the
// LIKE-tokenised scoring of SearchClauses (FTS is clause-only for now).
func (s *Store) SearchAPI(ctx context.Context, q APISearchQuery) ([]APIHit, error) {
	if q.TopK <= 0 {
		q.TopK = 10
	}
	if q.Kind == "" {
		q.Kind = "any"
	}
	var hits []APIHit
	if q.Kind == "operation" || q.Kind == "any" {
		ops, err := s.searchAPIOperations(ctx, q)
		if err != nil {
			return nil, err
		}
		hits = append(hits, ops...)
	}
	if q.Kind == "schema" || q.Kind == "any" {
		sch, err := s.searchAPISchemas(ctx, q)
		if err != nil {
			return nil, err
		}
		hits = append(hits, sch...)
	}
	sortAPIHits(hits)
	if len(hits) > q.TopK {
		hits = hits[:q.TopK]
	}
	return hits, nil
}

func (s *Store) searchAPIOperations(ctx context.Context, q APISearchQuery) ([]APIHit, error) {
	toks := likeTokens(q.Text)
	if len(toks) == 0 {
		return nil, nil
	}
	var score, where strings.Builder
	var args []any
	for i, tk := range toks {
		like := "%" + tk + "%"
		if i > 0 {
			score.WriteString(" + ")
			where.WriteString(" OR ")
		}
		// operation_id + path weighted above summary/tags/service.
		score.WriteString(`(CASE WHEN lower(operation_id) LIKE ? THEN 3.0 ELSE 0 END + ` +
			`CASE WHEN lower(path) LIKE ? THEN 2.0 ELSE 0 END + ` +
			`CASE WHEN lower(summary) LIKE ? THEN 1.0 ELSE 0 END + ` +
			`CASE WHEN lower(service) LIKE ? THEN 1.0 ELSE 0 END)`)
		where.WriteString(`lower(operation_id) LIKE ? OR lower(path) LIKE ? OR lower(summary) LIKE ? OR lower(service) LIKE ?`)
		args = append(args, like, like, like, like)
	}
	whereArgs := append([]any(nil), args...) // score args then where args
	sql := `SELECT op_id, spec_id, release, version, api_doc_version, service, service_family,
	               api_root, path, method, operation_id, summary,
	               array_to_string(tags, '` + arraySep + `'),
	               request_schema, array_to_string(response_codes, '` + arraySep + `'),
	               yaml_file, forge_sha, forge_url, (` + score.String() + `) AS score
	        FROM api_operations WHERE (` + where.String() + `)`
	allArgs := append(args, whereArgs...)
	allArgs, sql = appendAPIFilters(allArgs, sql, q, true)
	sql += ` ORDER BY score DESC, spec_id, path LIMIT ?`
	allArgs = append(allArgs, q.TopK)

	rows, err := s.db.QueryContext(ctx, sql, allArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []APIHit
	for rows.Next() {
		var o model.APIOperation
		var tags, codes string
		var score float64
		if err := rows.Scan(&o.OpID, &o.SpecID, &o.Release, &o.Version, &o.APIDocVersion,
			&o.Service, &o.ServiceFamily, &o.APIRoot, &o.Path, &o.Method, &o.OperationID,
			&o.Summary, &tags, &o.RequestSchema, &codes, &o.YAMLFile, &o.ForgeSHA, &o.ForgeURL,
			&score); err != nil {
			return nil, err
		}
		o.Tags = splitArr(tags)
		o.ResponseCodes = splitArr(codes)
		op := o
		out = append(out, APIHit{Kind: "operation", Score: score, Op: &op})
	}
	return out, rows.Err()
}

func (s *Store) searchAPISchemas(ctx context.Context, q APISearchQuery) ([]APIHit, error) {
	toks := likeTokens(q.Text)
	if len(toks) == 0 {
		return nil, nil
	}
	var score, where strings.Builder
	var args []any
	for i, tk := range toks {
		like := "%" + tk + "%"
		if i > 0 {
			score.WriteString(" + ")
			where.WriteString(" OR ")
		}
		score.WriteString(`(CASE WHEN lower(schema_name) LIKE ? THEN 3.0 ELSE 0 END + ` +
			`CASE WHEN lower(description) LIKE ? THEN 1.0 ELSE 0 END)`)
		where.WriteString(`lower(schema_name) LIKE ? OR lower(description) LIKE ?`)
		args = append(args, like, like)
	}
	whereArgs := append([]any(nil), args...)
	sql := `SELECT schema_id, spec_id, release, version, service, schema_name, kind, description,
	               array_to_string(properties, '` + arraySep + `'),
	               array_to_string(enum_values, '` + arraySep + `'),
	               array_to_string(refs_out, '` + arraySep + `'),
	               yaml_file, forge_sha, forge_url, (` + score.String() + `) AS score
	        FROM api_schemas WHERE (` + where.String() + `)`
	allArgs := append(args, whereArgs...)
	allArgs, sql = appendAPIFilters(allArgs, sql, q, false)
	sql += ` ORDER BY score DESC, spec_id, schema_name LIMIT ?`
	allArgs = append(allArgs, q.TopK)

	rows, err := s.db.QueryContext(ctx, sql, allArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []APIHit
	for rows.Next() {
		var sc model.APISchema
		var props, enums, refs string
		var score float64
		if err := rows.Scan(&sc.SchemaID, &sc.SpecID, &sc.Release, &sc.Version, &sc.Service,
			&sc.SchemaName, &sc.Kind, &sc.Description, &props, &enums, &refs,
			&sc.YAMLFile, &sc.ForgeSHA, &sc.ForgeURL, &score); err != nil {
			return nil, err
		}
		sc.Properties = splitArr(props)
		sc.EnumValues = splitArr(enums)
		sc.RefsOut = splitArr(refs)
		s2 := sc
		out = append(out, APIHit{Kind: "schema", Score: score, Sch: &s2})
	}
	return out, rows.Err()
}

// appendAPIFilters adds the release/spec/service/method facets shared by both
// searches. opMode toggles the operation-only method/service_family facets.
func appendAPIFilters(args []any, sql string, q APISearchQuery, opMode bool) ([]any, string) {
	if q.Release != "" {
		sql += ` AND release = ?`
		args = append(args, q.Release)
	}
	if q.SpecID != "" {
		sql += ` AND spec_id = ?`
		args = append(args, q.SpecID)
	}
	if q.Service != "" {
		sql += ` AND service = ?`
		args = append(args, q.Service)
	}
	if opMode && q.ServiceFamily != "" {
		sql += ` AND service_family = ?`
		args = append(args, q.ServiceFamily)
	}
	if opMode && q.Method != "" {
		sql += ` AND upper(method) = upper(?)`
		args = append(args, q.Method)
	}
	return args, sql
}

// GetAPIForSpec returns all API operations + schemas for a spec at a release,
// for the get_spec API enrichment (§5.2).
func (s *Store) GetAPIForSpec(ctx context.Context, specID, release string) ([]model.APIOperation, []model.APISchema, error) {
	return s.apiOpsForSpec(ctx, specID, release), s.apiSchemasForSpec(ctx, specID, release), nil
}

func (s *Store) apiOpsForSpec(ctx context.Context, specID, release string) []model.APIOperation {
	rows, err := s.db.QueryContext(ctx,
		`SELECT op_id, spec_id, release, version, api_root, path, method, operation_id, summary, forge_url
		 FROM api_operations WHERE spec_id = ? AND release = ? ORDER BY path, method`, specID, release)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []model.APIOperation
	for rows.Next() {
		var o model.APIOperation
		if rows.Scan(&o.OpID, &o.SpecID, &o.Release, &o.Version, &o.APIRoot, &o.Path,
			&o.Method, &o.OperationID, &o.Summary, &o.ForgeURL) == nil {
			out = append(out, o)
		}
	}
	return out
}

func (s *Store) apiSchemasForSpec(ctx context.Context, specID, release string) []model.APISchema {
	rows, err := s.db.QueryContext(ctx,
		`SELECT schema_id, spec_id, release, version, schema_name, kind, forge_url
		 FROM api_schemas WHERE spec_id = ? AND release = ? ORDER BY schema_name`, specID, release)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []model.APISchema
	for rows.Next() {
		var sc model.APISchema
		if rows.Scan(&sc.SchemaID, &sc.SpecID, &sc.Release, &sc.Version, &sc.SchemaName,
			&sc.Kind, &sc.ForgeURL) == nil {
			out = append(out, sc)
		}
	}
	return out
}

func splitArr(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, arraySep)
}

func sortAPIHits(h []APIHit) {
	for i := 1; i < len(h); i++ {
		for j := i; j > 0 && h[j].Score > h[j-1].Score; j-- {
			h[j], h[j-1] = h[j-1], h[j]
		}
	}
}
