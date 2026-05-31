// Package li is the Lawful-Interception domain subject (TS 33.128). It owns the
// authoritative ASN.1 event registry + type catalogue, the clause-heuristic
// fallback, the li_events MCP tool, and the resolve_term ASN.1 enrichment —
// everything LI, plugged into the generic core via internal/subject.
package li

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject"
	"github.com/kodflow/3gpp-mcp/internal/subject/li/asn1"
)

// specID is the spec this subject owns.
const specID = "33.128"

// Subject is the LI vertical.
type Subject struct{}

// New builds the LI subject.
func New() *Subject { return &Subject{} }

func (*Subject) Name() string             { return "li" }
func (*Subject) Activates(id string) bool { return id == specID }

// Ingest parses the TS 33.128 ASN.1 attachment for this release and loads the
// authoritative registry + type catalogue. Degrade-don't-block: a missing
// attachment returns (0, nil) so ingestion never stalls on it.
func (*Subject) Ingest(ctx context.Context, db *store.Store, ic subject.IngestContext) (int, error) {
	rel := filepath.Base(filepath.Dir(ic.ConvertPath))
	base := strings.TrimSuffix(filepath.Base(ic.ConvertPath), filepath.Ext(ic.ConvertPath))
	zipPath := filepath.Join(ic.OriginDir, rel, base+".zip")

	m, err := asn1.ParseFromSpecZip(zipPath)
	if err != nil {
		return 0, nil // no ASN.1 registry — clause/prose fallback at query time
	}
	if err := InsertEvents(db, m.Release, m.ModuleVersion, m.Events); err != nil {
		return 0, err
	}
	if err := InsertFields(db, m.Release, m.Fields); err != nil {
		return 0, err
	}
	if err := InsertNFClauses(db, m.Release, m.NFClauses); err != nil {
		return 0, err
	}
	if err := InsertASN1Types(db, specID, m.Release, m.Types); err != nil {
		return 0, err
	}
	return len(m.Events), nil
}

// Purge clears every LI-owned row for (specID, release) so a --resume redo of
// TS 33.128 re-ingests from a clean slate (subject.Purger). version is unused:
// LI's tables are release-scoped.
func (s *Subject) Purge(ctx context.Context, db *store.Store, sid, release, _ string) error {
	if !s.Activates(sid) {
		return nil
	}
	return Purge(ctx, db, release)
}

// Tools contributes the li_events MCP tool.
func (*Subject) Tools(db *store.Store, baseline string) []subject.ToolRegistration {
	return []subject.ToolRegistration{{
		Tool: mcp.NewTool("li_events",
			mcp.WithDescription("Lawful-Interception events a NE/NF reports over LI_X2 to the MDF2 "+
				"(TS 33.128, events-only), scoped to the baseline release, with a later-release annex."),
			mcp.WithString("nf", mcp.Required(), mcp.Description("NE/NF, e.g. AMF, SMF, UPF, UDM, SMSF, MME, SGW, PGW")),
			mcp.WithString("release", mcp.Description("override the baseline (e.g. Rel-18)")),
		),
		Handler: func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return liEvents(ctx, db, baseline, r)
		},
	}}
}

// EnrichTerm attaches an ASN.1 definition + citation when the term names a type.
func (*Subject) EnrichTerm(ctx context.Context, db *store.Store, term, baseline string) (map[string]any, bool) {
	t, ok := GetASN1Type(ctx, db, term, baseline)
	if !ok {
		return nil, false
	}
	version, _, _ := db.VersionForRelease(ctx, t.SpecID, t.Release)
	return map[string]any{
		"asn1": t,
		"asn1_citation": model.Citation{
			SpecID: t.SpecID, Release: t.Release, Version: version,
			Clause: "ASN.1 type " + t.TypeName, URL: model.ArchiveURL(t.SpecID, version),
		},
	}, true
}

func liEvents(ctx context.Context, db *store.Store, baseline string, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	nf, err := r.RequireString("nf")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	avail, ordered, err := db.ClauseAvailability(ctx, specID, "6")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("li_events failed", err), nil
	}
	release := r.GetString("release", baseline)
	if release == "" && len(ordered) > 0 {
		release = ordered[len(ordered)-1]
	}
	if HasEvents(ctx, db, release) {
		return liEventsAuthoritative(ctx, db, release, nf, ordered)
	}

	view := NFEventCatalog(avail, ordered, release, nf)
	if view.Count == 0 && len(view.AddedLater) == 0 {
		return mcp.NewToolResultError("no LI events for NF '" + nf +
			"' in TS 33.128 (try AMF, SMF, UPF, UDM, SMSF, MME, SGW, PGW)"), nil
	}
	version, _, _ := db.VersionForRelease(ctx, specID, release)
	cites := make([]model.Citation, 0, len(view.Events))
	for _, e := range view.Events {
		cites = append(cites, model.Citation{
			SpecID: specID, Release: release, Version: version, Clause: e.Clause,
			URL: model.ArchiveURL(specID, version),
		})
	}
	return jsonResult(map[string]any{
		"nf": view.NF, "spec_id": specID, "release": release, "version": version,
		"count": view.Count, "events": view.Events,
		"added_in_later_releases": view.AddedLater, "note": view.Note,
		"citations": cites,
	})
}

func liEventsAuthoritative(ctx context.Context, db *store.Store, release, nf string, ordered []string) (*mcp.CallToolResult, error) {
	evs, err := GetEvents(ctx, db, nf, release, "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("li_events failed", err), nil
	}
	baseNames := map[string]bool{}
	for _, e := range evs {
		baseNames[e.Interface+"/"+e.EventName] = true
	}
	seen := map[string]bool{}
	addedLater := map[string][]string{}
	past := false
	for _, rel := range ordered {
		if rel == release {
			past = true
			continue
		}
		if !past || !HasEvents(ctx, db, rel) {
			continue
		}
		later, _ := GetEvents(ctx, db, nf, rel, "")
		for _, e := range later {
			k := e.Interface + "/" + e.EventName
			if baseNames[k] || seen[k] {
				continue
			}
			seen[k] = true
			addedLater[rel] = append(addedLater[rel], e.Interface+":"+e.EventName)
		}
	}
	if len(evs) == 0 && len(addedLater) == 0 {
		return mcp.NewToolResultError("no LI events for NF '" + nf +
			"' in TS 33.128 " + release + " (try AMF, SMF, UPF, UDM, NEF, MME, SGW)"), nil
	}
	byIface := map[string]int{}
	clauseSet := map[string]bool{}
	moduleVersion := ""
	for _, e := range evs {
		byIface[e.Interface]++
		if e.Clause != "" {
			clauseSet[e.Clause] = true
		}
		if moduleVersion == "" {
			moduleVersion = e.ModuleVersion
		}
	}
	version, _, _ := db.VersionForRelease(ctx, specID, release)
	cites := make([]model.Citation, 0, len(clauseSet))
	for c := range clauseSet {
		cites = append(cites, model.Citation{
			SpecID: specID, Release: release, Version: version, Clause: c,
			URL: model.ArchiveURL(specID, version),
		})
	}
	return jsonResult(map[string]any{
		"nf": nf, "spec_id": specID, "release": release, "version": version,
		"module_version": moduleVersion, "source": "asn1",
		"count": len(evs), "by_interface": byIface, "events": evs,
		"added_in_later_releases": addedLater,
		"note":                    "Authoritative: parsed from TS 33.128 " + moduleVersion + " (TS33128Payloads.asn). Baseline = frozen expected state for " + release + ".",
		"citations":               cites,
	})
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("marshal", err), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// compile-time interface checks.
var (
	_ subject.Subject      = (*Subject)(nil)
	_ subject.TermEnricher = (*Subject)(nil)
	_ subject.Purger       = (*Subject)(nil)
	_                      = server.ToolHandlerFunc(nil)
)
