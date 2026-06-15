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
			mcp.WithDescription("Lawful-Interception coverage from TS 33.128. With no 'nf': the full INVENTORY "+
				"of every NE/NF impacted over LI_X1/LI_X2/LI_X3 (core 5GC/EPC + all clause-7 services: NEF, "+
				"SCEF/IWK-SCEF, AKMA, LMF, EES, charging function, MCData/PTC/RCS/MMS, …), grouped by interface. "+
				"With 'nf': that NE/NF's events (xIRI over LI_X2 / xCC over LI_X3), scoped to the baseline release "+
				"with a later-release annex. Authoritative ASN.1 registry where present, else literal-prose mining."),
			mcp.WithString("nf", mcp.Description("NE/NF, e.g. AMF, SMF, UPF, NEF, SCEF, AAnF, LMF, MME, charging function. Omit (or '*'/'all') for the full X1/X2/X3 inventory.")),
			mcp.WithString("release", mcp.Description("override the baseline (e.g. Rel-18)")),
			mcp.WithString("interface", mcp.Description("with no 'nf', filter the inventory to one interface: LI_X1, LI_X2 or LI_X3")),
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
			Stable: model.IsStableVersion(version),
		},
	}, true
}

func liEvents(ctx context.Context, db *store.Store, baseline string, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	nf := strings.TrimSpace(r.GetString("nf", ""))
	avail, ordered, err := db.ClauseAvailability(ctx, specID, "6")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("li_events failed", err), nil
	}
	release := r.GetString("release", baseline)
	if release == "" && len(ordered) > 0 {
		release = ordered[len(ordered)-1]
	}

	// Inventory mode (no NF): list EVERY NE/NF impacted over X1/X2/X3, grouped by
	// interface — the comprehensive "who is touched" view across clauses 6+7.
	if nf == "" || nf == "*" || strings.EqualFold(nf, "all") {
		return liInventory(ctx, db, release, strings.TrimSpace(r.GetString("interface", "")))
	}

	if HasEvents(ctx, db, release) {
		if res, ok := liEventsAuthoritativeIfAny(ctx, db, release, nf, ordered); ok {
			return res, nil
		}
		// Authoritative registry has nothing for this NF (e.g. a clause-7 service NF
		// absent from the ASN.1 core) — fall through to the prose miner.
	}

	// Prose-miner fallback over the WHOLE spec (clauses 6+7) so NEF/SCEF/AKMA/… work.
	if version, _, _ := db.VersionForRelease(ctx, specID, release); version != "" {
		if clauses, cerr := db.GetClauses(ctx, specID, version, ""); cerr == nil {
			if evs := POIEventsForNF(clauses, nf); len(evs) > 0 {
				return jsonResult(map[string]any{
					"nf": nf, "spec_id": specID, "release": release, "version": version,
					"source": "33.128/prose", "count": len(evs), "events": evs,
					"note":      "Mined from the canonical POI prose (clause 6/7). xIRI→LI_X2, xCC→LI_X3; every POI is also tasked over LI_X1.",
					"citations": poiCitations(evs),
				})
			}
		}
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
			URL: model.ArchiveURL(specID, version), Stable: model.IsStableVersion(version),
		})
	}
	return jsonResult(map[string]any{
		"nf": view.NF, "spec_id": specID, "release": release, "version": version,
		"count": view.Count, "events": view.Events,
		"added_in_later_releases": view.AddedLater, "note": view.Note,
		"citations": cites,
	})
}

// liEventsAuthoritativeIfAny renders the ASN.1-registry events for nf. ok=false when
// the registry holds nothing for this NF (the caller then tries the prose miner).
func liEventsAuthoritativeIfAny(ctx context.Context, db *store.Store, release, nf string, ordered []string) (*mcp.CallToolResult, bool) {
	evs, err := GetEvents(ctx, db, nf, release, "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("li_events failed", err), true
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
		return nil, false // not in the ASN.1 core — let the caller try the prose miner
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
			URL: model.ArchiveURL(specID, version), Stable: model.IsStableVersion(version),
		})
	}
	res, _ := jsonResult(map[string]any{
		"nf": nf, "spec_id": specID, "release": release, "version": version,
		"module_version": moduleVersion, "source": "asn1",
		"count": len(evs), "by_interface": byIface, "events": evs,
		"added_in_later_releases": addedLater,
		"note":                    "Authoritative: parsed from TS 33.128 " + moduleVersion + " (TS33128Payloads.asn). Baseline = frozen expected state for " + release + ".",
		"citations":               cites,
	})
	return res, true
}

// liInventory lists every NE/NF impacted over X1/X2/X3, grouped by interface — the
// comprehensive footprint mined from the whole TS 33.128 (clauses 6+7). ifaceFilter
// (LI_X1/LI_X2/LI_X3, optional) narrows it to NFs touched by that interface.
func liInventory(ctx context.Context, db *store.Store, release, ifaceFilter string) (*mcp.CallToolResult, error) {
	version, _, _ := db.VersionForRelease(ctx, specID, release)
	if version == "" {
		return mcp.NewToolResultError("no indexed version of TS 33.128 for release '" + release + "'"), nil
	}
	clauses, err := db.GetClauses(ctx, specID, version, "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("li_events inventory failed", err), nil
	}
	inv := NFInventory(clauses)
	want := strings.ToUpper(strings.ReplaceAll(ifaceFilter, "LI", "LI_"))
	want = strings.ReplaceAll(want, "LI__", "LI_")
	byIface := map[string][]string{"LI_X1": {}, "LI_X2": {}, "LI_X3": {}}
	kept := make([]NFImpact, 0, len(inv))
	cites := make([]model.Citation, 0, len(inv))
	for _, im := range inv {
		if want != "" {
			match := false
			for _, x := range im.Interfaces {
				if x == want {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		kept = append(kept, im)
		cites = append(cites, im.Citation)
		for _, x := range im.Interfaces {
			byIface[x] = append(byIface[x], im.NF)
		}
	}
	if len(kept) == 0 {
		return mcp.NewToolResultError("no NE/NF impacted" + ifaceLabel(want) + " found in TS 33.128 " + release), nil
	}
	return jsonResult(map[string]any{
		"spec_id": specID, "release": release, "version": version,
		"mode": "inventory", "interface_filter": want,
		"count": len(kept), "by_interface": byIface, "nfs": kept,
		"note":      "Every NE/NF TS 33.128 impacts over LI_X1 (provisioning/tasking) / LI_X2 (xIRI) / LI_X3 (xCC), across the core 5GC/EPC NFs AND the clause-7 services. Pass nf=<name> for one NF's events.",
		"citations": cites,
	})
}

func ifaceLabel(iface string) string {
	if iface == "" {
		return ""
	}
	return " over " + iface
}

// poiCitations collects the distinct clause citations of a mined event list.
func poiCitations(evs []Event) []model.Citation {
	seen := map[string]bool{}
	out := make([]model.Citation, 0, len(evs))
	for _, e := range evs {
		if seen[e.Citation.Clause] {
			continue
		}
		seen[e.Citation.Clause] = true
		out = append(out, e.Citation)
	}
	return out
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
