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

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// ToolRegistration is one MCP tool a subject contributes.
type ToolRegistration struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
}

// Subject is one domain vertical.
type Subject interface {
	// Name is the stable subject id ("li").
	Name() string
	// Activates reports whether this subject owns the given spec.
	Activates(specID string) bool
	// Tools returns the MCP tools this subject contributes (may be empty). Ingestion
	// moved to the Rust write-side (Phase 11b), so the Go Subject contract is read-only:
	// it declares spec ownership and contributes serve-time tools only.
	Tools(db *store.Store, baseline string) []ToolRegistration
}

// Purger is an optional capability: a subject that owns derived tables can clear
// the rows it wrote for one (spec, release) so a --resume redo starts from a
// clean slate. The generic PurgeSpecScope only knows the core tables (clauses,
// spec_versions, ingest_log); subject-owned tables (li_events, asn1_types, …) are
// invisible to it. Without this hook the resume "purge-then-redo-fresh" contract
// silently skips subject rows, so a corrected norm's rows can never overwrite a
// stale prior parse (ON CONFLICT DO NOTHING) and removed events linger forever.
type Purger interface {
	// Purge deletes every row this subject wrote for (specID, release). Called
	// from the resume redo path alongside the core PurgeSpecScope, only for
	// subjects whose Activates(specID) is true. version is provided for subjects
	// keyed by version rather than release.
	Purge(ctx context.Context, db *store.Store, specID, release, version string) error
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

// Purgers returns the subjects that both activate on specID and implement the
// Purger capability — i.e. the subjects whose derived rows a resume redo of
// specID must clear before re-ingesting.
func (r *Registry) Purgers(specID string) []Purger {
	var out []Purger
	for _, s := range r.subjects {
		if !s.Activates(specID) {
			continue
		}
		if p, ok := s.(Purger); ok {
			out = append(out, p)
		}
	}
	return out
}
