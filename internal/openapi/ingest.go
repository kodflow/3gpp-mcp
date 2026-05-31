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
// the HTML snapshot: it touches only the api_* tables, never clauses. When
// releases is non-empty only those <Rel> dirs are loaded. The op_id/schema_id
// counters are assigned over a sorted file walk, so the same corpus yields the
// same ids (deterministic, CLAUDE.md §1).
//
// PR-7 (openapi-clearapi-full-recompute-and-loss): the source is walked and
// VALIDATED before any rows are deleted, and the delete is SCOPED to exactly the
// releases that actually carry YAML this run — never a blanket ClearAPI. So a
// degraded/empty fetch (Forge outage; the workflow tolerates a failed fetch)
// leaves the inherited api_* rows UNTOUCHED instead of wiping the whole API
// channel, and re-ingesting only Rel-19 never drops Rel-17/Rel-18 rows.
func IngestDir(ctx context.Context, db *store.Store, srcDir string, releases []string, logf func(string, ...any)) (Stats, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var st Stats

	relDirs, err := filepath.Glob(filepath.Join(srcDir, "Rel-*"))
	if err != nil {
		return st, err
	}
	sort.Strings(relDirs)
	want := set(releases)

	// SOURCE VALIDATION (before any delete): determine which releases actually have
	// at least one .yaml under a <sha> subdir. Only those are deleted-then-replaced;
	// a degraded fetch that produced no files for a release leaves that release's
	// inherited rows in place. An empty result means "nothing to ingest" → we return
	// without deleting anything, so the prior api_* rows survive the degraded run.
	ingestable := map[string]bool{}
	for _, rd := range relDirs {
		rel := filepath.Base(rd)
		if len(want) > 0 && !want[rel] {
			continue
		}
		if _, sha := latestShaDir(rd); sha != "" {
			ingestable[rel] = true
		}
	}
	if len(ingestable) == 0 {
		logf("no ingestable OpenAPI releases under %s — preserving inherited api_* rows", srcDir)
		return st, nil
	}
	delRels := make([]string, 0, len(ingestable))
	for rel := range ingestable {
		delRels = append(delRels, rel)
	}
	sort.Strings(delRels)
	if err := db.ClearAPIReleases(ctx, delRels); err != nil {
		return st, err
	}

	var opID, schID uint64
	for _, rd := range relDirs {
		rel := filepath.Base(rd)
		if !ingestable[rel] {
			continue
		}
		shaDir, sha := latestShaDir(rd)
		if shaDir == "" {
			continue // unreachable: ingestable[rel] implies a non-empty <sha> dir
		}
		files, _ := filepath.Glob(filepath.Join(shaDir, "*.yaml"))
		sort.Strings(files)
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

// latestShaDir returns the newest (lexically last) <sha> subdir of a Rel-* dir
// that actually contains at least one .yaml, plus its sha. It returns ("","")
// when the release has no usable content — which is exactly the degraded-fetch
// signal IngestDir uses to PRESERVE that release's inherited rows rather than
// delete them. Resolving content here keeps the validation pass and the ingest
// loop in agreement on what counts as "ingestable".
func latestShaDir(relDir string) (shaDir, sha string) {
	shaDirs, _ := filepath.Glob(filepath.Join(relDir, "*"))
	sort.Strings(shaDirs)
	for _, d := range shaDirs {
		if !isDir(d) {
			continue
		}
		if files, _ := filepath.Glob(filepath.Join(d, "*.yaml")); len(files) > 0 {
			shaDir, sha = d, filepath.Base(d)
		}
	}
	return shaDir, sha
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
