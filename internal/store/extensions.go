package store

import (
	"context"
	"database/sql"
	"fmt"
)

// serveExtensions is the complete set of loadable DuckDB extensions the serve
// path depends on: fts (BM25) and vss (HNSW). LoadFTS/LoadVSS INSTALL+LOAD
// exactly these — keep the two lists in lockstep.
var serveExtensions = []string{"fts", "vss"}

// PrefetchExtensions installs and load-checks every serve-side DuckDB extension
// into this build's extension directory (~/.duckdb/extensions/<version>/<platform>).
//
// It exists for IMAGE BUILD time, where the network is available: `INSTALL`
// downloads from extensions.duckdb.org on first run and is a no-op when the
// extension is already present. Baking the extensions into the image means
// serve NEVER reaches for the network at startup — a no-egress NetworkPolicy or
// a read-only filesystem must still get BM25 + HNSW instead of silently
// degrading to LIKE + exact-scan (local-first, CLAUDE.md §1; the degradation
// observed as fts:false/hnsw:false in production).
//
// The extension ABI is DuckDB-version-specific; installing through this binary
// guarantees the baked artefacts match the statically linked DuckDB exactly.
func PrefetchExtensions(ctx context.Context) error {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return fmt.Errorf("open in-memory duckdb: %w", err)
	}
	defer func() { _ = db.Close() }()
	for _, ext := range serveExtensions {
		if _, err := db.ExecContext(ctx, "INSTALL "+ext); err != nil {
			return fmt.Errorf("install %s: %w", ext, err)
		}
		// LOAD is the proof the artefact is usable (right version/platform), not
		// merely downloaded.
		if _, err := db.ExecContext(ctx, "LOAD "+ext); err != nil {
			return fmt.Errorf("load %s: %w", ext, err)
		}
	}
	return nil
}

// InstalledExtensionPaths reports where each serve extension landed, for build
// logs and tests. Missing entries mean the extension is not installed.
func InstalledExtensionPaths(ctx context.Context) (map[string]string, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open in-memory duckdb: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx,
		`SELECT extension_name, install_path FROM duckdb_extensions()
		  WHERE installed AND extension_name IN ('fts', 'vss')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var name, path string
		if err := rows.Scan(&name, &path); err != nil {
			return nil, err
		}
		out[name] = path
	}
	return out, rows.Err()
}
