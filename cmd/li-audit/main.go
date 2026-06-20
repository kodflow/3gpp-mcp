// Command li-audit cross-checks an external LI event catalogue (the Sentinel R17
// oracle) against the indexed 3GPP normative text. Each event is verified in its
// cited spec and, when absent, RELOCATED to its true spec/clause anywhere in the
// index (e.g. RESTORE_DATA -> TS 29.002 §8.10.3). Writes a markdown report.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subject/li"
)

func main() {
	db := flag.String("db", "data/3gpp.duckdb", "DuckDB snapshot path")
	oracle := flag.String("oracle", "docs/inputs/sentinel_r17_events.json", "external event catalogue (JSON)")
	out := flag.String("out", "docs/generated/li_audit.md", "markdown report output")
	flag.Parse()

	ctx := context.Background()
	st, err := store.OpenReadOnly(*db) // li-audit only reads (AuditCatalog/SearchClauses)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()
	_ = st.LoadFTS(ctx) // faster cross-spec search when the BM25 index is present

	evs, err := li.LoadSentinel(*oracle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oracle:", err)
		os.Exit(1)
	}
	fs, err := li.AuditCatalog(ctx, st, evs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit:", err)
		os.Exit(1)
	}

	keys, counts := li.Summary(fs)
	var md strings.Builder
	md.WriteString("# LI event audit — external catalogue vs indexed 3GPP normative text\n\n")
	fmt.Fprintf(&md, "Oracle: `%s` (%d events). Each event verified in its cited spec, then relocated cross-spec when absent.\n\n## Summary\n\n", *oracle, len(evs))
	for _, k := range keys {
		fmt.Fprintf(&md, "- **%s**: %d\n", k, counts[k])
	}

	// Actionable sections: relocated + not-found.
	md.WriteString("\n## WRONG_SPEC_REF — relocated to their true normative home\n\n")
	md.WriteString("| NF | Event | Cited (wrong) | Real spec | Real clause | Heading |\n|---|---|---|---|---|---|\n")
	for _, f := range fs {
		if f.Verdict == li.VWrongSpec {
			fmt.Fprintf(&md, "| %s | %s | %s §%s | **%s** | **§%s** | %s |\n",
				f.NF, f.Event, f.CitedSpec, f.CitedClause, f.RealSpec, f.RealClause, f.RealHeading)
		}
	}
	md.WriteString("\n## NOT_FOUND — no trace anywhere (candidate hallucination)\n\n")
	for _, f := range fs {
		if f.Verdict == li.VNotFound {
			fmt.Fprintf(&md, "- %s / %s (cited %s §%s)\n", f.NF, f.Event, f.CitedSpec, f.CitedClause)
		}
	}
	md.WriteString("\n## Full per-event table\n\n| NF | Event | Alias | Cited | Verdict | Real ref |\n|---|---|---|---|---|---|\n")
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].NF != fs[j].NF {
			return fs[i].NF < fs[j].NF
		}
		return fs[i].Event < fs[j].Event
	})
	for _, f := range fs {
		real := ""
		if f.Verdict == li.VWrongSpec {
			real = f.RealSpec + " §" + f.RealClause
		}
		al := ""
		if f.Alias {
			al = "alias"
		}
		fmt.Fprintf(&md, "| %s | %s | %s | %s §%s | %s | %s |\n", f.NF, f.Event, al, f.CitedSpec, f.CitedClause, f.Verdict, real)
	}

	if err := os.WriteFile(*out, []byte(md.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("audited %d events -> %s\n", len(fs), *out)
	for _, k := range keys {
		fmt.Printf("  %-20s %d\n", k, counts[k])
	}
	fmt.Println("relocated (WRONG_SPEC_REF):")
	for _, f := range fs {
		if f.Verdict == li.VWrongSpec {
			fmt.Printf("  %s/%s : %s §%s -> %s §%s (%s)\n", f.NF, f.Event, f.CitedSpec, f.CitedClause, f.RealSpec, f.RealClause, f.RealHeading)
		}
	}
}
