// Package retry provides a small exponential-backoff retry helper shared by the
// Gemini and Interactions clients. Both wrap the same loop and differ only in
// which errors they treat as retryable, so that predicate is injected.
package retry

import (
	"context"
	"math/rand/v2"
	"time"
)

// Do runs fn, retrying while isRetryable(err) reports true, for up to `retries`
// additional attempts (so fn is called at most retries+1 times). Between
// attempts it waits base<<attempt plus up to 50% jitter, returning early with
// ctx.Err() if ctx is cancelled during the wait. A base <= 0 defaults to one
// second; jitter is skipped when the backoff window is too small to divide.
func Do(ctx context.Context, retries int, base time.Duration, isRetryable func(error) bool, fn func() error) error {
	if base <= 0 {
		base = time.Second
	}
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || !isRetryable(err) || attempt == retries {
			return err
		}
		wait := base << attempt
		if half := int64(wait) / 2; half > 0 {
			wait += time.Duration(rand.Int64N(half))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
