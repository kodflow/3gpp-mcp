package openapi

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// Stats summarises an OpenAPI ingest run.
type Stats struct {
	Releases, Files, Operations, Schemas int
}

// IngestDir loads the 5GC OpenAPI corpus under srcDir
// (data/sources/5g-apis/<Rel>/<sha>/*.yaml) into the store. It is additive to
// the HTML snapshot: it clears only the api_* tables, never clauses. When
// releases is non-empty only those <Rel> dirs are loaded. The op_id/schema_id
// counters are assigned over a sorted file walk, so the same corpus yields the
// same ids (deterministic, CLAUDE.md §1).
func IngestDir(ctx context.Context, db *store.Store, srcDir string, releases []string, logf func(string, ...any)) (Stats, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var st Stats
	if err := db.ClearAPI(ctx); err != nil {
		return st, err
	}

	relDirs, err := filepath.Glob(filepath.Join(srcDir, "Rel-*"))
	if err != nil {
		return st, err
	}
	sort.Strings(relDirs)
	want := set(releases)

	var opID, schID uint64
	for _, rd := range relDirs {
		rel := filepath.Base(rd)
		if len(want) > 0 && !want[rel] {
			continue
		}
		// The single <sha> subdir pins the content (newest if several present).
		shaDirs, _ := filepath.Glob(filepath.Join(rd, "*"))
		sort.Strings(shaDirs)
		var shaDir, sha string
		for _, d := range shaDirs {
			if isDir(d) {
				shaDir, sha = d, filepath.Base(d)
			}
		}
		if shaDir == "" {
			continue
		}

		files, _ := filepath.Glob(filepath.Join(shaDir, "*.yaml"))
		sort.Strings(files)
		if len(files) == 0 {
			continue
		}
		st.Releases++

		var ops []model.APIOperation
		var schemas []model.APISchema
		versions := map[string]string{} // specID -> version (for spec_versions upsert)
		for _, f := range files {
			res, err := ParseFile(f, rel, sha)
			if err != nil {
				logf("skip %s: %v", filepath.Base(f), err)
				continue
			}
			st.Files++
			for i := range res.Operations {
				opID++
				res.Operations[i].OpID = opID
			}
			for i := range res.Schemas {
				schID++
				res.Schemas[i].SchemaID = schID
			}
			ops = append(ops, res.Operations...)
			schemas = append(schemas, res.Schemas...)
			if res.Version != "" {
				versions[res.SpecID] = res.Version
			}
		}
		if err := db.InsertAPIOperations(ops); err != nil {
			return st, err
		}
		if err := db.InsertAPISchemas(schemas); err != nil {
			return st, err
		}
		// Make sure each API spec/version exists in spec_versions so the API
		// rows link to a real version row (idempotent upsert).
		for specID, ver := range versions {
			_ = db.UpsertSpec(model.Spec{SpecID: specID, Series: model.SeriesOf(specID), DocType: "TS"})
			_ = db.UpsertVersion(model.SpecVersion{SpecID: specID, Release: rel, Version: ver})
		}
		_ = db.SetMeta("5g_apis_"+rel+"_sha", sha)
		st.Operations += len(ops)
		st.Schemas += len(schemas)
		logf("%s (%s): %d files, %d operations, %d schemas", rel, short(sha), len(files), len(ops), len(schemas))
	}
	return st, nil
}

func set(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[strings.TrimSpace(x)] = true
	}
	return m
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
