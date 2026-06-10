package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, 0, 0, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, time.Millisecond, 2*time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("want nil after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 calls, got %d", calls)
	}
}

func TestDo_ExhaustsAndReturnsLastError(t *testing.T) {
	calls := 0
	sentinel := errors.New("boom-3")
	err := Do(context.Background(), 3, 0, 0, func() error {
		calls++
		return errors.New("boom-" + string(rune('0'+calls)))
	})
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
	if err == nil || err.Error() != sentinel.Error() {
		t.Fatalf("want last error %q, got %v", sentinel, err)
	}
}

func TestDo_AttemptsBelowOneRunsOnce(t *testing.T) {
	calls := 0
	_ = Do(context.Background(), 0, 0, 0, func() error {
		calls++
		return errors.New("x")
	})
	if calls != 1 {
		t.Fatalf("attempts<1 must run exactly once, got %d", calls)
	}
}

func TestDo_ContextCancelStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, 10, 50*time.Millisecond, 50*time.Millisecond, func() error {
		calls++
		cancel() // cancel during the first attempt; the backoff select must abort
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call before cancel, got %d", calls)
	}
}

func TestBackoff_CapsAtMax(t *testing.T) {
	base := 10 * time.Millisecond
	max := 25 * time.Millisecond
	// attempt 10 would be base*2^9 = 5120ms without the cap.
	d := backoff(10, base, max)
	if d < max || d > max+base {
		t.Fatalf("want capped to [max, max+base) = [%v,%v), got %v", max, max+base, d)
	}
}

func TestBackoff_ZeroBaseNoSleep(t *testing.T) {
	if d := backoff(3, 0, time.Second); d != 0 {
		t.Fatalf("base<=0 must yield 0 delay, got %v", d)
	}
}
