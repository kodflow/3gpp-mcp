package enrichmeta

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/evolseed"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestCurrentFoldsEvolutionsSeedHash asserts the global-enrichment parts the
// matrix sizer compares actually carry the content-derived evolutions seed hash,
// and that the resulting GlobalEnrichmentIdentity is sensitive to it. This is the
// guard that makes evolutions-seed-never-propagates-in-delta detectable by
// discover: a seed edit shifts SeedHash() → shifts the identity → forces refresh.
func TestCurrentFoldsEvolutionsSeedHash(t *testing.T) {
	cur := Current()
	if cur.EvolutionsSeedHash != evolseed.SeedHash() {
		t.Errorf("Current().EvolutionsSeedHash = %q, want %q", cur.EvolutionsSeedHash, evolseed.SeedHash())
	}
	if cur.CatalogFootprint == "" || cur.OpenAPIIngestVersion == "" {
		t.Errorf("catalog/openapi code halves must be populated: %+v", cur)
	}

	base := model.GlobalEnrichmentIdentity(cur)
	mutated := cur
	mutated.EvolutionsSeedHash = cur.EvolutionsSeedHash + "x"
	if model.GlobalEnrichmentIdentity(mutated) == base {
		t.Error("GlobalEnrichmentIdentity is blind to a seed-hash change")
	}
	mutated = cur
	mutated.CatalogFootprint = "catalog-vX"
	if model.GlobalEnrichmentIdentity(mutated) == base {
		t.Error("GlobalEnrichmentIdentity is blind to a catalog-version change")
	}
	mutated = cur
	mutated.OpenAPIIngestVersion = "openapi-vX"
	if model.GlobalEnrichmentIdentity(mutated) == base {
		t.Error("GlobalEnrichmentIdentity is blind to an openapi-version change")
	}
}
