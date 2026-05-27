// Package catalog seeds authoritative 3GPP spec metadata from the public
// DynaReport HTML reports (axis #3), replacing filename-derived guesses:
//   - portal Releases.aspx  -> release calendar + freeze_date (ordering fix)
//   - dynareport status-report.htm -> per-spec title, TS/TR, real working group
//
// It is a metadata OVERLAY: it updates specs/spec_versions rows that content
// ingest already created from the FTP corpus; it never invents catalogue-only
// specs (cite-or-silent). HTML is parsed with golang.org/x/net/html — the
// dependency the project already uses, so no new dependency.
package catalog

import "time"

// ReleaseMeta is one row of portal Releases.aspx.
type ReleaseMeta struct {
	Code          string     // "Rel-19"
	Name          string     // "Release 19"
	Status        string     // "Open" | "Frozen" | "Closed"
	StartDate     *time.Time // release opening
	FreezeDate    *time.Time // "End date" functional freeze; nil while Open/planned
	FreezeMeeting string     // "SA#110"
}

// SpecMeta is the authoritative per-spec metadata from status-report.htm.
type SpecMeta struct {
	SpecID       string // "23.501"
	Series       string // "23"
	DocType      string // "TS" | "TR"
	Title        string
	WorkingGroup string // "S2", "C1", "S3", "R2" ...
}

// VersionRel is the latest version of a spec within a given release section.
type VersionRel struct {
	SpecID  string // "23.501"
	Release string // "Rel-19"
	Version string // "19.7.0"
}
