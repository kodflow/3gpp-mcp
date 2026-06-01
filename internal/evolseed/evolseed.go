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
		// MME splits into AMF (mobility) + SMF (session) + parts.
		e("MME", "AMF", "SPLIT", "4.2.2", 0.90),
		e("MME", "SMF", "SPLIT", "4.2.2", 0.75),
		// Serving/PDN gateways -> UPF (user plane) and SMF (control).
		e("SGW", "UPF", "REPLACED_BY", "4.2.3", 0.80),
		e("PGW", "UPF", "SPLIT", "4.2.3", 0.80),
		e("PGW", "SMF", "SPLIT", "4.2.3", 0.80),
		e("PGW-C", "SMF", "RENAME", "4.2.3", 0.85),
		e("PGW-U", "UPF", "RENAME", "4.2.3", 0.85),
		// Subscriber data: HSS -> UDM + UDR + AUSF.
		e("HSS", "UDM", "REPLACED_BY", "4.2.4", 0.80),
		e("HSS", "UDR", "SPLIT", "4.2.4", 0.65),
		e("HSS", "AUSF", "SPLIT", "4.2.4", 0.65),
		// Policy: PCRF -> PCF.
		e("PCRF", "PCF", "RENAME", "4.2.5", 0.90),
		// RAN: eNB -> gNB; ePDG -> N3IWF; SGSN legacy -> AMF.
		e("eNB", "gNB", "RENAME", "4.2.6", 0.85),
		e("ePDG", "N3IWF", "REPLACED_BY", "4.2.8", 0.80),
		e("SGSN", "AMF", "REPLACED_BY", "4.2.2", 0.55),
		// New 5GC functions with no direct 4G predecessor.
		e("", "NRF", "EXTENDED_BY", "6.2.6", 0.70),
		e("", "NSSF", "EXTENDED_BY", "6.2.14", 0.70),
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
