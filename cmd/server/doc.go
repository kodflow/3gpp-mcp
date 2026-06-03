package main

// doc.go adds the HTTP "documentation" page for a single cited clause. In HTTP
// serve mode a citation like [TS 23.502 §4.2.2.2.2] becomes a clickable link to
// /spec/23.502/Rel-17/4.2.2.2.2 that opens the EXACT indexed clause text
// (verbatim, no transformation) plus a link to the official 3GPP source.
//
// It is a pure reader: spec_id/release/clause come straight from the URL and are
// fed to the parameterised store methods (GetClauses / ListReleases) — never
// concatenated into SQL — so path-traversal or odd input is harmless. Pure
// stdlib (net/http + html/template auto-escaping); zero new deps, zero MCP tools.

import (
	"context"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// specDocData is the view model for the clause documentation page. Every field
// is plain text; html/template escapes it on render (the verbatim Text lives in
// a <pre> and is auto-escaped — no manual sanitisation needed).
type specDocData struct {
	SpecID      string
	Release     string
	Version     string
	Clause      string
	Heading     string
	Text        string // VERBATIM indexed clause text
	OfficialURL string // docx_url from spec_versions, else the 3GPP archive URL
	PrevClause  string // sibling clause_path for navigation (empty if none)
	NextClause  string
	Host        string
}

// specDocHandler serves GET /spec/{spec_id}/{release}/{clause} and the
// query-string fallback /spec?spec_id=..&release=..&clause=.. . A missing
// clause yields a clean 404; it never panics and never mutates the store.
func specDocHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		specID, release, clause := parseSpecPath(r)
		if specID == "" || clause == "" {
			http.Error(w, "missing spec_id or clause; use /spec/{spec_id}/{release}/{clause}", http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		// Resolve the version exactly as get_spec does: a release pins the highest
		// version within it; an empty/unknown release falls back to the latest.
		version := ""
		if v, ok, _ := st.VersionForRelease(ctx, specID, release); ok {
			version = v
		} else if rel, v, ok, _ := st.LatestVersion(ctx, specID); ok {
			version, release = v, rel
		}

		// Parameterised fetch — clause is an EXACT match here (no LIKE subtree),
		// so the page shows the single cited clause verbatim.
		clauses, err := st.GetClauses(ctx, specID, version, clause)
		if err != nil {
			http.Error(w, "lookup failed", http.StatusInternalServerError)
			return
		}
		var found *model.Clause
		for i := range clauses {
			if clauses[i].ClausePath == clause {
				found = &clauses[i]
				break
			}
		}
		if found == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_ = specNotFoundTmpl.Execute(w, specDocData{
				SpecID: specID, Release: release, Version: version, Clause: clause,
			})
			return
		}

		data := specDocData{
			SpecID:      found.SpecID,
			Release:     found.Release,
			Version:     found.Version,
			Clause:      found.ClausePath,
			Heading:     found.Heading,
			Text:        found.Text,
			OfficialURL: officialURL(ctx, st, *found),
			Host:        hostOr(r.Host),
		}
		data.PrevClause, data.NextClause = siblingClauses(ctx, st, found.SpecID, found.Version, found.ClausePath)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := specDocTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// parseSpecPath extracts (spec_id, release, clause) from either the path form
// /spec/{spec_id}/{release}/{clause} or the query-string fallback. Trailing
// slashes and empty segments are tolerated; nothing here trusts the input
// beyond passing it to parameterised queries.
func parseSpecPath(r *http.Request) (specID, release, clause string) {
	rest := strings.TrimPrefix(r.URL.Path, "/spec")
	rest = strings.Trim(rest, "/")
	if rest != "" {
		parts := strings.SplitN(rest, "/", 3)
		switch len(parts) {
		case 1:
			specID = parts[0]
		case 2:
			specID, release = parts[0], parts[1]
		default:
			specID, release, clause = parts[0], parts[1], parts[2]
		}
	}
	// Query-string fallback fills any blank the path didn't supply.
	q := r.URL.Query()
	if specID == "" {
		specID = q.Get("spec_id")
	}
	if release == "" {
		release = q.Get("release")
	}
	if clause == "" {
		clause = q.Get("clause")
	}
	return strings.TrimSpace(specID), strings.TrimSpace(release), strings.TrimSpace(clause)
}

// officialURL prefers the spec_versions.docx_url recorded at ingest; when none
// is stored (e.g. a minimal snapshot) it falls back to the deterministic 3GPP
// archive URL the citation already carries. Either way the link points at the
// authoritative 3GPP document, never at our local copy.
func officialURL(ctx context.Context, st *store.Store, c model.Clause) string {
	if vs, err := st.ListReleases(ctx, c.SpecID); err == nil {
		for _, v := range vs {
			if v.Version == c.Version && v.DocxURL != "" {
				return v.DocxURL
			}
		}
	}
	return c.Cite().URL
}

// siblingClauses returns the immediately preceding/following clause_path within
// the same spec+version, ordered the same way the store orders clauses. It is a
// cheap single scan of the already-loaded version; on any error it returns
// empties (navigation is best-effort, never load-bearing).
func siblingClauses(ctx context.Context, st *store.Store, specID, version, clause string) (prev, next string) {
	all, err := st.GetClauses(ctx, specID, version, "")
	if err != nil {
		return "", ""
	}
	paths := make([]string, 0, len(all))
	for _, c := range all {
		paths = append(paths, c.ClausePath)
	}
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) < len(paths[j])
		}
		return paths[i] < paths[j]
	})
	for i, p := range paths {
		if p == clause {
			if i > 0 {
				prev = paths[i-1]
			}
			if i+1 < len(paths) {
				next = paths[i+1]
			}
			return prev, next
		}
	}
	return "", ""
}

// specDocTmpl renders the clause documentation page. The verbatim Text sits in a
// <pre>; html/template auto-escapes it so spec content can never inject markup.
var specDocTmpl = template.Must(template.New("specdoc").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.SpecID}} {{.Release}} §{{.Clause}} — 3gpp-mcp</title>
<style>
 body{font:15px/1.6 system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;color:#111}
 .cite{background:#0d1117;color:#e6edf3;padding:.8rem 1rem;border-radius:8px;font:13px ui-monospace,monospace}
 .cite b{color:#7ee787}
 h1{margin:.6rem 0 .2rem;font-size:1.4rem} .heading{color:#444;margin-top:0}
 pre{background:#f6f8fa;border:1px solid #d0d7de;padding:1rem;border-radius:8px;
   white-space:pre-wrap;word-wrap:break-word;overflow:auto}
 a{color:#0969da} .src{display:inline-block;margin:1rem 0;font-weight:600}
 nav{margin-top:1.5rem;border-top:1px solid #d0d7de;padding-top:.8rem;font-size:14px}
 .verbatim-note{color:#666;font-size:12px;margin:.4rem 0 0}
</style></head><body>
<div class="cite">
 spec_id <b>{{.SpecID}}</b> · release <b>{{.Release}}</b> · version <b>{{.Version}}</b> · clause <b>§{{.Clause}}</b>
</div>
<h1>{{.SpecID}} §{{.Clause}}</h1>
{{if .Heading}}<p class="heading">{{.Heading}}</p>{{end}}
{{if .OfficialURL}}<a class="src" href="{{.OfficialURL}}" rel="noopener noreferrer">Source officielle 3GPP &rarr;</a>{{end}}
<pre>{{.Text}}</pre>
<p class="verbatim-note">Texte indexe verbatim — aucune reformulation. Reasoning by the client; the index returns the exact fragment.</p>
<nav>
 {{if .PrevClause}}<a href="/spec/{{.SpecID}}/{{.Release}}/{{.PrevClause}}">&larr; §{{.PrevClause}}</a>{{end}}
 {{if and .PrevClause .NextClause}} &nbsp;·&nbsp; {{end}}
 {{if .NextClause}}<a href="/spec/{{.SpecID}}/{{.Release}}/{{.NextClause}}">§{{.NextClause}} &rarr;</a>{{end}}
</nav>
</body></html>`))

// specNotFoundTmpl renders a clean 404 (no panic, no stack) when the requested
// (spec_id, release, clause) is not in the corpus.
var specNotFoundTmpl = template.Must(template.New("specnf").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Not found — 3gpp-mcp</title>
<style>body{font:15px/1.6 system-ui,sans-serif;max-width:700px;margin:3rem auto;padding:0 1rem;color:#111}
code{background:#f0f0f0;padding:.1rem .3rem;border-radius:4px}</style></head><body>
<h1>404 — clause not in corpus</h1>
<p>No clause <code>§{{.Clause}}</code> for <code>{{.SpecID}}</code> in release
<code>{{if .Release}}{{.Release}}{{else}}(latest){{end}}</code>.</p>
<p>The exact spec/clause may not be indexed in this snapshot. Try the official
3GPP archive, or query the MCP <code>search_spec</code> / <code>get_spec</code> tools.</p>
</body></html>`))
