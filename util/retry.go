package util

import (
	"context"
	"math/rand"
	"time"
)

// WaitForRetry blocks for the given duration and exits early when context is canceled.
func WaitForRetry(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// JitterRetryDelay applies +/-20% jitter to the base delay.
func JitterRetryDelay(base time.Duration, rng *rand.Rand) time.Duration {
	if base <= 0 || rng == nil {
		return base
	}
	maxJitter := base / 5
	if maxJitter <= 0 {
		return base
	}
	delta := time.Duration(rng.Int63n(int64(maxJitter)*2+1)) - maxJitter
	return base + delta
}

// NextRetryDelay doubles the current delay and caps it at max.
func NextRetryDelay(current, min, max time.Duration) time.Duration {
	if min <= 0 {
		min = time.Millisecond
	}
	if max < min {
		max = min
	}
	if current <= 0 {
		return min
	}
	next := current * 2
	if next > max || next < current {
		return max
	}
	return next
}
