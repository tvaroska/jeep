package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tvaroska/jeep/internal/retry"
	"google.golang.org/genai"
)

var mimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".mkv":  "video/x-matroska",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".json": "application/json",
}

type ModelInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

func ListModels(ctx context.Context, project, region string) ([]ModelInfo, error) {
	client, err := newClient(ctx, project, region)
	if err != nil {
		return nil, err
	}
	var models []ModelInfo
	for m, err := range client.Models.All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("listing models: %w", err)
		}
		models = append(models, ModelInfo{
			Name:        m.Name,
			DisplayName: m.DisplayName,
			Description: m.Description,
		})
	}
	return models, nil
}

type FileInput struct {
	Name string
	Path string
}

type JSONMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type JSONRequest struct {
	Prompt      string          `json:"prompt"`
	Messages    []JSONMessage   `json:"messages,omitempty"`
	Model       string          `json:"model,omitempty"`
	System      string          `json:"system,omitempty"`
	Project     string          `json:"project,omitempty"`
	Region      string          `json:"region,omitempty"`
	Search      bool            `json:"search,omitempty"`
	Schema      *genai.Schema   `json:"schema,omitempty"`
	Files       []jsonFileInput `json:"files,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   int32           `json:"max_tokens,omitempty"`
}

type jsonFileInput struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
}

func LoadJSONRequest(path string) (*JSONRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading JSON file: %w", err)
	}
	var req JSONRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing JSON file: %w", err)
	}
	return &req, nil
}

func JSONFileInputsToFileInputs(jFiles []jsonFileInput) []FileInput {
	var inputs []FileInput
	autoIdx := 1
	for _, jf := range jFiles {
		name := jf.Name
		if name == "" {
			name = fmt.Sprintf("%d", autoIdx)
			autoIdx++
		}
		inputs = append(inputs, FileInput{Name: name, Path: jf.Path})
	}
	return inputs
}

func (r *JSONRequest) FileInputs() []FileInput {
	return JSONFileInputsToFileInputs(r.Files)
}

// ParseFileInputs parses -f values into named FileInputs. A value is treated as
// "name=path" only when the text before the first '=' looks like a bare label:
// it must not be a gs:// URI and must contain no '/' or '.'. This avoids
// misreading paths and URIs that legitimately contain '=' (e.g. "gs://b/k=v" or
// "dir/f=n.jpg"). Values without an explicit name get a 1-based auto index.
func ParseFileInputs(files []string) []FileInput {
	var inputs []FileInput
	autoIdx := 1
	for _, f := range files {
		if idx := strings.IndexByte(f, '='); idx > 0 && !strings.HasPrefix(f, "gs://") && !strings.Contains(f[:idx], "/") && !strings.Contains(f[:idx], ".") {
			inputs = append(inputs, FileInput{Name: f[:idx], Path: f[idx+1:]})
		} else {
			inputs = append(inputs, FileInput{Name: fmt.Sprintf("%d", autoIdx), Path: f})
			autoIdx++
		}
	}
	return inputs
}

const maxFetchSize = 100 << 20 // 100 MB

func fetchURL(rawURL string) ([]byte, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetching %s: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", rawURL, err)
	}
	if len(data) > maxFetchSize {
		return nil, "", fmt.Errorf("fetching %s: response exceeds 100 MB limit", rawURL)
	}
	parsed, _ := url.Parse(rawURL)
	mime := mimeFromExt(parsed.Path)
	if mime == "" {
		ct := resp.Header.Get("Content-Type")
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		mime = strings.TrimSpace(ct)
	}
	if mime == "" {
		mime = http.DetectContentType(data[:min(512, len(data))])
	}
	return data, mime, nil
}

func BuildFileParts(fi FileInput) ([]*genai.Part, error) {
	parts := []*genai.Part{
		genai.NewPartFromText(fmt.Sprintf(`<FILE name="%s">`, fi.Name)),
	}

	path := fi.Path
	switch {
	case path == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		mime := http.DetectContentType(data[:min(512, len(data))])
		parts = append(parts, genai.NewPartFromBytes(data, mime))
	case isYouTubeURL(path):
		parts = append(parts, genai.NewPartFromURI(path, "video/youtube"))
	case strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://"):
		data, mime, err := fetchURL(path)
		if err != nil {
			return nil, err
		}
		parts = append(parts, genai.NewPartFromBytes(data, mime))
	case strings.HasPrefix(path, "gs://"):
		mime := mimeFromExt(path)
		if mime == "" {
			mime = "application/octet-stream"
		}
		parts = append(parts, genai.NewPartFromURI(path, mime))
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			fmt.Fprintf(os.Stderr, "warning: %s is empty\n", path)
		}
		mime := mimeFromExt(path)
		if mime == "" {
			mime = http.DetectContentType(data[:min(512, len(data))])
		}
		parts = append(parts, genai.NewPartFromBytes(data, mime))
	}

	parts = append(parts, genai.NewPartFromText("</FILE>"))
	return parts, nil
}

type Source struct {
	Title  string `json:"title,omitempty"`
	URI    string `json:"uri"`
	Domain string `json:"domain,omitempty"`
}

type GenerateResult struct {
	Text             string   `json:"text"`
	Model            string   `json:"model,omitempty"`
	FinishReason     string   `json:"finish_reason,omitempty"`
	PromptTokens     int32    `json:"prompt_tokens,omitempty"`
	OutputTokens     int32    `json:"output_tokens,omitempty"`
	Sources          []Source `json:"sources,omitempty"`
	WebSearchQueries []string `json:"web_search_queries,omitempty"`
}

type GenerateOptions struct {
	Temperature *float64
	TopP        *float64
	MaxTokens   int32
	Retries     int
}

func newClient(ctx context.Context, project, region string) (*genai.Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: region,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return client, nil
}

func BuildAllFileParts(inputs []FileInput) ([]*genai.Part, error) {
	var parts []*genai.Part
	for _, fi := range inputs {
		fileParts, err := BuildFileParts(fi)
		if err != nil {
			return nil, fmt.Errorf("processing %s: %w", fi.Path, err)
		}
		parts = append(parts, fileParts...)
	}
	return parts, nil
}

func BuildContents(prompt string, messages []JSONMessage, fileParts []*genai.Part) []*genai.Content {
	if len(messages) == 0 {
		parts := append(fileParts, genai.NewPartFromText(prompt))
		return []*genai.Content{{Role: "user", Parts: parts}}
	}

	var contents []*genai.Content
	for _, m := range messages {
		contents = append(contents, &genai.Content{
			Role:  m.Role,
			Parts: []*genai.Part{genai.NewPartFromText(m.Content)},
		})
	}

	if prompt != "" {
		contents = append(contents, &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{genai.NewPartFromText(prompt)},
		})
	}

	if len(fileParts) > 0 {
		last := contents[len(contents)-1]
		last.Parts = append(fileParts, last.Parts...)
	}

	return contents
}

func isRetryable(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 429, 500, 503:
			return true
		}
	}
	return false
}

func Generate(ctx context.Context, project, region, model, system string, contents []*genai.Content, search bool, schema *genai.Schema, opts *GenerateOptions) (*GenerateResult, error) {
	client, err := newClient(ctx, project, region)
	if err != nil {
		return nil, err
	}

	config := &genai.GenerateContentConfig{}
	if system != "" {
		config.SystemInstruction = genai.NewContentFromText(system, "user")
	}
	if search {
		config.Tools = []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}
	}
	if schema != nil {
		config.ResponseMIMEType = "application/json"
		config.ResponseSchema = schema
	}
	if opts != nil {
		if opts.Temperature != nil {
			config.Temperature = genai.Ptr(float32(*opts.Temperature))
		}
		if opts.TopP != nil {
			config.TopP = genai.Ptr(float32(*opts.TopP))
		}
		if opts.MaxTokens > 0 {
			config.MaxOutputTokens = opts.MaxTokens
		}
	}

	retries := 0
	if opts != nil {
		retries = opts.Retries
	}

	var resp *genai.GenerateContentResponse
	err = retry.Do(ctx, retries, time.Second, isRetryable, func() error {
		var e error
		resp, e = client.Models.GenerateContent(ctx, model, contents, config)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("generating content: %w", err)
	}

	result := parseGenerateResult(resp)

	var wg sync.WaitGroup
	for i := range result.Sources {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result.Sources[idx].URI = ResolveRedirect(result.Sources[idx].URI)
		}(i)
	}
	wg.Wait()

	return result, nil
}

// parseGenerateResult extracts text, usage, and deduplicated grounding sources
// from a response. Sources are deduplicated by URI so that text and JSON output
// stay consistent. It performs no network I/O (redirect resolution happens in
// Generate), which keeps it unit-testable offline.
func parseGenerateResult(resp *genai.GenerateContentResponse) *GenerateResult {
	result := &GenerateResult{Model: resp.ModelVersion}

	if resp.UsageMetadata != nil {
		result.PromptTokens = resp.UsageMetadata.PromptTokenCount
		result.OutputTokens = resp.UsageMetadata.CandidatesTokenCount
	}

	var sb strings.Builder
	seen := map[string]bool{}
	for _, c := range resp.Candidates {
		if result.FinishReason == "" {
			result.FinishReason = string(c.FinishReason)
		}
		if c.Content != nil {
			for _, p := range c.Content.Parts {
				if p.Text != "" {
					sb.WriteString(p.Text)
				}
			}
		}
		if c.GroundingMetadata != nil {
			for _, chunk := range c.GroundingMetadata.GroundingChunks {
				if chunk.Web != nil && chunk.Web.URI != "" && !seen[chunk.Web.URI] {
					seen[chunk.Web.URI] = true
					result.Sources = append(result.Sources, Source{
						Title:  chunk.Web.Title,
						URI:    chunk.Web.URI,
						Domain: chunk.Web.Domain,
					})
				}
			}
			result.WebSearchQueries = append(result.WebSearchQueries, c.GroundingMetadata.WebSearchQueries...)
		}
	}
	result.Text = sb.String()
	return result
}

type ImageResult struct {
	Data         []byte
	MIMEType     string
	Text         string
	ModelVersion string
}

func GenerateImage(ctx context.Context, project, region, model, prompt string, inputs []FileInput, retries int) (*ImageResult, error) {
	client, err := newClient(ctx, project, region)
	if err != nil {
		return nil, err
	}

	parts, err := BuildAllFileParts(inputs)
	if err != nil {
		return nil, err
	}
	parts = append(parts, genai.NewPartFromText(prompt))

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"IMAGE", "TEXT"},
	}

	var resp *genai.GenerateContentResponse
	err = retry.Do(ctx, retries, time.Second, isRetryable, func() error {
		var e error
		resp, e = client.Models.GenerateContent(ctx, model,
			[]*genai.Content{{Role: "user", Parts: parts}},
			config,
		)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("generating content: %w", err)
	}

	return parseImageResult(resp)
}

func parseImageResult(resp *genai.GenerateContentResponse) (*ImageResult, error) {
	imgResult := &ImageResult{ModelVersion: resp.ModelVersion}
	for _, c := range resp.Candidates {
		if c.Content == nil {
			continue
		}
		for _, p := range c.Content.Parts {
			if p.InlineData != nil {
				imgResult.Data = p.InlineData.Data
				imgResult.MIMEType = p.InlineData.MIMEType
			}
			if p.Text != "" {
				imgResult.Text += p.Text
			}
		}
	}
	if imgResult.Data == nil {
		return nil, fmt.Errorf("no image returned by the model")
	}
	return imgResult, nil
}

type SpeechResult struct {
	PCMData      []byte
	ModelVersion string
}

func GenerateSpeech(ctx context.Context, project, region, model, voice, prompt string, retries int) (*SpeechResult, error) {
	client, err := newClient(ctx, project, region)
	if err != nil {
		return nil, err
	}

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
					VoiceName: voice,
				},
			},
		},
	}

	var resp *genai.GenerateContentResponse
	err = retry.Do(ctx, retries, time.Second, isRetryable, func() error {
		var e error
		resp, e = client.Models.GenerateContent(ctx, model,
			[]*genai.Content{{Role: "user", Parts: []*genai.Part{genai.NewPartFromText(prompt)}}},
			config,
		)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("generating speech: %w", err)
	}

	return parseSpeechResult(resp)
}

func parseSpeechResult(resp *genai.GenerateContentResponse) (*SpeechResult, error) {
	for _, c := range resp.Candidates {
		if c.Content == nil {
			continue
		}
		for _, p := range c.Content.Parts {
			if p.InlineData != nil && len(p.InlineData.Data) > 0 {
				return &SpeechResult{PCMData: p.InlineData.Data, ModelVersion: resp.ModelVersion}, nil
			}
		}
	}
	return nil, fmt.Errorf("no audio returned by the model")
}

func ResolveRedirect(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host != "vertexaisearch.cloud.google.com" {
		return rawURL
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head(rawURL)
	if err != nil {
		return rawURL
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "" {
		return loc
	}
	return rawURL
}

func isYouTubeURL(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "youtube.com/watch") || strings.Contains(s, "youtu.be/")
}

func mimeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return mimeTypes[ext]
}
