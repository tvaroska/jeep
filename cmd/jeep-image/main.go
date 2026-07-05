package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gcp"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "jeep-image: %v\n", err)
		code := 1
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, project, region, output, format string
	var showVersion, quiet, dryRun, listModels bool
	var timeout time.Duration
	var files []string
	var retries int

	fs := flag.NewFlagSet("jeep-image", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVar(&project, "project", "", "GCP project (default: auto-detect)")
	fs.StringVar(&region, "region", "global", "Vertex AI region")
	fs.StringVarP(&output, "output", "o", "output.png", "Output file path")
	fs.StringVar(&format, "format", "text", "Output format: text or json")
	fs.BoolVarP(&quiet, "quiet", "q", false, "Suppress stderr messages")
	fs.BoolVar(&dryRun, "dry-run", false, "Show what would be sent without making the API call")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.BoolVar(&showVersion, "version", false, "Print version and exit")
	fs.DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")
	fs.StringArrayVarP(&files, "file", "f", nil, "Reference image (repeatable)")
	fs.IntVar(&retries, "retry", 0, "Retry transient errors with exponential backoff")
	fs.Usage = func() { printUsage(fs, stderr) }
	if err := fs.Parse(args); err != nil {
		return &cli.ExitError{Code: cli.ExitUsage, Err: err}
	}

	if showVersion {
		fmt.Fprintf(stdout, "jeep-image %s\n", version.String())
		return nil
	}

	cfg := config.Load()
	if !quiet && cfg.Quiet {
		quiet = true
	}
	if region == "global" && !fs.Changed("region") && cfg.Region != "" {
		region = cfg.Region
	}
	if project == "" && cfg.Project != "" {
		project = cfg.Project
	}
	if project == "" {
		project = gcp.ResolveProject()
	}
	if project == "" {
		return cli.Exitf(cli.ExitConfig, "could not determine GCP project; set GOOGLE_CLOUD_PROJECT or pass --project")
	}

	if listModels {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return printModels(ctx, stdout, project, region, format)
	}

	prompt, err := cli.ResolvePrompt(fs.Args())
	if err != nil {
		return cli.Exitf(cli.ExitUsage, "%w", err)
	}

	if model == "" {
		model = config.ResolveModel("IMAGE")
	}

	inputs := gemini.ParseFileInputs(files)

	if dryRun {
		var dryFiles []cli.DryRunFile
		for _, fi := range inputs {
			dryFiles = append(dryFiles, cli.DryRunFile{Name: fi.Name, Path: fi.Path})
		}
		info := &cli.DryRunInfo{
			Tool:      "jeep-image",
			Model:     model,
			Project:   project,
			Region:    region,
			PromptLen: len(prompt),
			Files:     cli.ResolveDryRunFiles(dryFiles),
		}
		return info.Print(stdout, format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := gemini.GenerateImage(ctx, project, region, model, prompt, inputs, retries)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	ext := extFromMIME(result.MIMEType)
	if ext != "" && filepath.Ext(output) == "" {
		output += ext
	} else if ext != "" && filepath.Ext(output) != ext && !quiet {
		fmt.Fprintf(stderr, "Warning: model returned %s but output file is %s\n", result.MIMEType, filepath.Ext(output))
	}

	if err := os.WriteFile(output, result.Data, 0644); err != nil {
		return cli.Exitf(cli.ExitIO, "writing output: %w", err)
	}

	switch format {
	case "json":
		out := struct {
			Output   string `json:"output"`
			Size     int    `json:"size"`
			MIMEType string `json:"mime_type"`
			Text     string `json:"text,omitempty"`
			Model    string `json:"model,omitempty"`
		}{
			Output:   output,
			Size:     len(result.Data),
			MIMEType: result.MIMEType,
			Text:     result.Text,
			Model:    result.ModelVersion,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		if !quiet {
			fmt.Fprintf(stderr, "Saved %s (%d bytes)\n", output, len(result.Data))
		}
		if result.Text != "" {
			fmt.Fprintln(stdout, result.Text)
		}
	}
	return nil
}

func printModels(ctx context.Context, w io.Writer, project, region, format string) error {
	models, err := gemini.ListModels(ctx, project, region)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(models)
	}
	for _, m := range models {
		if m.DisplayName != "" {
			fmt.Fprintf(w, "%-40s %s\n", m.Name, m.DisplayName)
		} else {
			fmt.Fprintln(w, m.Name)
		}
	}
	return nil
}

func extFromMIME(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpeg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Usage: jeep-image [flags] "prompt" [-f [name=]file ...]

Generate images using Gemini via Vertex AI.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Examples:
  jeep-image "a cat astronaut floating in space"
  jeep-image "a cat astronaut" -o cat.png
  jeep-image "make this sketch realistic" -f sketch.jpg
  jeep-image "combine these styles" -f a=style.jpg -f b=content.jpg
  echo "a sunset over mountains" | jeep-image -o sunset.png
`)
}
