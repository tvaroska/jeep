package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDryRunFiles(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(local, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	files := ResolveDryRunFiles([]DryRunFile{
		{Name: "a", Path: local},
		{Name: "b", Path: "gs://bucket/obj"},
		{Name: "c", Path: "https://example.com/x"},
		{Name: "d", Path: "/does/not/exist"},
	})

	if files[0].Size != 5 || files[0].Remote {
		t.Errorf("local file = %+v, want size 5 not remote", files[0])
	}
	if !files[1].Remote || !files[2].Remote {
		t.Errorf("gs:// and https:// should be remote: %+v %+v", files[1], files[2])
	}
	if files[3].Size != 0 || files[3].Remote {
		t.Errorf("missing local file = %+v, want size 0 not remote", files[3])
	}
}

func TestDryRunInfo_PrintText(t *testing.T) {
	var buf bytes.Buffer
	info := &DryRunInfo{
		Tool: "jeep", Model: "m", Project: "p", Region: "r", PromptLen: 3,
		Files: []DryRunFile{
			{Path: "local.txt", Size: 12},
			{Path: "gs://b/o", Remote: true},
		},
		Extra: map[string]any{"search": true},
	}
	if err := info.Print(&buf, "text"); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"jeep", "local.txt (12 bytes)", "gs://b/o (remote)", "search:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDryRunInfo_PrintJSON(t *testing.T) {
	var buf bytes.Buffer
	info := &DryRunInfo{Tool: "jeep-tts", Model: "m", Project: "p", Region: "r", PromptLen: 1}
	if err := info.Print(&buf, "json"); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got["tool"] != "jeep-tts" {
		t.Errorf("tool = %v, want jeep-tts", got["tool"])
	}
}
