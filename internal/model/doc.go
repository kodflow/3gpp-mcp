// Package model defines the domain types backing every MCP response.
//
// Each type maps to a DuckDB table declared in CLAUDE.md §4:
//
//	Spec        ↔ specs
//	Version     ↔ spec_versions
//	Clause      ↔ clauses
//	Change      ↔ changes
//	Acronym     ↔ acronyms
//	Evolution   ↔ evolutions
//
// Every MCP tool response carries citations of the form
// {spec_id, release, version, clause, url}. A type that cannot produce a
// citation must not be returned by a tool (CLAUDE.md §1).
package model
