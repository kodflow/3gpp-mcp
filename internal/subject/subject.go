// Package subject defines the domain-vertical plugin contract. The generic
// retrieval core (ingest, MCP tools, store schema, search) stays
// domain-agnostic; each business vertical — Lawful Interception today, charging
// / roaming / security tomorrow — is a self-contained Subject that registers
// here. The core iterates the registry instead of hardcoding spec ids or tools,
// so adding a domain is adding a package, not editing the core.
//
// Import rule (no cycles): this package imports only store, model, and the
// mcp-go types. Concrete subjects import THIS package; the wiring that lists all
// subjects lives in internal/registry (which imports the subjects), and the core
// (ingest/mcp) imports internal/registry — never a subject directly.
package subject

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// IngestContext carries everything a subject needs to extract its structured
// artefacts from one parsed spec during the offline ingest pass.
type IngestContext struct {
	SpecID      string
	Release     string
	Version     string
	ConvertPath string // path of the converted file being ingested (to locate siblings)
	OriginDir   string // data/sources/origin (for attachment zips)
	Clauses     []model.Clause
	IsLatest    bool // this (spec, version) is the newest indexed version of the spec
}

// ToolRegistration is one MCP tool a subject contributes.
type ToolRegistration struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
}

// Subject is one domain vertical.
type Subject interface {
	// Name is the stable subject id ("li").
	Name() string
	// Activates reports whether this subject ingests the given spec.
	Activates(specID string) bool
	// Ingest extracts the subject's structured data for one spec into the store.
	// Returns the number of records added (degrade-don't-block: a missing source
	// returns 0, nil). Called only when Activates(ic.SpecID) is true.
	Ingest(ctx context.Context, db *store.Store, ic IngestContext) (added int, err error)
	// Tools returns the MCP tools this subject contributes (may be empty).
	Tools(db *store.Store, baseline string) []ToolRegistration
}

// TermEnricher is an optional capability: a subject can enrich the core
// resolve_term tool when a term belongs to its domain (e.g. an ASN.1 type).
type TermEnricher interface {
	// EnrichTerm returns extra response fields (merged into resolve_term's output)
	// and ok=true when the term is recognised by this subject.
	EnrichTerm(ctx context.Context, db *store.Store, term, baseline string) (map[string]any, bool)
}

// Registry holds the enabled subjects.
type Registry struct{ subjects []Subject }

// New builds a registry from the given subjects.
func New(ss ...Subject) *Registry { return &Registry{subjects: ss} }

// Active returns the subjects that ingest specID.
func (r *Registry) Active(specID string) []Subject {
	var out []Subject
	for _, s := range r.subjects {
		if s.Activates(specID) {
			out = append(out, s)
		}
	}
	return out
}

// All returns every registered subject.
func (r *Registry) All() []Subject { return r.subjects }

// TermEnrichers returns the subjects that implement TermEnricher.
func (r *Registry) TermEnrichers() []TermEnricher {
	var out []TermEnricher
	for _, s := range r.subjects {
		if te, ok := s.(TermEnricher); ok {
			out = append(out, te)
		}
	}
	return out
}
