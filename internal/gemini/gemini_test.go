package gemini

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
