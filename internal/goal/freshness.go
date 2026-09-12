package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// FRESHNESS IS A DETERMINANT, AND BOTH ARMS MUST HAVE ONE.
//
// An enumeration step asks upstream what exists. Nothing it depends on changes
// when upstream publishes, so its fingerprint cannot notice — which leaves two
// bad answers and one good one: pin it to its inputs and never see a new release
// again, re-run it every time and make the enumeration a clock that drags the
// whole write side behind it, or bucket the time since it last LOOKED and let the
// step's own output decide whether anything moved.
//
// The third is what this file is. `discover` had it; `discover-etsi` did not, and
// that asymmetry is not a detail: it means a new ETSI TS 103 221-1 version would
// have sat on etsi.org for ever, because the only step that could have seen it had
// an unchanging fingerprint. Nothing would have failed. The corpus would just have
// been quietly, permanently out of date — the exact defect shape arm_parity_test.go
// exists to catch, one level below the step list it reads.
//
// THE STAMP IS SEPARATE FROM THE CACHE, on purpose.
//
// discover's bucket used to be the age of status-report.htm, its own HTTP cache,
// and that only worked BY ACCIDENT: WriteAtomic leaves an unchanged file untouched,
// so the mtime would have stayed put and the bucket would have stayed expired,
// re-running discover on every single invocation. What kept it honest was that
// 3gpp.org re-nonces every response, so the file always changed — the very defect
// that made `enrich` replay every 6 h. Fixing that would have broken the bucket.
//
// A stamp written after every successful visit says what the bucket needs to know
// — when we last asked — without depending on whether the answer was new.
const discoverTTL = 6 * time.Hour

// visitStamp is where a step records that it went and looked. It is NOT an Output
// of the step: an output is what the step PRODUCED, and dependants replay when
// that moves. A stamp that moved on every visit would replay fetch and ingest on a
// clock — the failure this whole file is about, one edge further down.
func visitStamp(c *Ctx, step string) string {
	return c.statePath(filepath.Join("visits", step+".stamp"))
}

// visitBucket is the age of the last visit, in whole TTLs. Inside the window the
// value is stable and the step skips; past it the value moves and the step runs.
// "never" when the step has not looked yet, which is a bucket like any other —
// it differs from every number, so a first run happens.
func visitBucket(c *Ctx, step string) string {
	st, err := os.Stat(visitStamp(c, step))
	if err != nil {
		return "never"
	}
	return strconv.FormatInt(int64(time.Since(st.ModTime())/discoverTTL), 10)
}

// recordVisit stamps a successful enumeration. Call it at the END of Run: a
// crashed discover has not looked, and must not be credited with a visit it did
// not complete — the next run would then skip and the drift it was about to find
// would wait another six hours.
//
// The stamp CARRIES THE TIME as its content, so WriteAtomic (which leaves an
// unchanged file alone, by design) always writes: the mtime is the measurement,
// and a stamp that refused to move would freeze the bucket for ever.
func recordVisit(c *Ctx, step string) error {
	p := visitStamp(c, step)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return WriteAtomic(p, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"))
}

// freshnessKey is the determinant name both arms use. It is a constant because
// TestBothArmsEnumerateOnTheSameClock looks for it by name: two arms that spelled
// it differently would each have a bucket and still not be comparable.
const freshnessKey = "visit_bucket"

// withFreshness adds the visit bucket to a step's Extra map. Every enumeration
// step goes through here, which is what makes "both arms have one" a property of
// the code rather than of two people remembering.
func withFreshness(c *Ctx, step string, m map[string]string) (map[string]string, error) {
	if m == nil {
		m = map[string]string{}
	}
	if _, taken := m[freshnessKey]; taken {
		return nil, fmt.Errorf("step %s already sets %q", step, freshnessKey)
	}
	m[freshnessKey] = visitBucket(c, step)
	return m, nil
}
