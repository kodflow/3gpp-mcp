// Package registry wires the set of enabled domain subjects. It is the single
// place that knows which verticals exist; the core (ingest, mcp) depends on this
// package, never on a concrete subject — so adding a domain means adding it here
// plus its package, with zero edits to the core seams.
package registry

import (
	"github.com/kodflow/3gpp-mcp/internal/subject"
	"github.com/kodflow/3gpp-mcp/internal/subject/glossary"
	"github.com/kodflow/3gpp-mcp/internal/subject/li"
)

// Default returns the registry of all built-in subjects.
func Default() *subject.Registry {
	return subject.New(
		li.New(),
		glossary.New(),
	)
}
