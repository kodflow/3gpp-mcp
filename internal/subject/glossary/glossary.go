// Package glossary is the reference subject that seeds the acronym glossary from
// TS 21.905 (the 3GPP vocabulary). It populates the generic `acronyms` table
// (served by the core resolve_term tool) and contributes no MCP tool of its own.
// It exists as a subject so the ingest core carries no hardcoded spec id.
package glossary

import (
	"context"
	"regexp"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
)

const specID = "21.905"

// reAbbrev matches a glossary line "ABBR<TAB|2+ spaces>Expansion". The
// TAB/multi-space separator keeps prose ("This document defines …") out.
var reAbbrev = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._/-]{0,19})(?:\t+|\s{2,})(\S.{2,})$`)

// reNumericClause matches a clause path opening a new numbered section.
var reNumericClause = regexp.MustCompile(`^[0-9]`)

// Subject is the glossary reference vertical.
type Subject struct{}

// New builds the glossary subject.
func New() *Subject { return &Subject{} }

func (*Subject) Name() string             { return "glossary" }
func (*Subject) Activates(id string) bool { return id == specID }

func (*Subject) Tools(*store.Store, string) []subject.ToolRegistration { return nil }

// Ingest extracts acronyms from TS 21.905's abbreviations region into the
// generic acronyms table. Only the latest version is seeded (older versions
// duplicate the vocabulary).
func (*Subject) Ingest(ctx context.Context, db *store.Store, ic subject.IngestContext) (int, error) {
	if !ic.IsLatest {
		return 0, nil
	}
	start := -1
	for i, c := range ic.Clauses {
		if strings.Contains(strings.ToLower(c.Heading), "abbreviation") {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, nil
	}
	n := 0
	for i := start; i < len(ic.Clauses); i++ {
		c := ic.Clauses[i]
		if i > start && (reNumericClause.MatchString(c.ClausePath) || strings.HasPrefix(c.ClausePath, "Annex")) {
			break // left the abbreviations region
		}
		for _, line := range strings.Split(c.Text, "\n") {
			m := reAbbrev.FindStringSubmatch(strings.TrimRight(line, " \t"))
			if m == nil {
				continue
			}
			term, exp := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
			exp = strings.ReplaceAll(exp, "\t", " ")
			if len(exp) < 4 || strings.EqualFold(term, exp) || isAllDigits(exp) {
				continue
			}
			if err := db.UpsertAcronym(model.Acronym{
				Term: term, Expansion: exp,
				FirstRelease: ic.Release, LastRelease: ic.Release,
			}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

var _ subject.Subject = (*Subject)(nil)
