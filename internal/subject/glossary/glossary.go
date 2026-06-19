// Package glossary is the reference subject for the acronym glossary from TS 21.905
// (the 3GPP vocabulary). It owns spec id 21.905 and contributes no MCP tool of its own
// (the core resolve_term tool serves the acronyms table). Acronym EXTRACTION moved to the
// Rust write-side (parse3gpp::glossary, Phase 11b), so this Go subject is now read-only:
// it only declares ownership of 21.905 and registers no serve-time tool.
package glossary

import (
	"regexp"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
)

const specID = "21.905"

// reAbbrev matches a glossary line "ABBR<TAB|2+ spaces>Expansion". The TAB/multi-space
// separator keeps prose out. Retained as the canonical pattern the Rust extractor mirrors
// (parse3gpp::glossary) and the unit test pins; the Go ingest that used it is removed.
var reAbbrev = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._/-]{0,19})(?:\t+|\s{2,})(\S.{2,})$`)

// Subject is the glossary reference vertical.
type Subject struct{}

// New builds the glossary subject.
func New() *Subject { return &Subject{} }

func (*Subject) Name() string             { return "glossary" }
func (*Subject) Activates(id string) bool { return id == specID }

func (*Subject) Tools(*store.Store, string) []subject.ToolRegistration { return nil }

var _ subject.Subject = (*Subject)(nil)
