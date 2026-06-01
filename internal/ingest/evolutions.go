package ingest

import (
	"github.com/kodflow/3gpp-mcp/internal/evolseed"
	"github.com/kodflow/3gpp-mcp/internal/model"
)

// seedEvolutions returns the curated NE<->NF evolution edges. The seed itself
// now lives in the CGO-free internal/evolseed package so cmd/merge can re-seed it
// authoritatively after a delta fold (PR-7); this wrapper keeps the ingest call
// sites unchanged.
func seedEvolutions() []model.Evolution { return evolseed.Seed() }
