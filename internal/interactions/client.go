package interactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"cloud.google.com/go/auth/credentials"
	authhttp "cloud.google.com/go/auth/httptransport"
	"github.com/tvaroska/jeep/internal/retry"
)

type Client struct {
	project        string
	region         string
	baseURL        string
	httpClient     *http.Client
	initialBackoff time.Duration
	retryBackoff   time.Duration
	Retries        int
}

func NewClient(ctx context.Context, project, region string) (*Client, error) {
	cred, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return nil, fmt.Errorf("detecting credentials: %w", err)
	}
	httpClient, err := authhttp.NewClient(&authhttp.Options{
		Credentials: cred,
	})
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}
	return &Client{
		project:    project,
		region:     region,
		baseURL:    "https://aiplatform.googleapis.com",
		httpClient: httpClient,
	}, nil
}

type CreateRequest struct {
	Agent       string         `json:"agent,omitempty"`
	Input       string         `json:"input"`
	Background  bool           `json:"background,omitempty"`
	AgentConfig map[string]any `json:"agent_config,omitempty"`
}

type Interaction struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Steps      []Step `json:"steps,omitempty"`
	Usage      *Usage `json:"usage,omitempty"`
	OutputText string `json:"output_text,omitempty"`
}

type Step struct {
	Type    string    `json:"type"`
	Content []Content `json:"content,omitempty"`
}

type Content struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Annotations []Annotation `json:"annotations,omitempty"`
}

type Annotation struct {
	Type       string `json:"type"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	StartIndex int    `json:"start_index,omitempty"`
	EndIndex   int    `json:"end_index,omitempty"`
}

type Usage struct {
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

func (c *Client) url(path string) string {
	return fmt.Sprintf("%s/v1beta1/projects/%s/locations/%s/%s", c.baseURL, c.project, c.region, path)
}

type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func isRetryableHTTP(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		switch he.StatusCode {
		case 429, 500, 503:
			return true
		}
	}
	return false
}

func (c *Client) withRetry(ctx context.Context, fn func() error) error {
	base := c.retryBackoff
	if base == 0 {
		base = time.Second
	}
	return retry.Do(ctx, c.Retries, base, isRetryableHTTP, fn)
}

func (c *Client) Create(ctx context.Context, req *CreateRequest) (*Interaction, error) {
	var interaction *Interaction
	err := c.withRetry(ctx, func() error {
		body, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("interactions"), bytes.NewReader(body))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("creating interaction: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("creating interaction: %w", &httpError{StatusCode: resp.StatusCode, Body: string(data)})
		}

		var ix Interaction
		if err := json.NewDecoder(resp.Body).Decode(&ix); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		interaction = &ix
		return nil
	})
	return interaction, err
}

func (c *Client) Get(ctx context.Context, id string) (*Interaction, error) {
	var interaction *Interaction
	err := c.withRetry(ctx, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("interactions/"+id), nil)
		if err != nil {
			return err
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("getting interaction: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("getting interaction %s: %w", id, &httpError{StatusCode: resp.StatusCode, Body: string(data)})
		}

		var ix Interaction
		if err := json.NewDecoder(resp.Body).Decode(&ix); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		interaction = &ix
		return nil
	})
	return interaction, err
}

func (c *Client) RunAndWait(ctx context.Context, req *CreateRequest, onStatus func(string)) (*Interaction, error) {
	interaction, err := c.Create(ctx, req)
	if err != nil {
		return nil, err
	}

	if onStatus != nil {
		onStatus(fmt.Sprintf("Research started (id: %s)", interaction.ID))
	}

	backoff := c.initialBackoff
	if backoff == 0 {
		backoff = 5 * time.Second
	}
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		interaction, err = c.Get(ctx, interaction.ID)
		if err != nil {
			return nil, err
		}

		switch interaction.Status {
		case "completed":
			return interaction, nil
		case "failed":
			return nil, fmt.Errorf("research failed (id: %s)", interaction.ID)
		case "cancelled":
			return nil, fmt.Errorf("research cancelled (id: %s)", interaction.ID)
		}

		if onStatus != nil {
			onStatus(fmt.Sprintf("Status: %s", interaction.Status))
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

type Source struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

func (i *Interaction) ReportText() string {
	if i.OutputText != "" {
		return i.OutputText
	}
	for j := len(i.Steps) - 1; j >= 0; j-- {
		if i.Steps[j].Type == "model_output" {
			for _, c := range i.Steps[j].Content {
				if c.Text != "" {
					return c.Text
				}
			}
		}
	}
	return ""
}

func (i *Interaction) Sources() []Source {
	seen := map[string]bool{}
	var sources []Source
	for _, step := range i.Steps {
		for _, c := range step.Content {
			for _, a := range c.Annotations {
				if a.Type == "url_citation" && a.URL != "" && !seen[a.URL] {
					seen[a.URL] = true
					sources = append(sources, Source{Title: a.Title, URL: a.URL})
				}
			}
		}
	}
	return sources
}
