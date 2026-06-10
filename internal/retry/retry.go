// Package retry is the single backoff policy for every flaky network call in
// the server and the offline tooling (discover's 3GPP status-report fetch, the
// bootstrap DB/model/ORT/vector downloads from GitHub and GHCR). It mirrors the
// shell-side scripts/lib/retry.sh so Go and CI share one behaviour: a transient
// failure (connection reset, 5xx, CDN drop, GHCR throttle) is retried with
// exponential backoff + jitter; a genuinely broken call still surfaces its last
// error once the attempts are spent. Operations wrapped here MUST be idempotent.
package retry

import (
	"context"
	"math/rand"
	"time"
)

// Do calls fn up to attempts times (attempts < 1 is treated as 1). On a non-nil
// error it sleeps for min(max, base*2^(n-1)) + jitter[0,base) and retries, until
// fn succeeds, the attempts are exhausted, or ctx is cancelled. It returns nil on
// success, ctx.Err() if the context ends during a backoff, or fn's last error.
//
// base<=0 disables the sleep (back-to-back retries); pass a real base in
// production so a hammered endpoint gets breathing room. The jitter desynchronises
// parallel callers (e.g. matrix shards) so they don't retry in lockstep.
func Do(ctx context.Context, attempts int, base, max time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for n := 1; ; n++ {
		if err = fn(); err == nil {
			return nil
		}
		if n >= attempts {
			return err
		}
		d := backoff(n, base, max)
		if d <= 0 {
			// No sleep requested; still honour a cancelled context.
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if ctx == nil {
			time.Sleep(d)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// backoff returns the delay before attempt n+1: base*2^(n-1) capped at max, plus
// jitter in [0,base). A left shift that overflows time.Duration (very large n)
// folds to the cap rather than a negative sleep.
func backoff(n int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	d := base << (n - 1)
	if d <= 0 || d > max {
		d = max
	}
	if base > 0 {
		d += time.Duration(rand.Int63n(int64(base)))
	}
	return d
}
