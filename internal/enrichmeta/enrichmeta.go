// Package enrichmeta is the CGO-free source of truth for the GLOBAL enrichers'
// build identity (catalog overlay, 5GC OpenAPI ingest, evolutions seed).
//
// Why it exists, and why it is CGO-free: the global enrichers run as post-merge,
// corpus-GLOBAL passes — a DynaReport metadata correction, an OpenAPI extraction
// change, or an evolutions-seed edit moves NO spec version and NO subject
// footprint, so the spec-version delta and the subject delta are both blind to
// them (catalog-source-change-not-detected, openapi-not-in-any-footprint,
// evolutions-seed-never-propagates-in-delta). Folding their footprints into
// model.GlobalEnrichmentParts lets merge stamp + publish a global_enrichment
// identity and lets the CGO-free `discover` binary detect a drift and force the
// enricher refresh. Like subjectmeta, it imports no DuckDB/CGO code so discover
// can use it.
package enrichmeta

import (
	"github.com/kodflow/3gpp-mcp/internal/evolseed"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// CatalogVersion tags the DynaReport overlay extraction code (internal/catalog
// parse/apply). BUMP it whenever the overlay's parsing or the columns it writes
// (title / WG / TS-vs-TR / freeze_date attribution) change, so a code change to
// the catalog overlay shifts the global_enrichment identity even when the 3GPP
// site data is byte-identical. A pure 3GPP-side data change with no code bump is
// covered by CatalogSourceHash (the fetched-bytes digest threaded in by the
// caller); this constant only covers the CODE half.
const CatalogVersion = "catalog-v1"

// OpenAPIIngestVersion tags the 5GC OpenAPI extraction code (internal/openapi
// parse/ingest). BUMP it whenever the YAML parsing or the api_* rows it writes
// change shape. The per-release Forge SHA (the DATA half) is threaded in by the
// caller as OpenAPISourceHash; this constant covers the CODE half.
const OpenAPIIngestVersion = "openapi-v1"

// Current composes the current code's GlobalEnrichmentParts. The two source
// hashes (catalog HTML bytes, OpenAPI Forge SHAs) are DATA the offline enrichers
// resolve at run time; the CI matrix sizer (discover) compares the CODE halves it
// can compute statically, so they default to "" here and a caller that has the
// resolved data can fill them in. The evolutions seed hash IS statically
// derivable (the seed is in-repo) so it is always populated — that is what makes
// a seed edit visible to discover/merge without a manual bump.
func Current() model.GlobalEnrichmentParts {
	return model.GlobalEnrichmentParts{
		CatalogFootprint:     CatalogVersion,
		OpenAPIIngestVersion: OpenAPIIngestVersion,
		EvolutionsSeedHash:   evolseed.SeedHash(),
	}
}
