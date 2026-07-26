package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type DryRunFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	Remote bool   `json:"remote,omitempty"`
}

type DryRunInfo struct {
	Tool      string         `json:"tool"`
	Model     string         `json:"model"`
	Project   string         `json:"project"`
	Region    string         `json:"region"`
	PromptLen int            `json:"prompt_length"`
	Files     []DryRunFile   `json:"files,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

func ResolveDryRunFiles(files []DryRunFile) []DryRunFile {
	for i := range files {
		path := files[i].Path
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") ||
			strings.HasPrefix(path, "gs://") {
			files[i].Remote = true
		} else {
			if info, err := os.Stat(path); err == nil {
				files[i].Size = info.Size()
			}
		}
	}
	return files
}

func (d *DryRunInfo) Print(w io.Writer, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}

	fmt.Fprintf(w, "Tool:      %s\n", d.Tool)
	fmt.Fprintf(w, "Model:     %s\n", d.Model)
	fmt.Fprintf(w, "Project:   %s\n", d.Project)
	fmt.Fprintf(w, "Region:    %s\n", d.Region)
	fmt.Fprintf(w, "Prompt:    %d chars\n", d.PromptLen)
	if len(d.Files) > 0 {
		fmt.Fprintf(w, "Files:     %d\n", len(d.Files))
		for _, f := range d.Files {
			if f.Remote {
				fmt.Fprintf(w, "  - %s (remote)\n", f.Path)
			} else {
				fmt.Fprintf(w, "  - %s (%d bytes)\n", f.Path, f.Size)
			}
		}
	}
	for k, v := range d.Extra {
		fmt.Fprintf(w, "%-10s %v\n", k+":", v)
	}
	return nil
}
