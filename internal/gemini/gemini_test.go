package gemini

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestMimeFromExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPEG", "image/jpeg"},
		{"doc.pdf", "application/pdf"},
		{"video.mp4", "video/mp4"},
		{"file.txt", "text/plain"},
		{"noext", ""},
		{"gs://bucket/object", ""},
		{"archive.tar.gz", ""},
	}
	for _, tt := range tests {
		if got := mimeFromExt(tt.path); got != tt.want {
			t.Errorf("mimeFromExt(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseFileInputs(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []FileInput
	}{
		{
			name:  "named file",
			files: []string{"photo=img.jpg"},
			want:  []FileInput{{Name: "photo", Path: "img.jpg"}},
		},
		{
			name:  "auto numbered",
			files: []string{"img.jpg", "doc.pdf"},
			want: []FileInput{
				{Name: "1", Path: "img.jpg"},
				{Name: "2", Path: "doc.pdf"},
			},
		},
		{
			name:  "gcs uri with equals",
			files: []string{"gs://bucket/key=value"},
			want:  []FileInput{{Name: "1", Path: "gs://bucket/key=value"}},
		},
		{
			name:  "mixed",
			files: []string{"a=img.jpg", "doc.pdf", "b=other.png"},
			want: []FileInput{
				{Name: "a", Path: "img.jpg"},
				{Name: "1", Path: "doc.pdf"},
				{Name: "b", Path: "other.png"},
			},
		},
		{
			name:  "path with slash before equals",
			files: []string{"path/to/file=name.jpg"},
			want:  []FileInput{{Name: "1", Path: "path/to/file=name.jpg"}},
		},
		{
			name:  "name with dot rejected",
			files: []string{"file.name=value"},
			want:  []FileInput{{Name: "1", Path: "file.name=value"}},
		},
		{
			name:  "empty",
			files: nil,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFileInputs(tt.files)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d inputs, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("input[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestJSONFileInputsToFileInputs(t *testing.T) {
	jFiles := []jsonFileInput{
		{Name: "a", Path: "img1.jpg"},
		{Path: "doc.pdf"},
		{Name: "b", Path: "img2.jpg"},
	}
	got := JSONFileInputsToFileInputs(jFiles)
	want := []FileInput{
		{Name: "a", Path: "img1.jpg"},
		{Name: "1", Path: "doc.pdf"},
		{Name: "b", Path: "img2.jpg"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d inputs, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("input[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBuildFileParts_LocalFile(t *testing.T) {
	dir := t.TempDir()

	// Known extension
	png := filepath.Join(dir, "test.png")
	os.WriteFile(png, []byte("fake png data"), 0644)
	parts, err := BuildFileParts(FileInput{Name: "img", Path: png})
	if err != nil {
		t.Fatalf("BuildFileParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	if parts[0].Text != `<FILE name="img">` {
		t.Errorf("opening tag = %q", parts[0].Text)
	}
	if parts[2].Text != "</FILE>" {
		t.Errorf("closing tag = %q", parts[2].Text)
	}

	// Unknown extension — should use http.DetectContentType fallback
	unknown := filepath.Join(dir, "data.xyz")
	os.WriteFile(unknown, []byte("hello world"), 0644)
	parts, err = BuildFileParts(FileInput{Name: "f", Path: unknown})
	if err != nil {
		t.Fatalf("BuildFileParts unknown ext: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
}

func TestBuildFileParts_Stdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("stdin content here"))
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	parts, err := BuildFileParts(FileInput{Name: "stdin", Path: "-"})
	if err != nil {
		t.Fatalf("BuildFileParts stdin: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	if parts[0].Text != `<FILE name="stdin">` {
		t.Errorf("opening tag = %q", parts[0].Text)
	}
}

func TestBuildFileParts_MissingFile(t *testing.T) {
	_, err := BuildFileParts(FileInput{Name: "x", Path: "/nonexistent/file.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestIsYouTubeURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://YOUTUBE.COM/watch?v=abc", true},
		{"https://example.com/video", false},
		{"gs://bucket/video.mp4", false},
	}
	for _, tt := range tests {
		if got := isYouTubeURL(tt.url); got != tt.want {
			t.Errorf("isYouTubeURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestBuildContents_SinglePrompt(t *testing.T) {
	contents := BuildContents("hello", nil, nil)
	if len(contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("role = %q, want user", contents[0].Role)
	}
	if len(contents[0].Parts) != 1 || contents[0].Parts[0].Text != "hello" {
		t.Errorf("parts = %+v", contents[0].Parts)
	}
}

func TestBuildContents_WithFileParts(t *testing.T) {
	fileParts := []*genai.Part{genai.NewPartFromText("<FILE>"), genai.NewPartFromText("</FILE>")}
	contents := BuildContents("describe this", nil, fileParts)
	if len(contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(contents))
	}
	if len(contents[0].Parts) != 3 {
		t.Fatalf("got %d parts, want 3 (2 file + 1 prompt)", len(contents[0].Parts))
	}
	if contents[0].Parts[2].Text != "describe this" {
		t.Errorf("last part = %q", contents[0].Parts[2].Text)
	}
}

func TestBuildContents_MultiTurn(t *testing.T) {
	messages := []JSONMessage{
		{Role: "user", Content: "What is Go?"},
		{Role: "model", Content: "Go is a programming language."},
		{Role: "user", Content: "Tell me more."},
	}
	contents := BuildContents("", messages, nil)
	if len(contents) != 3 {
		t.Fatalf("got %d contents, want 3", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("contents[0].Role = %q", contents[0].Role)
	}
	if contents[1].Role != "model" {
		t.Errorf("contents[1].Role = %q", contents[1].Role)
	}
	if contents[2].Parts[0].Text != "Tell me more." {
		t.Errorf("contents[2] text = %q", contents[2].Parts[0].Text)
	}
}

func TestBuildContents_MessagesWithPrompt(t *testing.T) {
	messages := []JSONMessage{
		{Role: "user", Content: "What is Go?"},
		{Role: "model", Content: "Go is a programming language."},
	}
	contents := BuildContents("Now explain generics.", messages, nil)
	if len(contents) != 3 {
		t.Fatalf("got %d contents, want 3", len(contents))
	}
	if contents[2].Role != "user" {
		t.Errorf("last role = %q, want user", contents[2].Role)
	}
	if contents[2].Parts[0].Text != "Now explain generics." {
		t.Errorf("last text = %q", contents[2].Parts[0].Text)
	}
}

func TestBuildContents_MessagesWithFiles(t *testing.T) {
	messages := []JSONMessage{
		{Role: "user", Content: "Describe this image."},
	}
	fileParts := []*genai.Part{genai.NewPartFromText("<FILE>"), genai.NewPartFromText("</FILE>")}
	contents := BuildContents("", messages, fileParts)
	if len(contents) != 1 {
		t.Fatalf("got %d contents, want 1", len(contents))
	}
	if len(contents[0].Parts) != 3 {
		t.Fatalf("got %d parts, want 3 (2 file + 1 text)", len(contents[0].Parts))
	}
}

func TestLoadJSONRequest_WithMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.json")
	os.WriteFile(path, []byte(`{
		"messages": [
			{"role": "user", "content": "What is Go?"},
			{"role": "model", "content": "Go is a programming language."},
			{"role": "user", "content": "Tell me more."}
		]
	}`), 0644)

	req, err := LoadJSONRequest(path)
	if err != nil {
		t.Fatalf("LoadJSONRequest: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(req.Messages))
	}
	if req.Messages[0].Role != "user" || req.Messages[0].Content != "What is Go?" {
		t.Errorf("messages[0] = %+v", req.Messages[0])
	}
}

func TestLoadJSONRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.json")
	os.WriteFile(path, []byte(`{"prompt":"hello","model":"gemini-2.5-flash","search":true,"files":[{"name":"a","path":"img.jpg"}]}`), 0644)

	req, err := LoadJSONRequest(path)
	if err != nil {
		t.Fatalf("LoadJSONRequest: %v", err)
	}
	if req.Prompt != "hello" {
		t.Errorf("prompt = %q", req.Prompt)
	}
	if req.Model != "gemini-2.5-flash" {
		t.Errorf("model = %q", req.Model)
	}
	if !req.Search {
		t.Error("search should be true")
	}
	if len(req.Files) != 1 || req.Files[0].Name != "a" {
		t.Errorf("files = %+v", req.Files)
	}
}

func TestLoadJSONRequest_WithSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.json")
	os.WriteFile(path, []byte(`{"prompt":"extract","schema":{"type":"OBJECT","properties":{"name":{"type":"STRING"}}}}`), 0644)

	req, err := LoadJSONRequest(path)
	if err != nil {
		t.Fatalf("LoadJSONRequest: %v", err)
	}
	if req.Schema == nil {
		t.Fatal("schema should not be nil")
	}
	if req.Schema.Type != "OBJECT" {
		t.Errorf("schema type = %q, want OBJECT", req.Schema.Type)
	}
	if _, ok := req.Schema.Properties["name"]; !ok {
		t.Error("schema missing 'name' property")
	}
}

func TestLoadJSONRequest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{invalid`), 0644)

	_, err := LoadJSONRequest(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetchURL(t *testing.T) {
	t.Run("success with content-type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("fake png"))
		}))
		defer srv.Close()

		data, mime, err := fetchURL(srv.URL + "/photo.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != "fake png" {
			t.Errorf("data = %q", data)
		}
		if mime != "image/jpeg" {
			t.Errorf("mime = %q, want image/jpeg (from extension)", mime)
		}
	})

	t.Run("mime from content-type when no extension", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf; charset=utf-8")
			w.Write([]byte("fake pdf"))
		}))
		defer srv.Close()

		_, mime, err := fetchURL(srv.URL + "/document")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mime != "application/pdf" {
			t.Errorf("mime = %q, want application/pdf", mime)
		}
	})

	t.Run("404 error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
		}))
		defer srv.Close()

		_, _, err := fetchURL(srv.URL + "/missing")
		if err == nil {
			t.Fatal("expected error for 404")
		}
		if !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("error = %q, want mention of HTTP 404", err.Error())
		}
	})
}

func TestParseGenerateResult(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		ModelVersion: "gemini-3.5-flash-001",
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 42,
		},
		Candidates: []*genai.Candidate{
			{
				FinishReason: genai.FinishReasonStop,
				Content: &genai.Content{
					Parts: []*genai.Part{
						genai.NewPartFromText("Hello "),
						genai.NewPartFromText("world"),
					},
				},
				GroundingMetadata: &genai.GroundingMetadata{
					WebSearchQueries: []string{"go news"},
					GroundingChunks: []*genai.GroundingChunk{
						{Web: &genai.GroundingChunkWeb{Title: "A", URI: "https://a.example", Domain: "a.example"}},
						{Web: &genai.GroundingChunkWeb{Title: "A dup", URI: "https://a.example", Domain: "a.example"}},
						{Web: &genai.GroundingChunkWeb{Title: "B", URI: "https://b.example", Domain: "b.example"}},
						{Web: &genai.GroundingChunkWeb{URI: ""}}, // empty URI ignored
						{Web: nil},                               // nil web ignored
					},
				},
			},
		},
	}

	got := parseGenerateResult(resp)

	if got.Text != "Hello world" {
		t.Errorf("text = %q, want %q", got.Text, "Hello world")
	}
	if got.Model != "gemini-3.5-flash-001" {
		t.Errorf("model = %q", got.Model)
	}
	if got.FinishReason != "STOP" {
		t.Errorf("finish_reason = %q, want STOP", got.FinishReason)
	}
	if got.PromptTokens != 10 || got.OutputTokens != 42 {
		t.Errorf("tokens = %d/%d, want 10/42", got.PromptTokens, got.OutputTokens)
	}
	// Duplicate URI must be collapsed: expect a.example once, b.example once.
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 (deduped by URI)", len(got.Sources))
	}
	if got.Sources[0].URI != "https://a.example" || got.Sources[1].URI != "https://b.example" {
		t.Errorf("sources = %+v", got.Sources)
	}
	if len(got.WebSearchQueries) != 1 || got.WebSearchQueries[0] != "go news" {
		t.Errorf("web_search_queries = %+v", got.WebSearchQueries)
	}
}

func TestParseImageResult(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		ModelVersion: "img-model",
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{
				genai.NewPartFromText("here you go"),
				genai.NewPartFromBytes([]byte("PNGDATA"), "image/png"),
			}}},
		},
	}
	got, err := parseImageResult(resp)
	if err != nil {
		t.Fatalf("parseImageResult: %v", err)
	}
	if string(got.Data) != "PNGDATA" {
		t.Errorf("data = %q", got.Data)
	}
	if got.MIMEType != "image/png" {
		t.Errorf("mime = %q", got.MIMEType)
	}
	if got.Text != "here you go" {
		t.Errorf("text = %q", got.Text)
	}
	if got.ModelVersion != "img-model" {
		t.Errorf("model = %q", got.ModelVersion)
	}
}

func TestParseImageResult_NoImage(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText("only text")}}},
		},
	}
	if _, err := parseImageResult(resp); err == nil {
		t.Fatal("expected error when no image is returned")
	}
}

func TestParseSpeechResult(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		ModelVersion: "tts-model",
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{
				genai.NewPartFromBytes([]byte("PCMDATA"), "audio/L16"),
			}}},
		},
	}
	got, err := parseSpeechResult(resp)
	if err != nil {
		t.Fatalf("parseSpeechResult: %v", err)
	}
	if string(got.PCMData) != "PCMDATA" {
		t.Errorf("pcm = %q", got.PCMData)
	}
	if got.ModelVersion != "tts-model" {
		t.Errorf("model = %q", got.ModelVersion)
	}
}

func TestParseSpeechResult_NoAudio(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText("no audio")}}},
		},
	}
	if _, err := parseSpeechResult(resp); err == nil {
		t.Fatal("expected error when no audio is returned")
	}
}

func TestBuildFileParts_HTTPURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello from server"))
	}))
	defer srv.Close()

	parts, err := BuildFileParts(FileInput{Name: "web", Path: srv.URL + "/test.txt"})
	if err != nil {
		t.Fatalf("BuildFileParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	if parts[0].Text != `<FILE name="web">` {
		t.Errorf("opening tag = %q", parts[0].Text)
	}
}
