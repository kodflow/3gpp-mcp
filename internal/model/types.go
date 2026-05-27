package model

import "time"

// Spec is a 3GPP specification catalogue entry (table: specs).
type Spec struct {
	SpecID       string `json:"spec_id"`       // "33.128"
	Series       string `json:"series"`        // "33"
	Title        string `json:"title"`         // free text from the cover page
	DocType      string `json:"doc_type"`      // "TS" | "TR"
	WorkingGroup string `json:"working_group"` // "SA3" (best-effort, may be empty)
}

// SpecVersion is one published version of a spec for a given release
// (table: spec_versions). Ordering MUST use (Release, Version, FreezeDate)
// together: 3GPP version numbers are not monotonic across releases
// (CLAUDE.md §8.3), so semver alone is insufficient.
type SpecVersion struct {
	SpecID     string     `json:"spec_id"`
	Release    string     `json:"release"`     // "Rel-18"
	Version    string     `json:"version"`     // "18.6.0"
	FreezeDate *time.Time `json:"freeze_date"` // best-effort, nil if unknown
	DocxURL    string     `json:"docx_url"`
}

// Clause is a clause-level chunk of a spec (table: clauses). Chunking is always
// clause-aware — never an arbitrary token window (CLAUDE.md §13).
type Clause struct {
	ChunkID     uint64    `json:"chunk_id"`
	SpecID      string    `json:"spec_id"`
	Release     string    `json:"release"`
	Version     string    `json:"version"`
	ClausePath  string    `json:"clause_path"` // "6.2.3.1"
	Heading     string    `json:"heading"`
	Text        string    `json:"text"`
	IsNormative bool      `json:"is_normative"`
	Embedding   []float32 `json:"-"` // 1024-dim BGE-M3 dense vector; nil until Phase 4
}

// Change is a Change Request record (table: changes). A single CR can touch
// several specs at once, so the natural key is cr_number, not spec_id
// (CLAUDE.md §8.4).
type Change struct {
	CRNumber    string   `json:"cr_number"`
	CRRevision  int      `json:"cr_revision"`
	SpecID      string   `json:"spec_id"`
	FromVersion string   `json:"from_version"`
	ToVersion   string   `json:"to_version"`
	Meeting     string   `json:"meeting"`  // "SA2#150"
	Category    string   `json:"category"` // A|B|C|D|F
	Clauses     []string `json:"clauses"`
	Summary     string   `json:"summary"`
	TDocURL     string   `json:"tdoc_url"`
}

// Acronym is a glossary entry (table: acronyms). Acronyms are contextual:
// the same term can expand differently per domain/release (CLAUDE.md §8.5),
// hence the (Term, Domain) composite key.
type Acronym struct {
	Term         string `json:"term"`
	Expansion    string `json:"expansion"`
	Domain       string `json:"domain"` // 5GC|EPC|IMS|RAN|""
	FirstRelease string `json:"first_release"`
	LastRelease  string `json:"last_release"`
}

// Evolution is an inter-entity evolution edge (table: evolutions), e.g.
// MME -> AMF (SPLIT). Many-to-many with a confidence, since NE->NF mappings
// are rarely 1:1 (CLAUDE.md §8.6). Populated in V2 (graph layer).
type Evolution struct {
	FromTerm            string  `json:"from_term"`
	ToTerm              string  `json:"to_term"`
	EvolutionType       string  `json:"evolution_type"` // SPLIT|MERGE|RENAME|REPLACED_BY|EXTENDED_BY
	JustificationSpec   string  `json:"justification_spec"`
	JustificationClause string  `json:"justification_clause"`
	Confidence          float64 `json:"confidence"`
}

// Citation is the provenance block attached to every MCP response. A value that
// cannot produce a citation must not be returned by a tool (CLAUDE.md §1:
// "Pas d'hallucination tolérée").
type Citation struct {
	SpecID  string `json:"spec_id"`
	Release string `json:"release"`
	Version string `json:"version"`
	Clause  string `json:"clause"`
	URL     string `json:"url"`
}

// Cite builds the citation for this clause.
func (c Clause) Cite() Citation {
	return Citation{
		SpecID:  c.SpecID,
		Release: c.Release,
		Version: c.Version,
		Clause:  c.ClausePath,
		URL:     ArchiveURL(c.SpecID, c.Version),
	}
}

// Lineage is the release lineage of an indexed element (clause): the ordered
// set of releases it appears in, when it was introduced, when it was last seen,
// and whether it is obsolete (present in earlier releases but removed before the
// spec's newest indexed release). Surfaced on every fragment so a caller never
// cites a mechanism that no longer exists in the target release.
type Lineage struct {
	PresentIn  []string `json:"present_in"` // releases containing it, oldest-first
	Introduced string   `json:"introduced"` // first release it appears in
	LastSeen   string   `json:"last_seen"`  // newest release it appears in
	Obsolete   bool     `json:"obsolete"`   // removed before the spec's newest release
}

// SearchHit is a scored clause returned by the retrieval layer.
type SearchHit struct {
	Clause   Clause   `json:"-"`
	Score    float64  `json:"score"`
	Citation Citation `json:"citation"`
}

// APIOperation is one path+method of a 5GC service, parsed from the canonical
// OpenAPI YAML on 3GPP Forge (axis #2). Locator() overloads Citation.Clause so
// API answers honour the same cite contract as prose clauses.
type APIOperation struct {
	OpID          uint64   `json:"-"`
	SpecID        string   `json:"spec_id"`         // '29.518' (from externalDocs)
	Release       string   `json:"release"`         // 'Rel-18' (resolved ref)
	Version       string   `json:"version"`         // '18.13.0' (TS version)
	APIDocVersion string   `json:"api_doc_version"` // info.version, informative
	Service       string   `json:"service"`         // 'Namf_Communication'
	ServiceFamily string   `json:"service_family"`  // 'Namf'
	APIRoot       string   `json:"api_root"`        // 'namf-comm/v1'
	Path          string   `json:"path"`            // '/ue-contexts/{ueContextId}'
	Method        string   `json:"method"`          // 'PUT'
	OperationID   string   `json:"operation_id"`    // 'CreateUEContext'
	Summary       string   `json:"summary"`
	Tags          []string `json:"tags"`
	RequestSchema string   `json:"request_schema"` // best-effort, may be ''
	ResponseCodes []string `json:"response_codes"`
	YAMLFile      string   `json:"yaml_file"`
	ForgeSHA      string   `json:"forge_sha"`
	ForgeURL      string   `json:"forge_url"`
}

// Locator is the stable operation locator used as the citation "clause" (§3.1):
// "API <apiRoot> <METHOD> <path> (<operationId>)".
func (o APIOperation) Locator() string {
	loc := "API " + o.APIRoot + " " + o.Method + " " + o.Path
	if o.OperationID != "" {
		loc += " (" + o.OperationID + ")"
	}
	return loc
}

// Cite builds the citation for this API operation.
func (o APIOperation) Cite() Citation {
	return Citation{
		SpecID: o.SpecID, Release: o.Release, Version: o.Version,
		Clause: o.Locator(), URL: o.ForgeURL,
	}
}

// APISchema is one components/schemas entry (an IE / data type) of a 5GC API.
type APISchema struct {
	SchemaID    uint64   `json:"-"`
	SpecID      string   `json:"spec_id"`
	Release     string   `json:"release"`
	Version     string   `json:"version"`
	Service     string   `json:"service"`
	SchemaName  string   `json:"schema_name"`
	Kind        string   `json:"kind"` // object|enum|string|array|...
	Description string   `json:"description"`
	Properties  []string `json:"properties"`
	EnumValues  []string `json:"enum_values"`
	RefsOut     []string `json:"refs_out"` // ['29.518:UeContextCreateData', ...]
	YAMLFile    string   `json:"yaml_file"`
	ForgeSHA    string   `json:"forge_sha"`
	ForgeURL    string   `json:"forge_url"`
}

// Cite builds the citation for this API schema.
func (s APISchema) Cite() Citation {
	return Citation{
		SpecID: s.SpecID, Release: s.Release, Version: s.Version,
		Clause: "API schema " + s.SchemaName, URL: s.ForgeURL,
	}
}
