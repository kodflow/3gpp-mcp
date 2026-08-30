// Package evolseed holds the curated EPC(4G) -> 5GC(5G) network-element ->
// network-function evolution seed and its content digest.
//
// It is deliberately CGO-FREE (it imports only internal/model) so that BOTH the
// ingest pipeline (CGO, via the DuckDB store) AND cmd/merge can call it. Before
// PR-7 the seed lived inside internal/ingest, which transitively pulls in the
// CGO store, so merge could not re-seed evolutions authoritatively and had to
// rely on folding the table from shard #0 — and on a delta that shard is the
// BASE, whose stale seed then won against the fresh shards (the
// evolutions-seed-edit-never-lands-in-delta-merge bug). Hosting the seed here
// lets merge truncate+reseed from the CURRENT code regardless of fold order, and
// lets the change-detection plane hash the seed CGO-free.
package evolseed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// Seed returns the canonical EPC(4G) -> 5GC(5G) network-element ->
// network-function evolution edges. This is the V1 relational stand-in for the
// V2 KuzuDB graph: a curated, cited seed (NE->NF is many-to-many, never 1:1 —
// CLAUDE.md §8.6). Justifications point at TS 23.501 (5G system architecture).
//
// It is a SEED, not corpus-mined; confidence reflects how clean the mapping is.
func Seed() []model.Evolution {
	const arch = "23.501"
	e := func(from, to, typ, clause string, conf float64) model.Evolution {
		return model.Evolution{
			FromTerm: from, ToTerm: to, EvolutionType: typ,
			JustificationSpec: arch, JustificationClause: clause, Confidence: conf,
		}
	}
	return []model.Evolution{
		// ---- EPC / legacy PS core -> 5GC -------------------------------------
		// Each edge is anchored at the TARGET's own definition clause in 23.501
		// §6.2 (the NF catalogue), because that is the clause a reader following
		// the citation actually needs. The earlier seed pointed several of these
		// at §4.2.x architecture clauses that do not describe the target at all —
		// PCRF->PCF cited §4.2.5 "Data Storage architectures", eNB->gNB cited
		// §4.2.6 "Service-based interfaces". A citation that lands on the wrong
		// clause is worse than none: it looks checkable and is not.
		//
		// MME splits into AMF (mobility), SMF (session) and SMSF (SMS over NAS).
		e("MME", "AMF", "SPLIT", "6.2.1", 0.90),
		e("MME", "SMF", "SPLIT", "6.2.2", 0.75),
		e("MME", "SMSF", "SPLIT", "6.2.13", 0.60),
		// Serving/PDN gateways -> UPF (user plane) and SMF (control).
		e("SGW", "UPF", "REPLACED_BY", "6.2.3", 0.80),
		e("PGW", "UPF", "SPLIT", "6.2.3", 0.80),
		e("PGW", "SMF", "SPLIT", "6.2.2", 0.80),
		e("PGW-C", "SMF", "RENAME", "6.2.2", 0.85),
		e("PGW-U", "UPF", "RENAME", "6.2.3", 0.85),
		e("TDF", "UPF", "REPLACED_BY", "6.2.3", 0.60),
		// 2G/3G packet core.
		e("GGSN", "UPF", "SPLIT", "6.2.3", 0.60),
		e("GGSN", "SMF", "SPLIT", "6.2.2", 0.60),
		e("SGSN", "AMF", "REPLACED_BY", "6.2.1", 0.55),
		// Subscriber data and authentication: HSS fans out to UDM + UDR + AUSF.
		e("HSS", "UDM", "REPLACED_BY", "6.2.7", 0.80),
		e("HSS", "UDR", "SPLIT", "6.2.11", 0.65),
		e("HSS", "AUSF", "SPLIT", "6.2.8", 0.65),
		e("HSS-FE", "UDM", "RENAME", "6.2.7", 0.70),
		e("SPR", "UDR", "REPLACED_BY", "6.2.11", 0.65),
		e("AAA", "AUSF", "REPLACED_BY", "6.2.8", 0.55),
		e("EIR", "5G-EIR", "RENAME", "6.2.15", 0.85),
		// Policy and exposure.
		e("PCRF", "PCF", "RENAME", "6.2.4", 0.90),
		e("SCEF", "NEF", "REPLACED_BY", "6.2.5", 0.80),
		e("DRA", "SCP", "REPLACED_BY", "6.2.19", 0.50),
		// Location.
		e("E-SMLC", "LMF", "REPLACED_BY", "6.2.16", 0.75),
		// Access. gNB is a RAN node, not an NF of §6.2, and 23.501 only NAMES it
		// (§3.2 Abbreviations) — the NG-RAN node itself is described in 38.300 /
		// 38.401. §3.2 is the honest anchor inside the spec this seed cites: it is
		// where 23.501 introduces the term, and nothing in §4.2.2 mentions gNB at
		// all (the citation check catches that).
		e("eNB", "gNB", "RENAME", "3.2", 0.85),
		e("ePDG", "N3IWF", "REPLACED_BY", "6.2.9", 0.80),
		e("TWAG", "TWIF", "REPLACED_BY", "6.2.22", 0.60),
		// Multicast / broadcast.
		e("MBMS-GW", "MB-SMF", "REPLACED_BY", "6.2.27", 0.55),

		// ---- 5GC functions with no direct 4G predecessor ----------------------
		// FromTerm is empty on purpose: these are additions, not evolutions of an
		// existing element, and pretending otherwise would invent a lineage.
		e("", "NRF", "EXTENDED_BY", "6.2.6", 0.70),
		e("", "NSSF", "EXTENDED_BY", "6.2.14", 0.70),
		e("", "UDSF", "EXTENDED_BY", "6.2.12", 0.70),
		e("", "SEPP", "EXTENDED_BY", "6.2.17", 0.70),
		e("", "NWDAF", "EXTENDED_BY", "6.2.18", 0.70),
		e("", "W-AGF", "EXTENDED_BY", "6.2.20", 0.70),
		e("", "UCMF", "EXTENDED_BY", "6.2.21", 0.70),
		e("", "NSSAAF", "EXTENDED_BY", "6.2.23", 0.70),
		e("", "DCCF", "EXTENDED_BY", "6.2.24", 0.70),
		e("", "MFAF", "EXTENDED_BY", "6.2.25", 0.70),
		e("", "ADRF", "EXTENDED_BY", "6.2.26", 0.70),
		e("", "NSACF", "EXTENDED_BY", "6.2.28", 0.70),
		e("", "TSCTSF", "EXTENDED_BY", "6.2.29", 0.70),
		e("", "5G DDNMF", "EXTENDED_BY", "6.2.30", 0.70),
		e("", "EASDF", "EXTENDED_BY", "6.2.31", 0.70),
		e("", "TSN AF", "EXTENDED_BY", "6.2.32", 0.70),
		e("", "NSWOF", "EXTENDED_BY", "6.2.33", 0.70),
		e("", "EIF", "EXTENDED_BY", "6.2.34", 0.70),
	}
}

// SeedHash is the content-derived digest of Seed() — the short sha256 over the
// sorted (from,to,type,spec,clause,confidence) tuples. It is folded into
// model.GlobalEnrichmentParts.EvolutionsSeedHash so that ANY edit to the seed
// (added/removed edge, corrected confidence or justification) shifts the
// published global_enrichment_identity, letting discover force the enricher
// refresh even though no spec version moved. Content-derived, not a hand-bumped
// constant, so a maintainer can never forget to bump it (the seed IS the input).
func SeedHash() string {
	evos := Seed()
	tuples := make([]string, 0, len(evos))
	for _, e := range evos {
		// Fixed-precision confidence so a float formatting quirk can't perturb the
		// digest; the seed only ever uses 2-decimal confidences.
		tuples = append(tuples, fmt.Sprintf("%s|%s|%s|%s|%s|%.4f",
			e.FromTerm, e.ToTerm, e.EvolutionType,
			e.JustificationSpec, e.JustificationClause, e.Confidence))
	}
	sort.Strings(tuples) // order-independent: the seed is a SET of edges
	h := sha256.Sum256([]byte(strings.Join(tuples, "\n")))
	return hex.EncodeToString(h[:])[:12]
}
