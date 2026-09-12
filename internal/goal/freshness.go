package goal

import (
	"fmt"
	"strconv"
	"time"
)

// FRESHNESS IS A DETERMINANT, AND BOTH ARMS MUST HAVE ONE.
//
// An enumeration step asks upstream what exists. Nothing it depends on changes
// when upstream publishes, so its fingerprint cannot notice — which leaves two
// bad answers and one good one: pin it to its inputs and never see a new release
// again, re-run it every time and make the enumeration a clock that drags the
// whole write side behind it, or quantise time and let the step's own OUTPUT
// decide whether anything moved.
//
// The third is what this file is. `discover` had it; `discover-etsi` did not, and
// that asymmetry is not a detail: it means a new ETSI TS 103 221-1 version would
// have sat on etsi.org for ever, because the only step that could have seen it had
// an unchanging fingerprint. Nothing would have failed. The corpus would just have
// been quietly, permanently out of date — the exact defect shape arm_parity_test.go
// exists to catch, one level below the step list it reads.
const discoverTTL = 6 * time.Hour

// freshnessKey is the determinant name both arms use. It is a constant because
// TestBothArmsEnumerateOnTheSameClock looks for it by name: two arms that spelled
// it differently would each have a bucket and still not be comparable.
const freshnessKey = "visit_window"

// visitWindow is the current TTL-wide window of the clock: floor(now / TTL). Every
// invocation inside one window reads the SAME value, so a step that ran in this
// window skips; the first invocation of the next window reads a different one and
// the step runs.
//
// IT IS THE WINDOW, NOT THE AGE OF A STAMP, and the difference is the whole
// correctness of the thing.
//
// The obvious design — stamp the successful visit, bucket `now - stamp` — has a
// defect that is invisible until you follow the fingerprint through the ledger.
// The runner computes a step's fingerprint BEFORE the Run and records THAT with
// the success. So the recorded fingerprint holds the age read before the visit
// ("never", or the expired "1"), while the next invocation reads the age after it
// ("0"). Two different values, so the step runs AGAIN, immediately, inside the
// window it just refreshed — the enumeration happening twice per TTL instead of
// once, found by Qodo on PR #352.
//
// A window has no such gap: it is a pure function of the clock, the same before
// and after the Run, so what the ledger records is exactly what the next plan
// computes. It also needs no file, writes nothing, and cannot credit a crashed
// enumeration with a visit it did not make — a failed step leaves no success
// record, so it replays regardless of what any stamp would have said.
//
// The cost of quantising absolutely rather than relatively: two builds either side
// of a window boundary both enumerate. That is `discover` (~3 s) and
// `discover-etsi` (~40 s), bounded, and their OutputsComplete keeps an unchanged
// work list from replaying anything behind them.
//
// discover's ORIGINAL bucket was the age of its own HTTP cache, and it only worked
// BY ACCIDENT: WriteAtomic leaves an unchanged file untouched, so the mtime would
// have stayed put, the bucket stayed expired, and the step re-run on every single
// invocation. What kept it honest was that 3gpp.org re-nonces every response, so
// the file always changed — the very defect that made `enrich` replay every 6 h.
// Fixing that would have broken the clock.
func visitWindow(now time.Time) string {
	return strconv.FormatInt(now.UTC().Unix()/int64(discoverTTL.Seconds()), 10)
}

// withFreshness adds the visit window to a step's Extra map. Every enumeration
// step goes through here, which is what makes "both arms have one" a property of
// the code rather than of two people remembering.
func withFreshness(step string, m map[string]string) (map[string]string, error) {
	if m == nil {
		m = map[string]string{}
	}
	if _, taken := m[freshnessKey]; taken {
		return nil, fmt.Errorf("step %s already sets %q", step, freshnessKey)
	}
	m[freshnessKey] = visitWindow(time.Now())
	return m, nil
}
