package interactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		project:        "test-project",
		region:         "global",
		baseURL:        srv.URL,
		httpClient:     srv.Client(),
		initialBackoff: 10 * time.Millisecond,
	}
}

func TestCreate(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/v1beta1/projects/test-project/locations/global/interactions"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}

		var req CreateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Agent != "deep-research-preview-04-2026" {
			t.Errorf("agent = %q", req.Agent)
		}
		if req.Input != "test query" {
			t.Errorf("input = %q", req.Input)
		}

		json.NewEncoder(w).Encode(Interaction{
			ID:     "interaction-123",
			Status: "in_progress",
		})
	}))

	interaction, err := client.Create(context.Background(), &CreateRequest{
		Agent:      "deep-research-preview-04-2026",
		Input:      "test query",
		Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if interaction.ID != "interaction-123" {
		t.Errorf("id = %q", interaction.ID)
	}
	if interaction.Status != "in_progress" {
		t.Errorf("status = %q", interaction.Status)
	}
}

func TestGet(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		wantPath := "/v1beta1/projects/test-project/locations/global/interactions/interaction-123"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}

		json.NewEncoder(w).Encode(Interaction{
			ID:     "interaction-123",
			Status: "completed",
			Steps: []Step{
				{Type: "model_output", Content: []Content{{Type: "text", Text: "research report"}}},
			},
			OutputText: "research report",
			Usage:      &Usage{TotalInputTokens: 100, TotalOutputTokens: 500, TotalTokens: 600},
		})
	}))

	interaction, err := client.Get(context.Background(), "interaction-123")
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Status != "completed" {
		t.Errorf("status = %q", interaction.Status)
	}
	if interaction.OutputText != "research report" {
		t.Errorf("output_text = %q", interaction.OutputText)
	}
	if interaction.Usage.TotalTokens != 600 {
		t.Errorf("total_tokens = %d", interaction.Usage.TotalTokens)
	}
}

func TestCreateHTTPError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("access denied"))
	}))

	_, err := client.Create(context.Background(), &CreateRequest{Input: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunAndWait(t *testing.T) {
	var pollCount atomic.Int32
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(Interaction{ID: "int-1", Status: "in_progress"})
			return
		}
		n := pollCount.Add(1)
		status := "in_progress"
		if n >= 2 {
			status = "completed"
		}
		json.NewEncoder(w).Encode(Interaction{
			ID:         "int-1",
			Status:     status,
			OutputText: "final report",
		})
	}))

	var statuses []string
	interaction, err := client.RunAndWait(context.Background(), &CreateRequest{
		Agent:      "deep-research-preview-04-2026",
		Input:      "test",
		Background: true,
	}, func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatal(err)
	}
	if interaction.Status != "completed" {
		t.Errorf("status = %q", interaction.Status)
	}
	if len(statuses) == 0 {
		t.Error("expected status callbacks")
	}
}

func TestReportText_OutputText(t *testing.T) {
	i := &Interaction{OutputText: "from output_text"}
	if got := i.ReportText(); got != "from output_text" {
		t.Errorf("ReportText() = %q", got)
	}
}

func TestReportText_FromSteps(t *testing.T) {
	i := &Interaction{
		Steps: []Step{
			{Type: "search", Content: []Content{{Type: "text", Text: "search result"}}},
			{Type: "model_output", Content: []Content{{Type: "text", Text: "the report"}}},
		},
	}
	if got := i.ReportText(); got != "the report" {
		t.Errorf("ReportText() = %q", got)
	}
}

func TestReportText_Empty(t *testing.T) {
	i := &Interaction{}
	if got := i.ReportText(); got != "" {
		t.Errorf("ReportText() = %q, want empty", got)
	}
}
