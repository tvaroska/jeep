// Package usage accumulates per-run Gemini token consumption (prompt / output /
// total tokens) off the genai response metadata. Pure-local (no GCP calls), written
// concurrently by worker pools, so all mutation is mutex-guarded and nil-safe.
package usage

import (
	"log/slog"
	"sync"
)

// Tracker accumulates usage across a run. The zero value is NOT ready — use New.
// All methods are safe for concurrent use and safe to call on a nil *Tracker
// (nil == "recording disabled", a no-op), so adapters can call unconditionally.
type Tracker struct {
	mu sync.Mutex

	// per-model token totals and call counts
	llm map[string]*counters
}

type counters struct{ promptTokens, outputTokens, totalTokens, calls int64 }

// New creates a ready-to-use Tracker.
func New() *Tracker {
	return &Tracker{llm: map[string]*counters{}}
}

// Record adds one Gemini call's token usage. No-op on a nil receiver.
func (t *Tracker) Record(model string, prompt, output, total int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.llm[model]
	if c == nil {
		c = &counters{}
		t.llm[model] = c
	}
	c.promptTokens += prompt
	c.outputTokens += output
	c.totalTokens += total
	c.calls++
}

// Report holds per-run token totals.
type Report struct {
	PromptTokens int64
	OutputTokens int64
	TotalTokens  int64
	Calls        int64
}

// Report snapshots the current usage counters.
// Nil-safe: returns an empty (zero-value) report on a nil receiver.
func (t *Tracker) Report() Report {
	if t == nil {
		return Report{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var r Report

	// Aggregate token totals across all models.
	for _, c := range t.llm {
		r.PromptTokens += c.promptTokens
		r.OutputTokens += c.outputTokens
		r.TotalTokens += c.totalTokens
		r.Calls += c.calls
	}

	return r
}

// Log emits a single structured slog line with the current usage report.
// Nil-safe: no-op on a nil receiver or nil logger.
func (t *Tracker) Log(logger *slog.Logger) {
	if t == nil || logger == nil {
		return
	}

	r := t.Report()
	logger.Info("run token usage",
		"prompt_tokens", r.PromptTokens,
		"output_tokens", r.OutputTokens,
		"total_tokens", r.TotalTokens,
		"calls", r.Calls,
	)
}
