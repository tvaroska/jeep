package usage

import (
	"log/slog"
	"sync"
	"testing"
)

func TestTracker_RecordAndReport(t *testing.T) {
	tests := []struct {
		name       string
		record     func(*Tracker)
		wantPrompt int64
		wantOutput int64
		wantTotal  int64
		wantCalls  int64
	}{
		{
			name: "single call",
			record: func(tr *Tracker) {
				tr.Record("gemini-2.5-flash", 100, 40, 140)
			},
			wantPrompt: 100,
			wantOutput: 40,
			wantTotal:  140,
			wantCalls:  1,
		},
		{
			name: "multiple calls same model",
			record: func(tr *Tracker) {
				tr.Record("gemini-2.5-flash", 100, 40, 140)
				tr.Record("gemini-2.5-flash", 200, 60, 260)
			},
			wantPrompt: 300,
			wantOutput: 100,
			wantTotal:  400,
			wantCalls:  2,
		},
		{
			name: "multiple calls different models",
			record: func(tr *Tracker) {
				tr.Record("gemini-2.5-flash", 100, 40, 140)
				tr.Record("gemini-2.0-flash", 200, 60, 260)
			},
			wantPrompt: 300,
			wantOutput: 100,
			wantTotal:  400,
			wantCalls:  2,
		},
		{
			name: "mixed models",
			record: func(tr *Tracker) {
				tr.Record("gemini-2.5-flash", 1000, 200, 1200)
				tr.Record("gemini-2.0-flash", 500, 100, 600)
				tr.Record("gemini-2.5-flash", 500, 100, 600)
			},
			wantPrompt: 2000,
			wantOutput: 400,
			wantTotal:  2400,
			wantCalls:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New()
			tt.record(tr)

			r := tr.Report()

			if r.PromptTokens != tt.wantPrompt {
				t.Errorf("PromptTokens = %d, want %d", r.PromptTokens, tt.wantPrompt)
			}
			if r.OutputTokens != tt.wantOutput {
				t.Errorf("OutputTokens = %d, want %d", r.OutputTokens, tt.wantOutput)
			}
			if r.TotalTokens != tt.wantTotal {
				t.Errorf("TotalTokens = %d, want %d", r.TotalTokens, tt.wantTotal)
			}
			if r.Calls != tt.wantCalls {
				t.Errorf("Calls = %d, want %d", r.Calls, tt.wantCalls)
			}
		})
	}
}

func TestTracker_NilSafe(t *testing.T) {
	var tr *Tracker // nil pointer

	// All these should be no-ops and not panic.
	tr.Record("gemini-2.5-flash", 100, 40, 140)

	r := tr.Report()
	if r.PromptTokens != 0 || r.OutputTokens != 0 || r.TotalTokens != 0 || r.Calls != 0 {
		t.Errorf("nil Tracker Report should return zero-value report, got %+v", r)
	}

	// Log should also be nil-safe.
	logger := slog.Default()
	tr.Log(logger) // should not panic

	// Nil logger should also be safe.
	realTracker := New()
	realTracker.Log(nil) // should not panic
}

func TestTracker_Concurrent(t *testing.T) {
	tr := New()

	// Launch N goroutines each calling Record M times.
	const N = 10
	const M = 100

	var wg sync.WaitGroup
	wg.Add(N)

	// Workers
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < M; j++ {
				tr.Record("test-model", 10, 5, 15)
			}
		}()
	}

	wg.Wait()

	r := tr.Report()

	// Each call adds 10 prompt, 5 output, 15 total tokens.
	wantPrompt := int64(N * M * 10)
	wantOutput := int64(N * M * 5)
	wantTotal := int64(N * M * 15)
	wantCalls := int64(N * M)

	if r.PromptTokens != wantPrompt {
		t.Errorf("PromptTokens = %d, want %d", r.PromptTokens, wantPrompt)
	}
	if r.OutputTokens != wantOutput {
		t.Errorf("OutputTokens = %d, want %d", r.OutputTokens, wantOutput)
	}
	if r.TotalTokens != wantTotal {
		t.Errorf("TotalTokens = %d, want %d", r.TotalTokens, wantTotal)
	}
	if r.Calls != wantCalls {
		t.Errorf("Calls = %d, want %d", r.Calls, wantCalls)
	}
}
