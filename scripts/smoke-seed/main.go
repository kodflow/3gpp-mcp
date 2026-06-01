// Command smoke-seed writes a tiny, deterministic DuckDB snapshot used by
// scripts/embed-local-smoke.sh to exercise the embed pipeline end-to-end WITHOUT
// the 3GPP corpus, a network fetch, or a GPU. It seeds a handful of clauses whose
// chunk_id order is deliberately the REVERSE of release recency, so the smoke can
// prove recent-first ordering. Offline tooling only — never shipped.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: smoke-seed <db-path>")
		os.Exit(2)
	}
	if err := seed(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "smoke-seed: %v\n", err)
		os.Exit(1)
	}
}

func seed(path string) error {
	_ = os.Remove(path)
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(context.Background()); err != nil {
		return err
	}
	// chunk_id 1..6 ascends; release recency descends — so a recent-first scan must
	// reorder them (Rel-20 first, Rel-99 last).
	rows := []struct {
		id        uint64
		spec, rel string
		ver, path string
		heading   string
		text      string
	}{
		{1, "23.501", "Rel-99", "3.12.0", "1", "GPRS", "old gsm umts core clause"},
		{2, "23.501", "Rel-20", "20.0.0", "5.2", "AMF", "access and mobility management function in 5gc"},
		{3, "33.128", "Rel-18", "18.5.0", "6.2", "LI", "lawful interception at the amf over li_x2"},
		{4, "24.501", "Rel-19", "19.6.0", "4.1", "NAS", "non access stratum protocol for 5g"},
		{5, "29.518", "Rel-20", "20.0.0", "5.1", "Namf", "namf communication service operations"},
		{6, "23.502", "Rel-17", "17.9.0", "4.2", "PDU", "pdu session establishment procedure"},
	}
	for _, r := range rows {
		if err := st.UpsertSpec(model.Spec{SpecID: r.spec, Series: r.spec[:2], DocType: "TS"}); err != nil {
			return err
		}
		if err := st.UpsertVersion(model.SpecVersion{SpecID: r.spec, Release: r.rel, Version: r.ver}); err != nil {
			return err
		}
		if err := st.InsertClauses([]model.Clause{{
			ChunkID: r.id, SpecID: r.spec, Release: r.rel, Version: r.ver,
			ClausePath: r.path, Heading: r.heading, Text: r.text,
		}}); err != nil {
			return err
		}
	}
	fmt.Printf("seeded %d clauses into %s\n", len(rows), path)
	return nil
}
