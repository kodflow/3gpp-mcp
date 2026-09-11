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
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/registry"
	"github.com/kodflow/3gpp-mcp/internal/releaseview"
	"github.com/kodflow/3gpp-mcp/internal/rerank"
	"github.com/kodflow/3gpp-mcp/internal/search"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
)

// New builds the MCP server with all 8 tools registered against st. baseline is
// the release every answer is scoped to ("Rel-17"); empty means "latest". When
// set, get_spec returns the baseline content plus an annex of what later
// releases add (so the user is always told newer releases extend the answer).
// vecShards (optional) are attached sub-base aliases from store.AttachShards —
// when present, the vector arm runs the Option-B scatter-gather over them
// instead of the single-DB HNSW.
// etsi (optional) is a SECOND, independent store opened over etsi.duckdb — the
// corpora stay SPLIT (3gpp.duckdb + etsi.duckdb), never merged. When present, the
// handlers federate to it: get_spec / list_releases / get_changelog route a spec_id
// beginning "ETSI " to the ETSI store, and list_specs unions both. nil = 3GPP only.
func New(st store.Reader, version, baseline string, vecShards []string, etsi store.Reader) (*server.MCPServer, *search.Engine) {
	eng := search.New(st)
	eng.UseVectorShards(vecShards)
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
	var etsiEng *search.Engine
	if etsi != nil {
		etsiEng = search.New(etsi) // its own single-DB FTS/HNSW; no 3GPP vec shards
	}
	h := &handlers{st: st, etsi: etsi, eng: eng, etsiEng: etsiEng, reg: registry.Default(), baseline: baseline, version: version}

	// EVERY TOOL IS SHIELDED, AND THAT IS WHY THE WRAPPER IS HERE RATHER THAN
	// INSIDE EACH HANDLER: registrations are a list, and a list is auditable.
	//
	// Cancelling a running DuckDB query makes it raise duckdb::InterruptException,
	// and that C++ exception crosses cgo and ABORTS THE PROCESS — it never becomes
	// a Go error. A client that disconnects mid-tool-call cancels the request
	// context, so on Linux any tool could take the server down. Measured against
	// the published image on 2026-09-06; internal/search carries the numbers.
	//
	// database/sql refuses an ALREADY-cancelled context before the driver sees it,
	// which is why a wrapper at the driver layer cannot help and was removed: the
	// dangerous case is cancellation arriving DURING the query, and only a context
	// that cannot be cancelled at all prevents it.
	shielded := func(f server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return f(context.WithoutCancel(ctx), r)
		}
	}

	s.AddTool(mcp.NewTool("search_spec",
		mcp.WithDescription("Hybrid lexical retrieval over clauses, with citations."),
		mcp.WithString("query", mcp.Required(), mcp.Description("free-text query")),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("series", mcp.Description("e.g. 33")),
		mcp.WithString("spec_type", mcp.Description("TS (default), TR, EN, or any. Omitted = the NORMATIVE types: TS on the 3GPP half (TS-first doctrine), TS+EN on the ETSI half, where a European Norm is a standard and filtering it out would hide most of the catalogue.")),
		mcp.WithString("spec_id", mcp.Description("e.g. 33.128")),
		mcp.WithNumber("top_k", mcp.Description("max results per page (default 10)")),
		mcp.WithString("cursor", mcp.Description("opaque pagination cursor from a previous call's next_cursor")),
		mcp.WithString("mode", mcp.Description("retrieval mode: hybrid (default) | lexical | semantic")),
		mcp.WithBoolean("rerank", mcp.Description("re-score the top candidates with the cross-encoder (default false; needs the -tags onnx binary + reranker model)")),
	), shielded(h.searchSpec))

	s.AddTool(mcp.NewTool("get_spec",
		mcp.WithDescription("Fetch a spec, or a clause/clause-subtree, verbatim with citations."),
		mcp.WithString("spec_id", mcp.Required(), mcp.Description("e.g. 33.128")),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("version", mcp.Description("e.g. 19.6.0; default = latest")),
		mcp.WithString("clause", mcp.Description("clause path or prefix, e.g. 6.2.2.2")),
		mcp.WithBoolean("full", mcp.Description("inline full clause text instead of snippet+resource URI (default false)")),
	), shielded(h.getSpec))

	s.AddTool(mcp.NewTool("get_changelog",
		mcp.WithDescription("Change Request records for a spec between releases or versions, PAGED: `total` is how "+
			"many match, `count` how many this page returns (default 100, at most 500 per call), and `next_cursor` "+
			"fetches the rest. Records are ordered by to_version, then CR number. The note says what a count means: "+
			"0 is \"no record in this corpus\", never \"unchanged\"."),
		mcp.WithString("spec_id", mcp.Required()),
		mcp.WithString("from_release", mcp.Description("e.g. Rel-18, or a version (18.4.0; an ETSI deliverable has only versions)")),
		mcp.WithString("to_release", mcp.Description("e.g. Rel-19, or a version (18.6.0)")),
		mcp.WithString("clause", mcp.Description("filter by affected clause")),
		mcp.WithNumber("limit", mcp.Description("records per page (default 100, max 500)")),
		mcp.WithString("cursor", mcp.Description("opaque cursor from a previous call's next_cursor, for the next page of the SAME query")),
	), shielded(h.getChangelog))

	s.AddTool(mcp.NewTool("list_releases",
		mcp.WithDescription("All (release, version, freeze_date) of a spec, newest first."),
		mcp.WithString("spec_id", mcp.Required()),
	), shielded(h.listReleases))

	s.AddTool(mcp.NewTool("resolve_term",
		mcp.WithDescription("Glossary lookup of an acronym/term (TS 21.905 seed)."),
		mcp.WithString("term", mcp.Required()),
		mcp.WithString("release", mcp.Description("optional release context")),
	), shielded(h.resolveTerm))

	s.AddTool(mcp.NewTool("trace_evolution",
		mcp.WithDescription("How a 4G/legacy network element maps to its 5GC network function(s), "+
			"with the TS 23.501 clause that justifies each edge. NE->NF is many-to-many: "+
			"MME alone splits across AMF, SMF and SMSF. Pass a 5GC name to see what it "+
			"replaced, or an EPC name to see what replaced it. Reads a CURATED table on both halves: "+
			"a count of 0 means no curated edge, not that the entity has no history."),
		mcp.WithString("entity", mcp.Required()),
		mcp.WithString("from_release", mcp.Description("")),
		mcp.WithString("to_release", mcp.Description("")),
	), shielded(h.traceEvolution))

	s.AddTool(mcp.NewTool("find_cross_references",
		mcp.WithDescription("Specs referenced by a spec/clause (TS/TR mentions)."),
		mcp.WithString("spec_id", mcp.Required()),
		mcp.WithString("clause", mcp.Description("restrict to a clause/subtree")),
	), shielded(h.findCrossRefs))

	s.AddTool(mcp.NewTool("list_specs",
		mcp.WithDescription("Catalogue filter by release/series/working_group."),
		mcp.WithString("release", mcp.Description("e.g. Rel-19")),
		mcp.WithString("series", mcp.Description("e.g. 33")),
		mcp.WithString("working_group", mcp.Description("e.g. SA3")),
		mcp.WithString("spec_type", mcp.Description("TS (default), TR, EN, or any. Omitted = the NORMATIVE types: TS on the 3GPP half (TS-first doctrine), TS+EN on the ETSI half, where a European Norm is a standard and filtering it out would hide most of the catalogue.")),
	), shielded(h.listSpecs))

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
	), shielded(h.searchAPI))

	s.AddTool(mcp.NewTool("trace_clause",
		mcp.WithDescription("How a clause's TEXT evolved, PARAGRAPH by paragraph: which points of the spec's "+
			"history carry each statement, when it was introduced, and whether it is gone from the newest one. "+
			"The answer names the AXIS it used: a 3GPP spec evolves along RELEASE, an ETSI deliverable along "+
			"VERSION (it has no releases — TS 102 221 has 126 published versions). "+
			"With from_release and to_release, the paragraphs the clause gained and lost between them. "+
			"Clause-level lineage cannot see this: a clause that changed one sentence looks entirely new to it."),
		mcp.WithString("spec_id", mcp.Required(), mcp.Description("e.g. 23.501, or ETSI TS 102 221")),
		mcp.WithString("clause", mcp.Required(), mcp.Description("exact clause path, e.g. 5.4.4a")),
		// The parameter names say "release" and are kept for compatibility, but the
		// endpoints are matched against whichever of release/version the spec holds
		// them in. An ETSI deliverable is traced between two VERSIONS here.
		mcp.WithString("from_release", mcp.Description("with to_release: report the +/- between the two. A release (Rel-18) or a version (18.4.0) — whichever this spec is published along")),
		mcp.WithString("to_release", mcp.Description("with from_release: report the +/- between the two. A release (Rel-18) or a version (18.4.0) — whichever this spec is published along")),
	), shielded(h.traceClause))

	s.AddTool(mcp.NewTool("help",
		mcp.WithDescription("What this corpus HOLDS and how to drive it: counts per half "+
			"(specs, clauses, vectors), the map from question to tool, and the environment "+
			"knobs that change what you get back. Capabilities and their on/off reasons are "+
			"server_info's job. Read-only, no arguments."),
	), shielded(h.help))

	s.AddTool(mcp.NewTool("server_info",
		mcp.WithDescription("Report the server's retrieval capabilities and why semantic search is on/off "+
			"(so a client knows whether to use mode=semantic). Read-only, no arguments."),
	), shielded(h.serverInfo))

	registerResources(s, h)

	// Domain subjects contribute their own tools (li_events, …). The core knows
	// nothing about any vertical; it just iterates the registry.
	for _, sub := range h.reg.All() {
		for _, tr := range sub.Tools(st, baseline) {
			s.AddTool(tr.Tool, tr.Handler)
		}
	}
	return s, eng
}

type handlers struct {
	st       store.Reader
	etsi     store.Reader // optional SECOND store over etsi.duckdb (split, not merged); nil = 3GPP only
	eng      *search.Engine
	etsiEng  *search.Engine // search engine over the ETSI store; nil = 3GPP only
	reg      *subject.Registry
	baseline string // release every answer is scoped to ("Rel-17"); "" = latest
	version  string
}

// specStore routes a per-spec lookup to the right index: a spec_id beginning "ETSI "
// goes to the attached ETSI store (when present), everything else to the 3GPP store.
// This is how the two SPLIT indexes are federated without a merge.
//
// ONE PREDICATE FOR BOTH ROUTERS (CodeRabbit, #332). This used to test the raw
// string for "ETSI " while storeFor trimmed and ignored case, so the same id could
// reach different halves depending on which tool received it — and get_changelog
// routed with one rule and chose its note with the other. It now delegates.
func (h *handlers) specStore(specID string) store.Reader {
	return h.storeFor(specID)
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

// serverInfo reports retrieval capabilities + WHY semantic is on/off, so a
// client (or a human reading the result) isn't left guessing why mode=semantic
// behaves like lexical. It recomputes the reason independently of the serve-time
// coherence guard (which may already have disabled VSS on a model mismatch).
func (h *handlers) serverInfo(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dbModel := h.st.GetMeta(ctx, "embedding_model")
	clientModel := h.eng.EmbedderModelID()
	vss := h.st.VSSAvailable()

	semantic := false
	reason := ""
	switch {
	case !h.eng.EmbedderEnabled():
		reason = "embedder_disabled (lexical binary or EMBEDDER=off)"
	case dbModel == "":
		reason = "no_vectors_in_db"
	case dbModel != clientModel:
		reason = "model_mismatch (db=" + dbModel + " client=" + clientModel + ")"
	case !vss:
		reason = "hnsw_unavailable (exact-scan fallback)"
	default:
		semantic = true
	}
	// THE SPARSE ARM, with its own reason. It was missing here entirely: a client
	// asking what this server can do was told about lexical, semantic and the
	// reranker, and learned nothing about the third retrieval arm — which
	// search.Engine drops in silence when the model or the postings are absent.
	// "Ask rather than assume" is the whole point of this tool, and a capability it
	// cannot report is one the caller has to guess at.
	st := h.eng.State()
	sparseReason := ""
	switch {
	case !st.SparseEnabled && !h.eng.EmbedderEnabled():
		sparseReason = "embedder_disabled"
	case !st.SparseEnabled && h.st.GetMeta(ctx, "sparse_model") == "":
		sparseReason = "no_sparse_postings_in_db"
	case !st.SparseEnabled:
		sparseReason = "model_has_no_sparse_head (bake data/models/bge-m3-sparse as the ACTIVE model)"
	case !st.SparseOn:
		sparseReason = "turned_off_at_runtime"
	}

	baseline := h.baseline
	if baseline == "" {
		baseline = "latest"
	}
	info := map[string]any{
		"version":                h.version,
		"baseline":               baseline,
		"lexical":                true,
		"fts":                    h.st.FTSAvailable(),
		"semantic":               semantic,
		"reason":                 reason,
		"hnsw":                   vss,
		"sparse":                 st.SparseEnabled && st.SparseOn,
		"sparse_reason":          sparseReason,
		"sparse_model":           h.st.GetMeta(ctx, "sparse_model"),
		"reranker":               h.eng.RerankerEnabled(),
		"reranker_reason":        rerankReason(h.eng.RerankerEnabled()),
		"embedding_model_db":     dbModel,
		"embedding_model_client": clientModel,
		"embed_floor":            h.st.GetMeta(ctx, "embed_floor"),
	}
	// The ETSI half is SERVED ALONGSIDE, never merged, and was invisible here too.
	// Its embedding identity is reported because it is computed per store: an ETSI
	// corpus at a stale identity answers lexically while the 3GPP one does not, and
	// this is the only place a client can see that has happened.
	if h.etsi != nil {
		etsiModel := h.etsi.GetMeta(ctx, "embedding_model")
		info["etsi"] = map[string]any{
			"attached":           true,
			"fts":                h.etsi.FTSAvailable(),
			"hnsw":               h.etsi.VSSAvailable(),
			"sparse":             h.etsi.SparseAvailable(),
			"embedding_model":    etsiModel,
			"embedding_model_ok": etsiModel != "" && etsiModel == clientModel,
		}
	} else {
		info["etsi"] = map[string]any{"attached": false}
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

func (h *handlers) searchSpec(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q, err := r.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	filter := store.SpecFilter{
		Release: r.GetString("release", h.baseline), Series: r.GetString("series", ""),
		DocType: docTypeDefault(r.GetString("spec_type", "")), SpecID: r.GetString("spec_id", ""),
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
	mode, rerank := r.GetString("mode", ""), r.GetBool("rerank", false)
	want := offset + pageSize + 1
	etsiScoped := strings.HasPrefix(filter.SpecID, "ETSI ")
	// Which engine the reported mode must describe. An ETSI-scoped query runs
	// ONLY on the ETSI engine, which carries its own embedder and its own
	// toggles, so reading h.eng below described an engine that never ran.
	servingEng := h.eng
	var hits []model.SearchHit
	if h.etsiEng != nil && etsiScoped {
		// An ETSI-scoped query goes ONLY to the ETSI index. Its clauses live in the
		// "ETSI" release space, so the 3GPP baseline release filter must not apply.
		servingEng = h.etsiEng
		hits, err = h.searchETSI(ctx, q, filter, r.GetString("spec_type", ""), want, mode, rerank)
	} else {
		hits, err = h.eng.Search(ctx, search.Request{Text: q, Filter: filter, TopK: want, Mode: mode, Rerank: rerank})
		// Federate the SPLIT ETSI index: when not scoped to a specific 3GPP spec/series,
		// search it too and RRF-merge so ETSI clauses are searchable, not just reachable
		// by id. The release filter is cleared for ETSI (its own release space).
		if err == nil && h.etsiEng != nil && filter.SpecID == "" && filter.Series == "" {
			if eh, eerr := h.searchETSI(ctx, q, filter, r.GetString("spec_type", ""), want, mode, rerank); eerr == nil && len(eh) > 0 {
				hits = search.RRF(60, hits, eh)
			}
		}
	}
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
	// Say which retrieval actually ran. Search degrades a mode it cannot serve
	// (semantic with no embedder -> lexical), which is the right behaviour and
	// the wrong silence: the hits come back shaped identically and nothing marked
	// the substitution, so a client that asked for mode=semantic was handed BM25
	// with no way to tell.
	requested := mode
	if requested == "" {
		requested = "hybrid"
	}
	served := servingEng.ModeServed(mode)
	resp["mode"] = served
	if served != requested {
		resp["mode_requested"] = requested
		resp["mode_degraded"] = fmt.Sprintf(
			"requested %q, served %q — call server_info for why semantic is unavailable", requested, served)
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
	st := h.specStore(specID) // route "ETSI …" ids to the attached ETSI store
	if version == "" {
		if v, ok, _ := st.VersionForRelease(ctx, specID, release); ok {
			version = v
		} else if _, v, ok, _ := st.LatestVersion(ctx, specID); ok {
			version, release = v, ""
		}
	}
	clauses, err := st.GetClauses(ctx, specID, version, clause)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_spec failed", err), nil
	}
	if len(clauses) == 0 {
		return mcp.NewToolResultError("no such spec/clause in corpus: " + specID), nil
	}
	// Release lineage per clause (present_in / introduced / last_seen / obsolete)
	// so every fragment says where it lives across releases and whether it's gone.
	full := r.GetBool("full", false)
	lineage, _ := st.ClauseLineage(ctx, specID, clause)
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
		"stable":         model.IsStableSpecVersion(specID, version),
	}
	// Stable-first doctrine: the resolver already prefers a published version, so a
	// draft here means NO stable version is indexed for this spec/release. Say so
	// loudly rather than let the client treat work-in-progress text as normative.
	//
	// Half-aware (model.IsStableSpecVersion): the "major < 3" rule is 3GPP's, and
	// applied to ETSI it warned that 4 354 of 5 142 published deliverables were
	// drafts — ETSI TS 103 221-1 V1.23.1 among them.
	if !model.IsStableSpecVersion(specID, version) {
		resp["draft_warning"] = "returned version " + version +
			" is a DRAFT (major < 3, work-in-progress); no stable/published version is indexed for this spec/release"
	}
	if !full {
		resp["resource_hint"] = "Full clause text is omitted when truncated=true. " +
			"Call resources/read on a clause's `resource` URI (3gpp://…) to fetch it, or pass full=true."
	}
	// Release annex: what the baseline introduced vs the previous release, and
	// what LATER releases add on top (surfaced so the user knows it exists).
	if release != "" {
		if avail, ordered, aerr := st.ClauseAvailability(ctx, specID, clause); aerr == nil {
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
	// The store applies release bounds and nothing else; a version bound is applied
	// here, and a bound neither can read is reported rather than dropped in silence.
	// See changelogBound for the measurement.
	from := parseChangelogBound(r.GetString("from_release", ""))
	to := parseChangelogBound(r.GetString("to_release", ""))
	st := h.specStore(specID)
	// THE NOTE SPEAKS FOR THE SPEC, SO IT READS THE SPEC'S RECORDS — ALL OF THEM.
	//
	// `changes` is about to be narrowed to one clause, and changelogNote describes
	// the whole change history: fed the narrowed slice it would answer "this corpus
	// holds no citable records for 23.501" whenever the clause simply has none, on
	// a spec with plenty — the same false zero this release exists to remove, one
	// level down. Keep the unfiltered set for it.
	//
	// And not narrowed by the RANGE either, for the same reason one level up: a
	// request for Rel-5..Rel-5 on a spec whose records start at Rel-15 is empty,
	// and a note fed that empty set told the caller the spec had no records at all.
	all, err := st.GetChangelog(ctx, specID, "", "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("get_changelog failed", err), nil
	}
	changes := all
	bounded := from.raw != "" || to.raw != ""
	if bounded {
		// Only a RELEASE bound is the store's to apply; for any other the query
		// would repeat the one above verbatim (CodeRabbit, #332).
		if from.release != "" || to.release != "" {
			if changes, err = st.GetChangelog(ctx, specID, from.release, to.release); err != nil {
				return mcp.NewToolResultErrorFromErr("get_changelog failed", err), nil
			}
		}
		changes = applyVersionBounds(changes, from, to)
	}
	inRange := len(changes)
	clause := r.GetString("clause", "")
	// How many records in range name NO clause: the filter cannot test those, so
	// they leave the answer whether or not they touched the clause.
	clauseless := 0
	if clause != "" {
		var filtered []model.Change
		for _, c := range changes {
			// A list holding only "" names no clause: it is what an empty list
			// becomes when it is written through string_split('', sep).
			if strings.TrimSpace(strings.Join(c.Clauses, "")) == "" {
				clauseless++
			}
			for _, cl := range c.Clauses {
				if strings.HasPrefix(cl, clause) {
					filtered = append(filtered, c)
					break
				}
			}
		}
		changes = filtered
	}
	// PAGED, AFTER EVERY FILTER AND IN ONE TOTAL ORDER. The bounds and the clause
	// decide which records exist for this question; only then is the list sorted
	// (sortChanges: a page of an unordered list is not a page) and cut. The cursor
	// is bound to the question, so it cannot be replayed against another range.
	// See changelog_pagination.go for the measurement behind the default page.
	sortChanges(changes)
	total := len(changes)
	limit, clamped := changelogLimit(r.GetInt("limit", 0))
	page, start, next, err := paginate(changes, r.GetString("cursor", ""), changelogQueryHash(specID, from, to, clause), limit)
	if err != nil {
		return mcp.NewToolResultError("invalid cursor for this query: pass the next_cursor of a get_changelog call " +
			"with the same spec_id, from_release, to_release and clause"), nil
	}
	// An empty slice, not nil: `"changes": null` is a different JSON type from the
	// list a caller iterates, and the count beside it already says there are none.
	if page == nil {
		page = []model.Change{}
	}
	out := map[string]any{
		"spec_id": specID, "count": len(page), "changes": page,
		"total": total, "returned": len(page), "offset": start, "limit": limit,
	}
	if next != "" {
		out["next_cursor"] = next
	}
	// A ZERO THAT MEANS TWO DIFFERENT THINGS IS NOT AN ANSWER.
	//
	// The changes table holds 3GPP change requests. The ETSI half carries none, so
	// every ETSI deliverable answered count 0 — which reads as "this deliverable
	// never changed", on a corpus that keeps all 126 published versions of
	// TS 102 221 precisely because it did.
	//
	// The gap is not an oversight in the pipeline; it is what the source allows.
	// A 3GPP change history survives .doc conversion as an HTML TABLE. ETSI ships
	// PDFs, and `pdftotext -layout` flattens that table into space-aligned text
	// whose rows split across lines, drop their Date/Meeting on continuation rows,
	// and leave the Old/New columns blank — so a parser would attach a CR summary
	// to the wrong CR number and the wrong version transition. A change record that
	// names the wrong transition is worse than no record: it is a citation that
	// looks authoritative and is false.
	//
	// So say what is true, and name the tool that DOES answer the question from the
	// text itself. trace_clause diffs a clause between two versions as a set
	// operation on paragraph ids — no parsing, nothing reconstructed.
	//
	// The ETSI note is read from the ETSI corpus (etsiChangelogNote), so it stays
	// true whether or not that half carries records, and it now also speaks when
	// the ETSI half is not attached at all — a case both branches used to leave
	// silent. ETSI records, when there are any, cite the published PDF whose
	// annex they were read from.
	note := ""
	if isETSISpecID(specID) {
		note = etsiChangelogNote(ctx, h.etsi, specID, all)
		if h.etsi != nil && len(page) > 0 {
			cites, uncited := etsiChangeCitations(ctx, h.etsi, specID, page)
			out["citations"] = cites
			if uncited > 0 {
				note = fmt.Sprintf("%d of these records name a version whose document this corpus could not "+
					"resolve, so they carry no citation. ", uncited) + note
			}
		}
	} else {
		note = changelogNote(ctx, st, specID, all)
	}
	narrowed := ""
	switch {
	case bounded && inRange == 0 && len(all) > 0:
		narrowed = fmt.Sprintf("none of the %d records held for %s falls inside the requested range.", len(all), specID)
	case clause != "" && clauseless > 0:
		// A FILTER THAT CANNOT MATCH IS NOT A NEGATIVE ANSWER. The 3GPP records
		// come from the CR database, which records a version transition and not
		// the clause paths a change touched — so `clauses` is NULL on every one of
		// them, and a clause filter answered 0 for every clause of every spec.
		//
		// And a PARTLY blind filter is not a complete answer either (Qodo, #332):
		// when only some records name their clauses, the others were dropped
		// untested, and a count built from the rest — zero or not — must say so.
		// "in range" only when a bound was actually APPLIED: an unreadable one is
		// named as not applied by boundsNote, and the two must not contradict.
		scope := ""
		if from.release != "" || from.version != "" || to.release != "" || to.version != "" {
			scope = " in range"
		}
		if clauseless == inRange {
			narrowed = fmt.Sprintf("none of the %d records%s names the clauses it touched, so the clause "+
				"filter cannot match any of them: count 0 says nothing about clause %s. trace_clause answers "+
				"that from the clause text.", inRange, scope, clause)
		} else {
			narrowed = fmt.Sprintf("%d of the %d records%s name no clause, so the clause filter could not "+
				"test them: they are left out of this answer whether or not they touched clause %s. "+
				"trace_clause answers that from the clause text.", clauseless, inRange, scope, clause)
		}
	}
	// The page note leads: a caller who does not know the list is truncated reads
	// everything after it as the whole history.
	if n := joinNotes(joinNotes(joinNotes(pageNote(total, start, len(page), limit, clamped, next),
		boundsNote(from, to)), narrowed), note); n != "" {
		out["note"] = n
	}
	return jsonResult(out)
}

// changelogNote applies to the 3GPP half the standard the ETSI half was already
// held to one branch above: say what the number means.
//
// THE TABLE HAD NO WRITER, AND NOW IT HAS ONE. Measured on the corpus published
// 2026-09-09, over 3 568 specs: 3 352 records named no CR and no version
// transition — the HEADER row of the change-history table, read positionally like
// a body row, 3 026 of them summarised "Date" — and dropping those left 311 specs
// with any change history at all. The writer had been deleted with the Go
// HTML-ingest write side (Phase 11b, c635038) and never reimplemented in Rust.
// `ingest-crs` now rewrites the table from the 3GPP Change Request database, the
// authority the printed change-history tables are rendered from.
//
// WHAT STILL HAS TO BE SAID, because it is a different silence rather than none:
//
//   - The CR database records CHANGE REQUESTS. An MCC editorial republication
//     carries no CR and is not in it, so a spec can have moved version without
//     gaining a record here.
//   - It is a periodic export. Anything approved after the export the corpus was
//     built from is absent rather than empty, and `changes_source` names which
//     export that was so the two can be told apart.
//
// Both branches name trace_clause, which answers the same question from the clause
// text rather than from this table, exactly as the ETSI branch does.
func changelogNote(ctx context.Context, st store.Reader, specID string, changes []model.Change) string {
	const useTraceClause = "Use trace_clause with from_release/to_release to diff a clause between two " +
		"versions from the text itself, which is derived from the corpus rather than from this table."
	// NO CORPUS COUNTS IN THE SERVED TEXT. An earlier draft carried "covers 311 of
	// the 3 568 specs": true of the snapshot it was measured on and silently false
	// of every later one, which is the failure mode of a note whose whole job is to
	// stop a number from being read as more than it is. The export STAMP is not a
	// count — it is read from the corpus being served, so it cannot go stale
	// against it.
	source := strings.TrimSpace(st.GetMeta(ctx, "changes_source"))
	from := " from the 3GPP change-request database"
	if source != "" {
		from += " (" + source + ")"
	}
	if len(changes) == 0 {
		return "this corpus holds no citable change-request records for " + specID +
			". The table is built" + from + ", which records change requests — not " +
			"editorial republications, and not anything approved after that export. A count of 0 " +
			"means \"no change request recorded here\", not \"never changed\". " + useTraceClause
	}
	// GetChangelog orders by to_version ASC through versionOrderSQL, so the newest
	// recorded transition is the last row that names one. Scanning backwards uses
	// that order instead of re-deriving it — and a plain string max would not do:
	// "9.0.0" sorts above "18.0.0".
	newest := ""
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].ToVersion != "" {
			newest = changes[i].ToVersion
			break
		}
	}
	if newest == "" {
		return ""
	}
	_, latest, ok, err := st.LatestVersion(ctx, specID)
	if err != nil || !ok || compareVersions(latest, newest) <= 0 {
		return ""
	}
	return "this change history stops at " + newest + ", and the corpus holds " + latest +
		". The table is built" + from + ", so anything approved after that export — or " +
		"republished editorially, which raises no change request — is absent rather than empty. " +
		useTraceClause
}

// isETSISpecID is storeFor's routing predicate, named so a caller can ask the
// question without asking for the store.
func isETSISpecID(specID string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(specID)), "ETSI")
}

func (h *handlers) listReleases(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	vs, err := h.specStore(specID).ListReleases(ctx, specID)
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
	// FEDERATE, like search_spec and list_specs already do. resolve_term read the
	// 3GPP store alone, so it answered only from TS 21.905's 1 300 terms — which do
	// not include UICC, ADF or AID, on a corpus that holds the deliverables those
	// terms are defined in. The ETSI half has no vocabulary spec: each deliverable
	// carries its own Abbreviations clause, and ingest-glossary mines them.
	//
	// 3GPP first: it is the canonical vocabulary for a term both halves define, and
	// an ETSI row names the DELIVERABLE that declares it — "ETSI TS 103 221-1" — so
	// a caller can still tell the halves apart without this having to say so twice,
	// and can now open the document the answer came from.
	//
	// That used to be the constant "etsi", which named nothing and could therefore
	// be neither cited nor ranked: every ETSI row tied, and Store.ResolveTerm's
	// tie-break handed back the alphabetically first expansion. Each row now also
	// carries declared_by, the number of deliverables that agree on it, which is
	// what orders a corpus that publishes no precedence rule of its own.
	a, err := h.st.ResolveTerm(ctx, term)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve_term failed", err), nil
	}
	if h.etsi != nil {
		seen := make(map[[3]string]bool, len(a))
		for _, x := range a {
			seen[[3]string{x.Term, x.Expansion, x.Domain}] = true
		}
		e, eErr := h.etsi.ResolveTerm(ctx, term)
		if eErr != nil {
			return mcp.NewToolResultErrorFromErr("resolve_term failed on the ETSI half", eErr), nil
		}
		for _, x := range e {
			if k := ([3]string{x.Term, x.Expansion, x.Domain}); !seen[k] {
				seen[k] = true
				a = append(a, x)
			}
		}
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
	// FEDERATED, like every other lookup that can name an ETSI entity, and each
	// half reports how many edges it had to look at — see evolutionHalves and
	// evolutionNote for the silence this replaces.
	//
	// ONE HALF FAILING DOES NOT SILENCE THE OTHER (CodeRabbit, #332). search_spec
	// federates the same optional half defensively, and aborting here would drop
	// the 3GPP edges — today the only ones there are — over an ETSI read error. A
	// half that cannot be read is named in the note and left out of edges_held, so
	// its silence is never read as a count of zero. Only when no half can be read
	// is there nothing to serve.
	evos := []model.Evolution{}
	cites := []model.Citation{}
	held := map[string]int{}
	var unread []string
	var lookupErr error
	halves := h.evolutionHalves()
	for _, half := range halves {
		es, err := half.st.GetEvolutions(ctx, entity)
		if err != nil {
			unread = append(unread, half.name)
			lookupErr = err
			continue
		}
		for _, e := range es {
			evos = append(evos, e)
			cites = append(cites, h.evolutionCitation(ctx, e))
		}
		if n, err := countEvolutions(ctx, half.st); err == nil {
			held[half.name] = n
		} else {
			unread = append(unread, half.name)
		}
	}
	if lookupErr != nil && len(unread) == len(halves) {
		return mcp.NewToolResultErrorFromErr("trace_evolution failed on every half", lookupErr), nil
	}
	return jsonResult(map[string]any{
		"entity":     entity,
		"count":      len(evos),
		"evolutions": evos,
		"citations":  cites,
		"edges_held": held,
		"note":       evolutionNote(entity, len(evos), held, unread, h.etsi != nil),
	})
}

// docTypeDefault applies the TS-first doctrine (CLAUDE.md §7: "TS ≠ TR — toujours
// filtrer par défaut sur TS"). When the caller omits spec_type we default to "TS"
// so TR (informative study reports) don't pollute normative results. Explicit
// "TR" filters to TR; "any"/"all"/"*" (case-insensitive) opt into the mixed set
// by returning "" (no doc_type filter). Issue #6.
func docTypeDefault(specType string) string {
	switch strings.ToLower(strings.TrimSpace(specType)) {
	case "":
		return "TS" // TS-first default
	case "any", "all", "*":
		return "" // explicit opt-in to every type
	case "tr":
		return "TR"
	case "ts":
		return "TS"
	case "en":
		// ETSI European Norms. Without this case the value fell through to the
		// pass-through below and reached the store as the lowercase "en", which
		// matches nothing: the filter is an exact string comparison against the
		// stored "EN". Asking for ENs returned silence.
		return "EN"
	default:
		// Pass through, UPPERCASED. The store compares exactly, so a caller who
		// spelled a real type in lower case would otherwise get an empty result
		// that is indistinguishable from "no such specs".
		return strings.ToUpper(strings.TrimSpace(specType))
	}
}

var reSpecRef = regexp.MustCompile(`\bT[SR]\s?(\d\d\.\d{3})\b`)

// reEtsiRef catches ETSI deliverable mentions ("ETSI TS 103 221-1", "TS 103 280")
// that the 3GPP miner cannot see — the LI X1/X2/X3 base specs 33.128 profiles. The
// id shape (1NN NNN[-P], space-separated) is disjoint from 3GPP's dotted NN.NNN, so
// the two miners never overlap. Matched ids are cited as a pointer to the ETSI
// deliver archive (model.EtsiDeliverURL), never ingested (cite-or-silent, §1).
var reEtsiRef = regexp.MustCompile(`(?:ETSI\s+)?T[SR]\s?(1\d{2}\s?\d{3}(?:-\d+)?)\b`)

func (h *handlers) findCrossRefs(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// ROUTE THE SOURCE SPEC. This handler read h.st unconditionally while get_spec
	// and trace_clause went through storeFor, so asking an ETSI deliverable for its
	// references hit the 3GPP store, found no such spec, and answered count 0 —
	// silently, on clause 2, which IS the normative reference list. The two halves
	// are federated and never merged, so the routing has to be explicit everywhere.
	src := h.storeFor(specID)
	release, version := "", ""
	if rel, v, ok, _ := src.LatestVersion(ctx, specID); ok {
		release, version = rel, v
	}
	clauses, err := src.GetClauses(ctx, specID, version, r.GetString("clause", ""))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("find_cross_references failed", err), nil
	}
	seen := map[string]bool{}
	var refs []string
	// One citation per resolved reference: the regex only yields a spec_id, so we
	// resolve each referenced spec's latest indexed release/version to complete the
	// citation (issue #7). A reference not in the corpus still gets spec_id + a
	// spec-directory URL (cite-or-silent never drops the pointer).
	refCites := make([]model.Citation, 0)
	// ETSI references are mined in a SEPARATE pass / separate result keys so the 3GPP
	// `references`/`ref_citations` stay byte-identical for existing consumers. ETSI
	// ids (1NN NNN[-P]) are cited as a pointer to the deliver archive (no ingestion).
	etsiSeen := map[string]bool{}
	etsiRefs := make([]string, 0)
	etsiCites := make([]model.Citation, 0)
	for _, c := range clauses {
		hay := c.Heading + " " + c.Text
		for _, m := range reSpecRef.FindAllStringSubmatch(hay, -1) {
			id := m[1]
			if id != specID && !seen[id] {
				seen[id] = true
				refs = append(refs, id)
				rel, ver, _, _ := h.st.LatestVersion(ctx, id)
				url := model.ArchiveURL(id, ver)
				if url == "" {
					url = "https://www.3gpp.org/ftp/Specs/archive/" + model.SeriesOf(id) + "_series/" + id + "/"
				}
				refCites = append(refCites, model.Citation{SpecID: id, Release: rel, Version: ver, URL: url, Stable: model.IsStableSpecVersion(id, ver)})
			}
		}
		for _, m := range reEtsiRef.FindAllStringSubmatch(hay, -1) {
			id, ok := model.NormalizeEtsiID(m[1])
			if !ok || etsiSeen[id] {
				continue
			}
			etsiSeen[id] = true
			etsiRefs = append(etsiRefs, id)
			// RESOLVE THE MENTION AGAINST THE ATTACHED HALF WHEN THERE IS ONE.
			//
			// A bare "TS 103 280" in prose names no version and no document type, so
			// this used to cite the etsi_ts deliver FOLDER and stop. That was right
			// when the ETSI half held 14 deliverables; it now holds 11 822 versions of
			// 5 142 deliverables, so the corpus usually knows the exact one — and a
			// versioned PDF is a citation a reader can open at the right text.
			//
			// The type is tried in normative order and the first that resolves wins:
			// nothing in "103 101" says TR, and the trees are disjoint (etsi_ts/103101
			// is a 404 while etsi_tr/103101 is TR 103 101). Unresolved still cites the
			// folder — cite the pointer, never fabricate a version.
			cite := model.Citation{SpecID: "ETSI TS " + id, URL: model.EtsiDeliverURL(id, "")}
			if h.etsi != nil {
				for _, dt := range []string{"TS", "EN", "TR"} {
					full := "ETSI " + dt + " " + id
					if rel, v, ok, _ := h.etsi.LatestVersion(ctx, full); ok {
						cite = model.Citation{
							SpecID: full, Release: rel, Version: v,
							URL: model.SpecURL(full, v), Stable: model.IsStableSpecVersion(full, v),
						}
						break
					}
				}
			}
			etsiCites = append(etsiCites, cite)
		}
	}
	return jsonResult(map[string]any{
		"spec_id": specID, "release": release, "version": version,
		"count": len(refs), "references": refs,
		// Source-spec citation (where the references were found) + one citation per
		// referenced spec, each with whatever provenance is resolvable.
		// SpecURL, not ArchiveURL: find_cross_references is routed to the ETSI
		// store for a spec_id beginning "ETSI ", and ArchiveURL answers "" for
		// anything that is not a 3GPP id — so this citation named a deliverable
		// with no pointer to it.
		"citation":      model.Citation{SpecID: specID, Release: release, Version: version, URL: model.SpecURL(specID, version), Stable: model.IsStableSpecVersion(specID, version)},
		"ref_citations": refCites,
		// ETSI cross-references (separate keys; absent-as-empty, never null).
		"etsi_references":    etsiRefs,
		"etsi_ref_citations": etsiCites,
	})
}

func (h *handlers) listSpecs(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specs, err := h.st.ListSpecs(ctx, store.SpecFilter{
		Release: r.GetString("release", h.baseline), Series: r.GetString("series", ""),
		WorkingGroup: r.GetString("working_group", ""), DocType: docTypeDefault(r.GetString("spec_type", "")),
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
	// Union the SPLIT ETSI index (when attached): ETSI specs live in their own
	// release space ("ETSI"), so the 3GPP release filter never applies to them —
	// pass series/WG/doc_type through but clear the release so they always surface.
	if h.etsi != nil {
		for _, dt := range etsiDocTypes(r.GetString("spec_type", "")) {
			es, eerr := h.etsi.ListSpecs(ctx, store.SpecFilter{
				Series: r.GetString("series", ""), WorkingGroup: r.GetString("working_group", ""),
				DocType: dt,
			})
			if eerr != nil {
				continue
			}
			for _, sp := range es {
				rows = append(rows, specRow{Spec: sp, HasAPI: false})
			}
		}
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

// traceClause answers how a clause's TEXT evolved, paragraph by paragraph.
//
// The existing lineage tools work at clause granularity, which cannot see the
// change that matters most in a spec: a clause that gains or loses one sentence
// between releases looks, to them, exactly like a clause that was rewritten.
// Since the corpus stores each paragraph once (ADR 0004), "which releases carry
// this sentence" is a lookup rather than a diff.
//
// With from/to it answers the narrower question — what this clause gained and
// lost between two releases — as a set operation on paragraph ids, never a text
// comparison.
func (h *handlers) traceClause(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	specID, err := r.RequireString("spec_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	clause, err := r.RequireString("clause")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	st := h.storeFor(specID)
	if !st.ContentAddressed() {
		return mcp.NewToolResultError(
			"this corpus does not carry paragraph-level provenance — it predates the content-addressed " +
				"storage (ADR 0004). Clause-level lineage is still available through get_spec and list_releases."), nil
	}

	from := r.GetString("from_release", "")
	to := r.GetString("to_release", "")
	if from != "" && to != "" {
		added, removed, kept, dErr := st.ClauseDelta(ctx, specID, clause, from, to)
		if dErr != nil {
			return mcp.NewToolResultErrorFromErr("trace_clause delta failed", dErr), nil
		}
		return jsonResult(map[string]any{
			"spec_id": specID, "clause": clause, "from": from, "to": to,
			"added": added, "removed": removed, "unchanged_paragraphs": kept,
		})
	}

	traces, lErr := st.ParagraphLineage(ctx, specID, clause)
	if lErr != nil {
		return mcp.NewToolResultErrorFromErr("trace_clause failed", lErr), nil
	}
	// SAY what present_in is listing. A 3GPP spec is traced across releases; an
	// ETSI deliverable has no releases (parse_etsi_meta stamps the constant
	// "ETSI") and is traced across its published versions. Returning the values
	// without the axis leaves the reader to guess which, and "1.21.1" versus
	// "Rel-18" is only obvious until an id happens to look like both.
	resp := map[string]any{"spec_id": specID, "clause": clause, "paragraphs": traces}
	if axis, ordered, aErr := st.LineageAxis(ctx, specID); aErr == nil {
		resp["axis"] = axis
		resp["axis_values"] = ordered
	}
	return jsonResult(resp)
}

// storeFor routes an id to the corpus that owns it. ETSI ids are served by the
// second store when one is attached; everything else is 3GPP. The two are never
// merged, so the routing has to be explicit.
func (h *handlers) storeFor(specID string) store.Reader {
	if h.etsi != nil && isETSISpecID(specID) {
		return h.etsi
	}
	return h.st
}

// etsiDocTypes is the ETSI half's answer to spec_type, and it deliberately differs
// from the 3GPP half's.
//
// The TS-first default exists because in 3GPP a TS is the norm and a TR is a study
// report, so browsing a catalogue should not be dominated by reports. Applied
// verbatim to the ETSI half it stops meaning that: ETSI's normative output is TS
// *and* EN — a European Norm is a standard, often the harmonised one that carries
// legal presumption of conformity — and 1 542 of the 5 117 deliverables in this
// corpus are ENs. Filtering on the literal string "TS" would therefore hide the
// majority of the ETSI catalogue by default, for a distinction that does not exist
// there.
//
// So an unset spec_type means "the normative types" here, and the caller who wants
// reports asks for them, exactly as on the 3GPP side. An explicit value is honoured
// as given, and "any" clears the filter for both halves alike.
//
// It returns a LIST because store.SpecFilter.DocType is one exact-match string; two
// small catalogue queries are cheaper than widening the filter shape for this.
func etsiDocTypes(specType string) []string {
	switch dt := docTypeDefault(specType); dt {
	case "TS":
		// Only when the caller said nothing: an explicit spec_type=TS must mean TS.
		if strings.TrimSpace(specType) == "" {
			return []string{"TS", "EN"}
		}
		return []string{"TS"}
	default:
		return []string{dt} // "" (any) clears the filter, everything else is exact
	}
}

// searchETSI runs a query against the ETSI index with that half's own conventions.
//
// Two of them, and both were wrong before this existed. The release filter is
// cleared: ETSI clauses live in their own "ETSI" release space, so a 3GPP baseline
// like Rel-19 matches nothing there. And the document-type default is the
// NORMATIVE SET rather than the literal "TS" — the same rule list_specs uses, for
// the same reason (see etsiDocTypes). Copying the 3GPP filter wholesale meant an
// unqualified search could never surface an ETSI EN or TR at all: 56% of that
// corpus was reachable by id and invisible to search, with nothing saying so.
//
// One query per type, RRF-merged. RRF is rank-based, so merging two lists from the
// same engine is the same operation as merging the ETSI list into the 3GPP one —
// no score calibration is implied between them.
func (h *handlers) searchETSI(ctx context.Context, q string, f store.SpecFilter, specType string,
	topK int, mode string, rerank bool) ([]model.SearchHit, error) {
	f.Release = ""

	// A spec_id PINS the document, so the type default must not second-guess it.
	// Without this, search_spec(spec_id="ETSI TR 103 101") with no spec_type takes
	// the normative default {TS, EN}, neither matches a TR, and the query returns
	// NOTHING for a document the caller named explicitly — a filter answering a
	// question the caller had already answered.
	types := etsiDocTypes(specType)
	if f.SpecID != "" && strings.TrimSpace(specType) == "" {
		types = []string{""}
	}

	var lists [][]model.SearchHit
	var firstErr error
	for _, dt := range types {
		ef := f
		ef.DocType = dt
		hits, err := h.etsiEng.Search(ctx, search.Request{Text: q, Filter: ef, TopK: topK, Mode: mode, Rerank: rerank})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(hits) > 0 {
			lists = append(lists, hits)
		}
	}
	switch {
	case len(lists) == 0 && firstErr != nil:
		return nil, firstErr
	case len(lists) == 0:
		return nil, nil
	case len(lists) == 1:
		return lists[0], nil
	default:
		return search.RRF(60, lists...), nil
	}
}

// rerankReason mirrors sparse_reason: an arm reported as false must say why.
//
// Every failure path in the ONNX reranker returns Disabled{} — that is correct,
// a retrieval arm degrades rather than stopping the server — but it left
// "reranker": false with nothing to act on. Measured on this machine: false,
// with model.onnx and tokenizer.json both on disk and the EMBEDDER live on the
// same ONNX runtime, which rules out the two guesses an operator would make
// first.
func rerankReason(enabled bool) string {
	if enabled {
		return ""
	}
	return rerank.Reason()
}

// compareVersions orders two dotted versions numerically, field by field, and is
// the Go twin of store.versionOrderSQL: split on ".", a field that is not an
// integer counts as 0, a missing field counts as 0. Returns -1, 0 or +1.
//
// It exists because the obvious string comparison is wrong on exactly the specs
// that matter: "9.0.0" > "18.0.0" lexically, so a changelog that stopped at 9.0.0
// would be reported as ahead of a corpus holding 18.0.0 — the staleness note
// would then be silent precisely where the gap is largest.
func compareVersions(a, b string) int {
	af, bf := strings.Split(a, "."), strings.Split(b, ".")
	n := len(af)
	if len(bf) > n {
		n = len(bf)
	}
	at := func(f []string, i int) int {
		if i >= len(f) {
			return 0
		}
		v, err := strconv.Atoi(strings.TrimSpace(f[i]))
		if err != nil {
			return 0
		}
		return v
	}
	for i := 0; i < n; i++ {
		x, y := at(af, i), at(bf, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
