// Command derive-anchor writes the 3GPP delta anchor of a corpus FROM that
// corpus: "spec|release -> highest version" over its spec_versions table, in the
// exact bytes the fold (rust/store merge --index-out) writes.
//
//	derive-anchor --db data/3gpp.duckdb --out .local/corpus-index.json
//
// It is how `seed` gives a fresh clone an anchor of the same generation as the
// snapshot it just pulled, and how `ingest` restores a missing anchor without a
// fold. See internal/anchor for why the anchor is derived rather than downloaded.
//
// Read-only on the corpus. The output is written atomically (tmp + rename).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodflow/3gpp-mcp/internal/anchor"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	db := flag.String("db", "", "corpus DuckDB to derive the anchor from (required)")
	out := flag.String("out", "", "anchor file to write (required)")
	flag.Parse()
	if *db == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "derive-anchor: --db and --out are required")
		os.Exit(2)
	}
	n, err := run(context.Background(), *db, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "derive-anchor:", err)
		os.Exit(1)
	}
	fmt.Printf("anchor_keys=%d\n", n)
}

func run(ctx context.Context, dbPath, out string) (int, error) {
	ix, err := derive(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	// An empty anchor is not an anchor: discover would read it as "nothing is
	// indexed" and ask for the whole archive. A corpus with no spec_versions is
	// not something to describe, it is something to report.
	if len(ix) == 0 {
		return 0, fmt.Errorf("%s has no spec_versions rows — refusing to write an empty anchor", dbPath)
	}
	b, err := ix.Marshal()
	if err != nil {
		return 0, err
	}
	if err := writeDurable(out, b); err != nil {
		return 0, err
	}
	return len(ix), nil
}

// writeDurable publishes b at path the way goal.WriteAtomic does: a uniquely
// named temporary in the same directory, flushed to disk BEFORE the rename. A
// rename of unflushed data can survive a power cut as an empty file, and an empty
// anchor is read by discover as "nothing is indexed".
func writeDurable(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o644)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// derive reads spec_versions, the table the fold derives its anchor from.
func derive(ctx context.Context, dbPath string) (anchor.Index, error) {
	s, err := store.OpenReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	rows, err := s.QueryContext(ctx, `SELECT spec_id, release, version FROM spec_versions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ix := anchor.Index{}
	for rows.Next() {
		var spec, rel, ver string
		if err := rows.Scan(&spec, &rel, &ver); err != nil {
			return nil, err
		}
		ix.Add(spec, rel, ver)
	}
	return ix, rows.Err()
}
