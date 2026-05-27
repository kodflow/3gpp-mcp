package ingest

import "github.com/kodflow/3gpp-mcp/internal/model"

// seedEvolutions returns the canonical EPC(4G) -> 5GC(5G) network-element ->
// network-function evolution edges. This is the V1 relational stand-in for the
// V2 KuzuDB graph: a curated, cited seed (NE->NF is many-to-many, never 1:1 —
// CLAUDE.md §8.6). Justifications point at TS 23.501 (5G system architecture).
//
// It is a SEED, not corpus-mined; confidence reflects how clean the mapping is.
func seedEvolutions() []model.Evolution {
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
