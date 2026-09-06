package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DuckDB's buffer manager defaults to ~80% of physical RAM, and that default is
// what killed two builds of this corpus.
//
// Measured 2026-09-06 on a 28 GB machine, watching resident set size per process:
//
//	28.0 GB x 0.8              = 22.4 GB   the default cap
//	embed-io --import-vectors    22.3 GB   ledger staged as a TEMP TABLE
//	migrate-paragraphs           22.0 GB   during --restore
//
// Both within a tenth of a gigabyte of the default. Neither process was leaking
// and neither ever felt pressure: they were spending exactly what they had been
// allowed. The MACHINE is what ran out.
//
// A cap is not a diet. Past it DuckDB spills to temp_directory instead of asking
// the operating system for more, so the work still completes — slower in the
// worst case, alive in every case. The Rust side already does this in
// copy_database_compact and freeze-hnsw; the writers that died were the ones
// left out of the practice.

// MemoryLimitEnv overrides DefaultMemoryLimit without a rebuild, for a machine
// with more or less room than this one.
const MemoryLimitEnv = "DUCKDB_MEMORY_LIMIT"

// DefaultMemoryLimit is FIXED, and deliberately not a share of physical RAM. A
// percentage-of-machine default is precisely the bug this package removes: the
// goal is to leave the operating system room, not to claim a fraction of the box.
const DefaultMemoryLimit = "12GB"

// PickMemoryLimit is the whole policy, as a pure function of its input.
//
// Pure ON PURPOSE. The obvious shape reads os.Getenv inside, and then the only
// way to test the fallback is for a test to mutate the process environment —
// which is shared by every goroutine and every parallel test, and on Unix is not
// permitted while another thread may be reading it. Keeping the decision
// separate from where the value comes from means the policy is tested with
// values and nothing has to touch the environment at all.
//
// A blank value must NOT be forwarded: `SET memory_limit = ”` is a DuckDB parse
// error, so an empty or whitespace-only override would kill the run instead of
// being ignored.
func PickMemoryLimit(raw string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return DefaultMemoryLimit
}

// SpillDir puts DuckDB's temporary files beside the corpus they belong to.
//
// That volume must have room for the result anyway, whereas the process working
// directory is wherever `make` happened to be invoked from — which on this
// project is not always the same disk.
func SpillDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "duckdb-spill.tmp")
}

// BoundMemory caps the buffer manager for this connection and gives it somewhere
// to spill. Call it once, right after opening a corpus for writing.
func BoundMemory(h *sql.DB, dbPath string) error {
	return boundMemoryTo(h, dbPath, PickMemoryLimit(os.Getenv(MemoryLimitEnv)))
}

// boundMemoryTo is BoundMemory with the limit supplied rather than read, so the
// SQL can be exercised against a real DuckDB without an environment variable.
func boundMemoryTo(h *sql.DB, dbPath, limit string) error {
	q := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	if _, err := h.Exec(fmt.Sprintf("SET temp_directory = '%s'", q(SpillDir(dbPath)))); err != nil {
		return fmt.Errorf("set temp_directory: %w", err)
	}
	if _, err := h.Exec(fmt.Sprintf("SET memory_limit = '%s'", q(limit))); err != nil {
		return fmt.Errorf("set memory_limit: %w", err)
	}
	return nil
}
