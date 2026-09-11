package goal

// anchor_derive.go gives the 3GPP corpus the delta anchor that describes IT.
//
// The anchor used to arrive two ways, and neither was tied to the corpus it sat
// next to. `seed` downloaded corpus-index.json from the `latest` GitHub release —
// written 2026-06-05, republished by nothing — beside a corpus pulled by digest
// from GHCR, so a fresh clone paired a snapshot of one generation with an anchor of
// another: measured against the corpus the next snapshot will be published from,
// 645 keys behind and 140 missing. Run through the real discover against the
// 2026-09-11 status report, a clone seeding the pinned snapshot got a work list of
// 805 (spec, release) pairs over 18 series with that anchor — and 20 pairs over 6
// series with the derived one, exactly this machine's own work list.
// And when no anchor was present, `ingest` meant to regenerate one with
// `merge --index-out --base <corpus>` and no shard — which merge refuses outright
// ("pass at least one shard path"), so that path failed the step every time it
// was reached.
//
// Both now derive it: cmd/derive-anchor reads the corpus's spec_versions and
// writes exactly what the fold would have written (see internal/anchor). No
// release asset, no second artefact to keep in step, nothing to publish.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kodflow/3gpp-mcp/internal/anchor"
)

// anchorPath is the 3GPP delta anchor discover reads.
func anchorPath(c *Ctx) string { return filepath.Join(c.Local, "corpus-index.json") }

// runDeriveAnchor runs cmd/derive-anchor. A variable so the steps that call it can
// be tested without building the binary; nothing in the shipped code rewrites it.
var runDeriveAnchor = func(c *Ctx, db, out string) error {
	_, err := c.Output(Cmd{Name: c.bin("derive-anchor"), Args: []string{"--db", db, "--out", out}})
	return err
}

// deriveAnchor makes .local/corpus-index.json the anchor of db, replacing whatever
// was there, and says how the previous one differed. It is the answer to "is the
// anchor of the same generation as the corpus?" — asked of the corpus itself.
//
// A previous anchor that disagrees is REPLACED, not trusted and not refused:
// the corpus on disk is the fact, and an anchor is only ever a summary of it. What
// the log must not do is replace it in silence, so the drift is named, and the
// dangerous direction (an anchor claiming versions the corpus lacks, which makes
// discover skip them) is called out as such.
func deriveAnchor(c *Ctx, db string) error {
	idx := anchorPath(c)
	if err := os.MkdirAll(filepath.Dir(idx), 0o755); err != nil {
		return err
	}
	tmp := idx + ".derived"
	if err := runDeriveAnchor(c, db, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("derive the delta anchor from %s: %w", db, err)
	}
	fresh, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	want, err := anchor.Parse(fresh)
	if err != nil || len(want) == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("derive-anchor wrote an unreadable or empty anchor (%v)", err)
	}
	c.Checkpoint("anchor_entries", fmt.Sprint(len(want)))

	old, rerr := os.ReadFile(idx)
	switch {
	case rerr != nil:
		c.Log.Printf("delta anchor derived from the corpus: %d (spec, release) entries", len(want))
	case string(old) == string(fresh):
		_ = os.Remove(tmp)
		c.Log.Printf("delta anchor already describes this corpus (%d entries)", len(want))
		return nil
	default:
		have, perr := anchor.Parse(old)
		if perr != nil {
			c.Log.Printf("the previous delta anchor did not parse (%v) — replaced by the one derived from the corpus", perr)
			break
		}
		d := anchor.Compare(have, want)
		c.Log.Printf("the previous delta anchor was NOT this corpus's: behind=%d missing=%d ahead=%d extra=%d — replaced",
			d.Behind, d.Missing, d.Ahead, d.Extra)
		if d.OverClaims() {
			c.Log.Printf("  it claimed %d key(s) the corpus does not hold: discover would have skipped them for good",
				d.Ahead+d.Extra)
		}
	}
	return os.Rename(tmp, idx)
}
