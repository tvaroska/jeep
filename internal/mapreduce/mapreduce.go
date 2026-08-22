// Package mapreduce is a generic, provider-agnostic chunk→map→reduce
// [+refine] orchestrator. The gemini-backed binding (Step) routes each stage
// through `gemini.Generate`; the orchestrator itself has no GCP dependency and
// is unit-tested with plain fakes.
package mapreduce

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

// Spec describes one map-reduce run. Element E is one input chunk, Partial P is
// one mapped result, Result R is the final reduced value.
type Spec[E any, P any, R any] struct {
	Elements    []E
	Map         func(ctx context.Context, i int, e E) (P, error) // required
	Reduce      func(ctx context.Context, partials []P) (R, error) // required
	Refine      func(ctx context.Context, r R) (R, error)          // optional; nil => skip
	Concurrency int                                                 // <=0 => defaultConcurrency
}

const defaultConcurrency = 4

// Run maps every element concurrently through a bounded, fail-fast worker pool,
// collects the partials in input order, reduces them into a single result, and
// (if Refine != nil) refines it. Context cancellation is honored; the first Map
// error cancels the rest and is returned (fail-fast), so Reduce/Refine never run
// on a partial failure. Empty Elements calls Reduce(ctx, nil) so callers get a
// defined result (e.g. an empty-summary string); it does NOT early-return a zero
// value on empty input.
func Run[E any, P any, R any](ctx context.Context, spec Spec[E, P, R]) (R, error) {
	var zero R

	// Early-out on cancelled context.
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	// Validate required functions.
	if spec.Map == nil || spec.Reduce == nil {
		return zero, fmt.Errorf("mapreduce: Map and Reduce are required")
	}

	// Map phase: empty Elements → call Reduce(ctx, nil) (do NOT early-return zero).
	partials := make([]P, len(spec.Elements))
	if len(spec.Elements) > 0 {
		concurrency := spec.Concurrency
		if concurrency <= 0 {
			concurrency = defaultConcurrency
		}

		grp, gctx := errgroup.WithContext(ctx)
		grp.SetLimit(concurrency)
		for i, e := range spec.Elements {
			i, e := i, e
			grp.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				p, err := spec.Map(gctx, i, e)
				if err != nil {
					return fmt.Errorf("mapreduce: map element %d: %w", i, err)
				}
				partials[i] = p
				return nil
			})
		}
		if err := grp.Wait(); err != nil {
			return zero, err
		}
	}

	// Reduce phase (use outer ctx, not gctx which is cancelled after Wait).
	r, err := spec.Reduce(ctx, partials)
	if err != nil {
		return zero, fmt.Errorf("mapreduce: reduce: %w", err)
	}

	// Refine phase (optional).
	if spec.Refine != nil {
		r, err = spec.Refine(ctx, r)
		if err != nil {
			return zero, fmt.Errorf("mapreduce: refine: %w", err)
		}
	}

	return r, nil
}
