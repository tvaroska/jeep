package mapreduce

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRun_ChunkMapReduce_ProducesExpectedResult tests the basic map-reduce flow
// with integers: map multiplies by 10, reduce sums to 60.
func TestRun_ChunkMapReduce_ProducesExpectedResult(t *testing.T) {
	spec := Spec[int, int, int]{
		Elements: []int{1, 2, 3},
		Map: func(ctx context.Context, i int, e int) (int, error) {
			return e * 10, nil
		},
		Reduce: func(ctx context.Context, partials []int) (int, error) {
			sum := 0
			for _, p := range partials {
				sum += p
			}
			return sum, nil
		},
	}

	result, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != 60 {
		t.Errorf("Run() = %d, want 60", result)
	}
}

// TestRun_PreservesInputOrder verifies that partials are collected in element order
// despite concurrent completion.
func TestRun_PreservesInputOrder(t *testing.T) {
	spec := Spec[int, int, string]{
		Elements: []int{0, 1, 2, 3, 4},
		Map: func(ctx context.Context, i int, e int) (int, error) {
			// Sleep inversely to index so higher indices complete first.
			time.Sleep(time.Duration(5-i) * time.Millisecond)
			return e, nil
		},
		Reduce: func(ctx context.Context, partials []int) (string, error) {
			var parts []string
			for _, p := range partials {
				parts = append(parts, fmt.Sprintf("%d", p))
			}
			return strings.Join(parts, ","), nil
		},
	}

	result, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "0,1,2,3,4"
	if result != want {
		t.Errorf("Run() = %q, want %q (order not preserved)", result, want)
	}
}

// TestRun_MapError_FailFast verifies that a Map error stops the run and Reduce
// is not called.
func TestRun_MapError_FailFast(t *testing.T) {
	var reduceCalled atomic.Bool

	spec := Spec[int, int, int]{
		Elements: []int{1, 2, 3},
		Map: func(ctx context.Context, i int, e int) (int, error) {
			if e == 2 {
				return 0, errors.New("map failed on 2")
			}
			return e * 10, nil
		},
		Reduce: func(ctx context.Context, partials []int) (int, error) {
			reduceCalled.Store(true)
			return 0, nil
		},
	}

	_, err := Run(context.Background(), spec)
	if err == nil {
		t.Fatal("Run() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "map element") || !strings.Contains(err.Error(), "map failed on 2") {
		t.Errorf("Run() error = %v, want map error with 'map failed on 2'", err)
	}
	if reduceCalled.Load() {
		t.Error("Reduce was called despite Map error (expected fail-fast)")
	}
}

// TestRun_EmptyElements_CallsReduceWithNil verifies that empty Elements calls
// Reduce with nil/empty partials.
func TestRun_EmptyElements_CallsReduceWithNil(t *testing.T) {
	sentinel := "empty-result"
	var calledWithLen int

	spec := Spec[int, int, string]{
		Elements: nil,
		Map: func(ctx context.Context, i int, e int) (int, error) {
			return e, nil
		},
		Reduce: func(ctx context.Context, partials []int) (string, error) {
			calledWithLen = len(partials)
			return sentinel, nil
		},
	}

	result, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result != sentinel {
		t.Errorf("Run() = %q, want %q", result, sentinel)
	}
	if calledWithLen != 0 {
		t.Errorf("Reduce called with len(partials)=%d, want 0", calledWithLen)
	}
}

// TestRun_Refine_Applied verifies that Refine is applied when non-nil and
// skipped when nil.
func TestRun_Refine_Applied(t *testing.T) {
	t.Run("WithRefine", func(t *testing.T) {
		spec := Spec[int, int, string]{
			Elements: []int{1, 2},
			Map: func(ctx context.Context, i int, e int) (int, error) {
				return e, nil
			},
			Reduce: func(ctx context.Context, partials []int) (string, error) {
				return "result", nil
			},
			Refine: func(ctx context.Context, r string) (string, error) {
				return strings.ToUpper(r), nil
			},
		}

		result, err := Run(context.Background(), spec)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result != "RESULT" {
			t.Errorf("Run() = %q, want 'RESULT' (Refine not applied)", result)
		}
	})

	t.Run("WithoutRefine", func(t *testing.T) {
		spec := Spec[int, int, string]{
			Elements: []int{1, 2},
			Map: func(ctx context.Context, i int, e int) (int, error) {
				return e, nil
			},
			Reduce: func(ctx context.Context, partials []int) (string, error) {
				return "result", nil
			},
			Refine: nil,
		}

		result, err := Run(context.Background(), spec)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result != "result" {
			t.Errorf("Run() = %q, want 'result'", result)
		}
	})
}

// TestRun_ContextCancelled verifies that a pre-cancelled context returns
// context.Canceled.
func TestRun_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	spec := Spec[int, int, int]{
		Elements: []int{1, 2, 3},
		Map: func(ctx context.Context, i int, e int) (int, error) {
			return e, nil
		},
		Reduce: func(ctx context.Context, partials []int) (int, error) {
			return 0, nil
		},
	}

	_, err := Run(ctx, spec)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want context.Canceled", err)
	}
}

// TestRun_MissingMapOrReduce_Errors verifies that nil Map or nil Reduce returns
// an error.
func TestRun_MissingMapOrReduce_Errors(t *testing.T) {
	t.Run("NilMap", func(t *testing.T) {
		spec := Spec[int, int, int]{
			Elements: []int{1, 2, 3},
			Map:      nil,
			Reduce: func(ctx context.Context, partials []int) (int, error) {
				return 0, nil
			},
		}

		_, err := Run(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), "Map and Reduce are required") {
			t.Errorf("Run() error = %v, want 'Map and Reduce are required'", err)
		}
	})

	t.Run("NilReduce", func(t *testing.T) {
		spec := Spec[int, int, int]{
			Elements: []int{1, 2, 3},
			Map: func(ctx context.Context, i int, e int) (int, error) {
				return e, nil
			},
			Reduce: nil,
		}

		_, err := Run(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), "Map and Reduce are required") {
			t.Errorf("Run() error = %v, want 'Map and Reduce are required'", err)
		}
	})
}
