package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gcp"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/version"
	"google.golang.org/genai"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "jeep: %v\n", err)
		code := 1
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, system, project, region, jsonFile, schemaFile, format string
	var search, showVersion, quiet, dryRun, listModels bool
	var timeout time.Duration
	var files []string
	var temperature, topP float64
	var maxTokens int32
	var retries int

	fs := flag.NewFlagSet("jeep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVar(&system, "system", "", "System instruction")
	fs.StringVar(&project, "project", "", "GCP project (default: auto-detect)")
	fs.StringVar(&region, "region", "global", "Vertex AI region")
	fs.StringVar(&jsonFile, "json", "", "Load all parameters from a JSON file")
	fs.StringVar(&schemaFile, "schema", "", "JSON schema file for structured output")
	fs.StringVar(&format, "format", "text", "Output format: text or json")
	fs.BoolVar(&search, "search", false, "Ground responses with Google Search")
	fs.BoolVarP(&quiet, "quiet", "q", false, "Suppress stderr messages")
	fs.BoolVar(&dryRun, "dry-run", false, "Show what would be sent without making the API call")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.BoolVar(&showVersion, "version", false, "Print version and exit")
	fs.DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")
	fs.StringArrayVarP(&files, "file", "f", nil, "File, YouTube URL, or gs:// URI (repeatable)")
	fs.Float64Var(&temperature, "temperature", -1, "Sampling temperature (0-2, default: model default)")
	fs.Float64Var(&topP, "top-p", -1, "Top-p sampling (0-1, default: model default)")
	fs.Int32Var(&maxTokens, "max-tokens", 0, "Maximum output tokens")
	fs.IntVar(&retries, "retry", 0, "Retry transient errors with exponential backoff")
	fs.Usage = func() { printUsage(fs, stderr) }
	if err := fs.Parse(args); err != nil {
		return &cli.ExitError{Code: cli.ExitUsage, Err: err}
	}

	if showVersion {
		fmt.Fprintf(stdout, "jeep %s\n", version.String())
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

	var prompt string
	var inputs []gemini.FileInput
	var schema *genai.Schema
	var err error

	if jsonFile != "" {
		req, err := gemini.LoadJSONRequest(jsonFile)
		if err != nil {
			return cli.Exitf(cli.ExitUsage, "%w", err)
		}
		prompt = req.Prompt
		if prompt == "" {
			return cli.Exitf(cli.ExitUsage, "JSON file missing required \"prompt\" field")
		}
		if req.Model != "" {
			model = req.Model
		}
		if req.System != "" {
			system = req.System
		}
		if req.Project != "" {
			project = req.Project
		}
		if req.Region != "" {
			region = req.Region
		}
		if req.Search {
			search = true
		}
		if req.Schema != nil {
			schema = req.Schema
		}
		if req.Temperature != nil && temperature < 0 {
			temperature = *req.Temperature
		}
		if req.TopP != nil && topP < 0 {
			topP = *req.TopP
		}
		if req.MaxTokens > 0 && maxTokens == 0 {
			maxTokens = req.MaxTokens
		}
		inputs = req.FileInputs()
	} else {
		prompt, err = cli.ResolvePrompt(fs.Args())
		if err != nil {
			return cli.Exitf(cli.ExitUsage, "%w", err)
		}
		inputs = gemini.ParseFileInputs(files)
	}

	if schemaFile != "" {
		schema, err = loadSchema(schemaFile)
		if err != nil {
			return cli.Exitf(cli.ExitUsage, "%w", err)
		}
	}

	if model == "" {
		model = config.ResolveModel("TEXT")
	}

	if dryRun {
		var dryFiles []cli.DryRunFile
		for _, fi := range inputs {
			dryFiles = append(dryFiles, cli.DryRunFile{Name: fi.Name, Path: fi.Path})
		}
		extra := map[string]any{"search": search}
		if schema != nil {
			extra["schema"] = true
		}
		if temperature >= 0 {
			extra["temperature"] = temperature
		}
		if topP >= 0 {
			extra["top_p"] = topP
		}
		if maxTokens > 0 {
			extra["max_tokens"] = maxTokens
		}
		if system != "" {
			extra["system"] = system
		}
		info := &cli.DryRunInfo{
			Tool:      "jeep",
			Model:     model,
			Project:   project,
			Region:    region,
			PromptLen: len(prompt),
			Files:     cli.ResolveDryRunFiles(dryFiles),
			Extra:     extra,
		}
		return info.Print(stdout, format)
	}

	opts := &gemini.GenerateOptions{Retries: retries}
	if temperature >= 0 {
		opts.Temperature = &temperature
	}
	if topP >= 0 {
		opts.TopP = &topP
	}
	if maxTokens > 0 {
		opts.MaxTokens = maxTokens
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := gemini.Generate(ctx, project, region, model, system, prompt, inputs, search, schema, opts)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	switch format {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		fmt.Fprintln(stdout, result.Text)
		if !quiet && len(result.Sources) > 0 {
			seen := map[string]bool{}
			fmt.Fprintln(stderr, "\nSources:")
			for _, s := range result.Sources {
				if seen[s.URI] {
					continue
				}
				seen[s.URI] = true
				fmt.Fprintf(stderr, "  - %s\n", s.URI)
			}
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

func loadSchema(path string) (*genai.Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}
	var schema genai.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema file: %w", err)
	}
	return &schema, nil
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Usage: jeep [flags] "prompt" [-f [name=]file ...]

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Examples:
  jeep "hello world"
  jeep "describe this" -f photo.jpg
  jeep "compare a vs b" -f a=img1.jpg -f b=img2.jpg
  jeep "summarize" -f https://youtube.com/watch?v=xxx
  jeep "analyze" -f gs://bucket/doc.pdf
  echo "explain this code" | jeep -f main.go
  jeep --system "Be concise" "what is this?" -f image.png
  jeep --search "latest news about Go programming language"
  jeep --json request.json
  jeep --schema person.json "Extract name and age" -f doc.txt
  jeep --format json "hello world"
  jeep --format json --search "latest Go news"
`)
}
