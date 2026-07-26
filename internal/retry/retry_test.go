package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errRetryable = errors.New("retryable")

func retryable(err error) bool { return errors.Is(err, errRetryable) }

func TestDo_ImmediateSuccess(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, time.Millisecond, retryable, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_RetryableThenSuccess(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, time.Millisecond, retryable, func() error {
		calls++
		if calls < 3 {
			return errRetryable
		}
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_ExhaustsRetries(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 2, time.Millisecond, retryable, func() error {
		calls++
		return errRetryable
	})
	if !errors.Is(err, errRetryable) {
		t.Fatalf("err = %v, want errRetryable", err)
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_NonRetryableReturnsImmediately(t *testing.T) {
	other := errors.New("boom")
	calls := 0
	err := Do(context.Background(), 5, time.Millisecond, retryable, func() error {
		calls++
		return other
	})
	if !errors.Is(err, other) {
		t.Fatalf("err = %v, want %v", err, other)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on non-retryable)", calls)
	}
}

func TestDo_ContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Do(ctx, 3, time.Millisecond, retryable, func() error {
		calls++
		return errRetryable
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 before cancellation observed", calls)
	}
}

func TestDo_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Long base so the backoff wait is still pending when we cancel.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	err := Do(ctx, 3, time.Second, retryable, func() error {
		return errRetryable
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDo_ZeroBaseDefaults(t *testing.T) {
	// base <= 0 must not panic (would divide by zero on jitter) and must still
	// run the initial attempt. Success on first call avoids the 1s default wait.
	calls := 0
	err := Do(context.Background(), 1, 0, retryable, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_NoRetriesConfigured(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 0, time.Millisecond, retryable, func() error {
		calls++
		return errRetryable
	})
	if !errors.Is(err, errRetryable) {
		t.Fatalf("err = %v, want errRetryable", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
