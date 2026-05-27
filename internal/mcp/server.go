// Package mcp wires the MCP surface (CLAUDE.md §5: 8 core tools, plus the
// li_events and search_api siblings) over a store.
//
// Every response carries a `citations` block; a tool refuses to answer when it
// cannot cite (CLAUDE.md §1: "Pas d'hallucination tolérée"). The server is the
// only client-facing component and stays pure-Go (no CGO beyond DuckDB).
package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/registry"
	"github.com/kodflow/3gpp-mcp/internal/releaseview"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
)

// New builds the MCP server with all 8 tools registered against st. baseline is
// the release every answer is scoped to ("Rel-17"); empty means "latest". When
// set, get_spec returns the baseline content plus an annex of what later
// releases add (so the user is always told newer releases extend the answer).
func New(st *store.Store, version, baseline string) *server.MCPServer {
	eng := search.New(st)
	scope := "latest release"
	if baseline != "" {
		scope = baseline + " (baseline norm; later-release additions surfaced as an annex)"
	}
	s := server.NewMCPServer("3gpp-mcp", version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, false), // static corpus: no subscribe/listChanged
		server.WithInstructions(
			"3GPP corpus retrieval, scoped to "+scope+". Returns spec fragments with "+
				"exact citations {spec_id, release, version, clause, url}; it never "+
				"summarises — the client reasons. Default to TS over TR. Answers are "+
				"pinned to the baseline release; get_spec also reports new_in_baseline "+
				"(vs previous release) and added_in_later_releases (annex)."),
	)
	h := &handlers{st: st, eng: eng, reg: registry.Default(), baseline: baseline}

	s.AddTool(mcp.NewTool("search_spec",
		mcp.WithDescription("Hybrid lexical retrieval over clauses, with citations."),
		mcp.WithString("query", mcp.Required(), mcp.Description("free-text query")),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("series", mcp.Description("e.g. 33")),
		mcp.WithString("spec_type", mcp.Description("TS or TR")),
		mcp.WithString("spec_id", mcp.Description("e.g. 33.128")),
		mcp.WithNumber("top_k", mcp.Description("max results per page (default 10)")),
		mcp.WithString("cursor", mcp.Description("opaque pagination cursor from a previous call's next_cursor")),
	), h.searchSpec)

	s.AddTool(mcp.NewTool("get_spec",
		mcp.WithDescription("Fetch a spec, or a clause/clause-subtree, verbatim with citations."),
		mcp.WithString("spec_id", mcp.Required(), mcp.Description("e.g. 33.128")),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("version", mcp.Description("e.g. 19.6.0; default = latest")),
		mcp.WithString("clause", mcp.Description("clause path or prefix, e.g. 6.2.2.2")),
		mcp.WithBoolean("full", mcp.Description("inline full clause text instead of snippet+resource URI (default false)")),
	), h.getSpec)

	s.AddTool(mcp.NewTool("get_changelog",
		mcp.WithDescription("Change Request records for a spec between releases."),
		mcp.WithString("spec_id", mcp.Required()),
		mcp.WithString("from_release", mcp.Description("e.g. Rel-18")),
		mcp.WithString("to_release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("clause", mcp.Description("filter by affected clause")),
	), h.getChangelog)

	s.AddTool(mcp.NewTool("list_releases",
		mcp.WithDescription("All (release, version, freeze_date) of a spec, newest first."),
		mcp.WithString("spec_id", mcp.Required()),
	), h.listReleases)

	s.AddTool(mcp.NewTool("resolve_term",
		mcp.WithDescription("Glossary lookup of an acronym/term (TS 21.905 seed)."),
		mcp.WithString("term", mcp.Required()),
		mcp.WithString("release", mcp.Description("optional release context")),
	), h.resolveTerm)

	s.AddTool(mcp.NewTool("trace_evolution",
		mcp.WithDescription("NE<->NF evolution subgraph (V2/KuzuDB; empty in V1)."),
		mcp.WithString("entity", mcp.Required()),
		mcp.WithString("from_release", mcp.Description("")),
		mcp.WithString("to_release", mcp.Description("")),
	), h.traceEvolution)

	s.AddTool(mcp.NewTool("find_cross_references",
		mcp.WithDescription("Specs referenced by a spec/clause (TS/TR mentions)."),
		mcp.WithString("spec_id", mcp.Required()),
		mcp.WithString("clause", mcp.Description("restrict to a clause/subtree")),
	), h.findCrossRefs)

	s.AddTool(mcp.NewTool("list_specs",
		mcp.WithDescription("Catalogue filter by release/series/working_group."),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("series", mcp.Description("e.g. 33")),
		mcp.WithString("working_group", mcp.Description("e.g. SA3")),
		mcp.WithString("spec_type", mcp.Description("TS or TR")),
	), h.listSpecs)

	s.AddTool(mcp.NewTool("search_api",
		mcp.WithDescription("Lexical search over 5GC OpenAPI operations and schemas parsed from the "+
			"canonical 3GPP Forge YAML (TS 29.5xx). Returns exact, SHA-pinned citations."),
		mcp.WithString("query", mcp.Required(), mcp.Description("free-text, e.g. 'create UE context'")),
		mcp.WithString("release", mcp.Description("e.g. Rel-18")),
		mcp.WithString("service", mcp.Description("e.g. Namf_Communication")),
		mcp.WithString("service_family", mcp.Description("e.g. Namf")),
		mcp.WithString("spec_id", mcp.Description("e.g. 29.518")),
		mcp.WithString("method", mcp.Description("HTTP method: GET/PUT/POST/DELETE/PATCH")),
		mcp.WithString("kind", mcp.Description("operation | schema | any (default any)")),
		mcp.WithNumber("top_k", mcp.Description("max results (default 10)")),
	), h.searchAPI)

	registerResources(s, h)

	// Domain subjects contribute their own tools (li_events, …). The core knows
	// nothing about any vertical; it just iterates the registry.
	for _, sub := range h.reg.All() {
		for _, tr := range sub.Tools(st, baseline) {
			s.AddTool(tr.Tool, tr.Handler)
		}
	}
	return s
}

type handlers struct {
	st       *store.Store
	eng      *search.Engine
	reg      *subject.Registry
	baseline string // release every answer is scoped to ("Rel-17"); "" = latest
}

// ---- response shapes ----------------------------------------------------

type hitOut struct {
	SpecID   string         `json:"spec_id"`
	Clause   string         `json:"clause"`
	Heading  string         `json:"heading"`
	Snippet  string         `json:"snippet"`
	Resource string         `json:"resource"` // 3gpp:// URI → resources/read for full body
	Score    float64        `json:"score"`
	Citation model.Citation `json:"citation"`
}

type clauseOut struct {
	ClausePath  string         `json:"clause_path"`
	Heading     string         `json:"heading"`
	Text        string         `json:"text,omitempty"`    // set only when full=true
	Snippet     string         `json:"snippet,omitempty"` // bounded; when not full
	Truncated   bool           `json:"truncated"`         // Snippet shorter than full body
	Bytes       int            `json:"bytes"`             // full body size (client budget)
	Resource    string         `json:"resource"`          // 3gpp:// URI → resources/read
	IsNormative bool           `json:"is_normative"`
	Citation    model.Citation `json:"citation"`
	Lineage     model.Lineage  `json:"lineage"`
}

// snippetLimit bounds get_spec clause bodies; the full text is behind the
// resource URI (axis #5).
const snippetLimit = 600

// ---- handlers -----------------------------------------------------------

func (h *handlers) searchSpec(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q, err := r.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	filter := store.SpecFilter{
		Release: r.GetString("release", h.baseline), Series: r.GetString("series", ""),
		DocType: r.GetString("spec_type", ""), SpecID: r.GetString("spec_id", ""),
	}
	pageSize := r.GetInt("top_k", 10)
	if pageSize <= 0 {
		pageSize = 10
	}
	qh := queryHash(q, filter.Release, filter.Series, filter.DocType, filter.SpecID)
	offset, err := resolveOffset(r.GetString("cursor", ""), qh)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Over-fetch the page window + 1 to detect "more exists" without a count.
	hits, err := h.eng.Search(ctx, search.Request{Text: q, Filter: filter, TopK: offset + pageSize + 1})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search failed", err), nil
	}
	var next string
	if len(hits) > offset+pageSize {
		next = encodeCursor(pageCursor{Offset: offset + pageSize, QHash: qh})
	}
	if offset < len(hits) {
		end := offset + pageSize
		if end > len(hits) {
			end = len(hits)
		}
		hits = hits[offset:end]
	} else {
		hits = nil
	}
	out := make([]hitOut, 0, len(hits))
	cites := make([]model.Citation, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hitOut{
			SpecID: hit.Clause.SpecID, Clause: hit.Clause.ClausePath,
			Heading: hit.Clause.Heading, Snippet: truncate(hit.Clause.Text, 400),
			Resource: build3GPPURI(hit.Citation),
			Score:    hit.Score, Citation: hit.Citation,
		})
		cites = append(cites, hit.Citation)
	}
	resp := map[string]any{
		"query": q, "intent": string(search.Classify(q)),
		"count": len(out), "hits": out, "citations": cites,
	}
	if next != "" {
		resp["next_cursor"] = next
	}
	return jsonResult(resp)
}

func (h *handlers) getSpec(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	clause := r.GetString("clause", "")
	release := r.GetString("release", h.baseline) // default: the baseline norm
	version := r.GetString("version", "")
	if version == "" {
		if v, ok, _ := h.st.VersionForRelease(ctx, specID, release); ok {
			version = v
		} else if _, v, ok, _ := h.st.LatestVersion(ctx, specID); ok {
			version, release = v, ""
		}
	}
	clauses, err := h.st.GetClauses(ctx, specID, version, clause)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_spec failed", err), nil
	}
	if len(clauses) == 0 {
		return mcp.NewToolResultError("no such spec/clause in corpus: " + specID), nil
	}
	// Release lineage per clause (present_in / introduced / last_seen / obsolete)
	// so every fragment says where it lives across releases and whether it's gone.
	full := r.GetBool("full", false)
	lineage, _ := h.st.ClauseLineage(ctx, specID, clause)
	out := make([]clauseOut, 0, len(clauses))
	cites := make([]model.Citation, 0, len(clauses))
	obsolete := 0
	for _, c := range clauses {
		lin := lineage[c.ClausePath]
		if lin.Obsolete {
			obsolete++
		}
		cite := c.Cite()
		co := clauseOut{
			ClausePath: c.ClausePath, Heading: c.Heading, Bytes: len(c.Text),
			Resource: build3GPPURI(cite), IsNormative: c.IsNormative,
			Citation: cite, Lineage: lin,
		}
		if full {
			co.Text = c.Text
		} else {
			co.Snippet = truncate(c.Text, snippetLimit)
			co.Truncated = len(strings.TrimSpace(c.Text)) > snippetLimit
		}
		out = append(out, co)
		cites = append(cites, cite)
	}
	resp := map[string]any{
		"spec_id": specID, "release": release, "version": version,
		"count": len(out), "clauses": out, "citations": cites,
		"obsolete_count": obsolete,
	}
	if !full {
		resp["resource_hint"] = "Full clause text is omitted when truncated=true. " +
			"Call resources/read on a clause's `resource` URI (3gpp://…) to fetch it, or pass full=true."
	}
	// Release annex: what the baseline introduced vs the previous release, and
	// what LATER releases add on top (surfaced so the user knows it exists).
	if release != "" {
		if avail, ordered, aerr := h.st.ClauseAvailability(ctx, specID, clause); aerr == nil {
			rv := releaseview.BuildReleaseView(specID, avail, ordered, release)
			resp["note"] = rv.Note
			resp["new_in_baseline"] = rv.NewInBaseline
			resp["added_in_later_releases"] = rv.AddedLater
			resp["removed_before_baseline"] = rv.RemovedBefore
		}
	}
	return jsonResult(resp)
}

func (h *handlers) getChangelog(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	changes, err := h.st.GetChangelog(ctx, specID, r.GetString("from_release", ""), r.GetString("to_release", ""))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_changelog failed", err), nil
	}
	clause := r.GetString("clause", "")
	if clause != "" {
		filtered := changes[:0]
		for _, c := range changes {
			for _, cl := range c.Clauses {
				if strings.HasPrefix(cl, clause) {
					filtered = append(filtered, c)
					break
				}
			}
		}
		changes = filtered
	}
	return jsonResult(map[string]any{"spec_id": specID, "count": len(changes), "changes": changes})
}

func (h *handlers) listReleases(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	vs, err := h.st.ListReleases(ctx, specID)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list_releases failed", err), nil
	}
	return jsonResult(map[string]any{"spec_id": specID, "count": len(vs), "versions": vs})
}

func (h *handlers) resolveTerm(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	term, err := r.RequireString("term")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	a, err := h.st.ResolveTerm(ctx, term)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve_term failed", err), nil
	}
	resp := map[string]any{"term": term, "count": len(a), "matches": a}
	// Domain subjects may enrich the term (e.g. the LI subject attaches an ASN.1
	// type definition + citation when the term names a type). Core stays generic.
	for _, te := range h.reg.TermEnrichers() {
		if extra, ok := te.EnrichTerm(ctx, h.st, term, r.GetString("release", h.baseline)); ok {
			for k, v := range extra {
				resp[k] = v
			}
		}
	}
	return jsonResult(resp)
}

func (h *handlers) traceEvolution(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entity, err := r.RequireString("entity")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	evos, err := h.st.GetEvolutions(ctx, entity)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("trace_evolution failed", err), nil
	}
	cites := make([]model.Citation, 0, len(evos))
	for _, e := range evos {
		cites = append(cites, model.Citation{
			SpecID: e.JustificationSpec, Clause: e.JustificationClause,
			URL: "https://www.3gpp.org/ftp/Specs/archive/" + model.SeriesOf(e.JustificationSpec) + "_series/" + e.JustificationSpec + "/",
		})
	}
	return jsonResult(map[string]any{
		"entity":     entity,
		"count":      len(evos),
		"evolutions": evos,
		"citations":  cites,
		"note":       "Curated NE↔NF seed (V1 relational). Full corpus-mined graph (KuzuDB) is V2.",
	})
}

var reSpecRef = regexp.MustCompile(`\bT[SR]\s?(\d\d\.\d{3})\b`)

func (h *handlers) findCrossRefs(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	version := ""
	if _, v, ok, _ := h.st.LatestVersion(ctx, specID); ok {
		version = v
	}
	clauses, err := h.st.GetClauses(ctx, specID, version, r.GetString("clause", ""))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("find_cross_references failed", err), nil
	}
	seen := map[string]bool{}
	var refs []string
	for _, c := range clauses {
		for _, m := range reSpecRef.FindAllStringSubmatch(c.Heading+" "+c.Text, -1) {
			id := m[1]
			if id != specID && !seen[id] {
				seen[id] = true
				refs = append(refs, id)
			}
		}
	}
	return jsonResult(map[string]any{
		"spec_id": specID, "version": version, "count": len(refs), "references": refs,
		"citation": model.Citation{SpecID: specID, Version: version, URL: model.ArchiveURL(specID, version)},
	})
}

func (h *handlers) listSpecs(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specs, err := h.st.ListSpecs(ctx, store.SpecFilter{
		Release: r.GetString("release", h.baseline), Series: r.GetString("series", ""),
		WorkingGroup: r.GetString("working_group", ""), DocType: r.GetString("spec_type", ""),
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list_specs failed", err), nil
	}
	// Flag specs that also have machine-readable OpenAPI rows (axis #2) so a
	// client knows it can call search_api for them.
	apiSpecs := h.st.SpecsWithAPI(ctx)
	type specRow struct {
		model.Spec
		HasAPI bool `json:"has_api"`
	}
	rows := make([]specRow, len(specs))
	for i, sp := range specs {
		rows[i] = specRow{Spec: sp, HasAPI: apiSpecs[sp.SpecID]}
	}
	return jsonResult(map[string]any{"count": len(rows), "specs": rows})
}

// searchAPI answers from the 5GC OpenAPI tables (axis #2). Every hit carries the
// SHA-pinned forge citation; an empty result is an explicit "nothing", never a
// fabricated answer.
func (h *handlers) searchAPI(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := r.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	q := store.APISearchQuery{
		Text:          query,
		Release:       r.GetString("release", h.baseline),
		SpecID:        r.GetString("spec_id", ""),
		Service:       r.GetString("service", ""),
		ServiceFamily: r.GetString("service_family", ""),
		Method:        r.GetString("method", ""),
		Kind:          r.GetString("kind", "any"),
		TopK:          int(r.GetFloat("top_k", 10)),
	}
	hits, err := h.st.SearchAPI(ctx, q)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search_api failed", err), nil
	}
	type apiResult struct {
		Kind      string         `json:"kind"`
		Score     float64        `json:"score"`
		Operation any            `json:"operation,omitempty"`
		Schema    any            `json:"schema,omitempty"`
		Citation  model.Citation `json:"citation"`
	}
	out := make([]apiResult, 0, len(hits))
	cites := make([]model.Citation, 0, len(hits))
	for _, hit := range hits {
		ar := apiResult{Kind: hit.Kind, Score: hit.Score}
		switch {
		case hit.Op != nil:
			ar.Operation = hit.Op
			ar.Citation = hit.Op.Cite()
		case hit.Sch != nil:
			ar.Schema = hit.Sch
			ar.Citation = hit.Sch.Cite()
		}
		out = append(out, ar)
		cites = append(cites, ar.Citation)
	}
	return jsonResult(map[string]any{
		"query": query, "release": q.Release, "count": len(out),
		"hits": out, "citations": cites,
	})
}

// ---- helpers ------------------------------------------------------------

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("marshal", err), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
