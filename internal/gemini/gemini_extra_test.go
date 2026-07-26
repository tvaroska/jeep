package gemini

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONRequest_FileInputs(t *testing.T) {
	req := &JSONRequest{Files: []jsonFileInput{
		{Name: "a", Path: "1.jpg"},
		{Path: "2.jpg"},
	}}
	got := req.FileInputs()
	want := []FileInput{{Name: "a", Path: "1.jpg"}, {Name: "1", Path: "2.jpg"}}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("input[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBuildAllFileParts(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	parts, err := BuildAllFileParts([]FileInput{{Name: "a", Path: f}})
	if err != nil {
		t.Fatalf("BuildAllFileParts: %v", err)
	}
	if len(parts) != 3 { // open tag + bytes + close tag
		t.Errorf("got %d parts, want 3", len(parts))
	}
}

func TestBuildAllFileParts_Error(t *testing.T) {
	_, err := BuildAllFileParts([]FileInput{{Name: "x", Path: "/no/such/file"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadJSONRequest_ReadError(t *testing.T) {
	if _, err := LoadJSONRequest("/no/such/request.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
