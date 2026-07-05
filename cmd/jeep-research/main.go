package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gcp"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/interactions"
	"github.com/tvaroska/jeep/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "jeep-research: %v\n", err)
		code := 1
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var agent, project, region, format string
	var showVersion, quiet, dryRun bool
	var timeout time.Duration

	fs := flag.NewFlagSet("jeep-research", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&agent, "agent", "", "Agent name")
	fs.StringVar(&project, "project", "", "GCP project (default: auto-detect)")
	fs.StringVar(&region, "region", "global", "Vertex AI region")
	fs.StringVar(&format, "format", "text", "Output format: text or json")
	fs.BoolVarP(&quiet, "quiet", "q", false, "Suppress stderr messages")
	fs.BoolVar(&dryRun, "dry-run", false, "Show what would be sent without making the API call")
	fs.BoolVar(&showVersion, "version", false, "Print version and exit")
	fs.DurationVar(&timeout, "timeout", 30*time.Minute, "Request timeout")
	fs.Usage = func() { printUsage(fs, stderr) }
	if err := fs.Parse(args); err != nil {
		return &cli.ExitError{Code: cli.ExitUsage, Err: err}
	}

	if showVersion {
		fmt.Fprintf(stdout, "jeep-research %s\n", version.String())
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

	prompt, err := cli.ResolvePrompt(fs.Args())
	if err != nil {
		return cli.Exitf(cli.ExitUsage, "%w", err)
	}

	if agent == "" {
		agent = config.ResolveModel("RESEARCH")
	}

	if dryRun {
		info := &cli.DryRunInfo{
			Tool:      "jeep-research",
			Model:     agent,
			Project:   project,
			Region:    region,
			PromptLen: len(prompt),
		}
		return info.Print(stdout, format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := interactions.NewClient(ctx, project, region)
	if err != nil {
		return cli.Exitf(cli.ExitConfig, "%w", err)
	}

	var onStatus func(string)
	if !quiet {
		onStatus = func(s string) { fmt.Fprintf(stderr, "%s\n", s) }
	}

	interaction, err := client.RunAndWait(ctx, &interactions.CreateRequest{
		Agent:      agent,
		Input:      prompt,
		Background: true,
	}, onStatus)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	report := interaction.ReportText()
	sources := interaction.Sources()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for i := range sources {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sources[idx].URL = gemini.ResolveRedirect(sources[idx].URL)
		}(i)
	}
	wg.Wait()

	switch format {
	case "json":
		out := researchOutput{
			Text:    report,
			Agent:   agent,
			Status:  interaction.Status,
			Sources: sources,
		}
		if interaction.Usage != nil {
			out.InputTokens = interaction.Usage.TotalInputTokens
			out.OutputTokens = interaction.Usage.TotalOutputTokens
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		fmt.Fprintln(stdout, report)
		if len(sources) > 0 {
			fmt.Fprintln(stdout, "\n## Sources")
			for i, s := range sources {
				if s.Title != "" {
					fmt.Fprintf(stdout, "%d. [%s](%s)\n", i+1, s.Title, s.URL)
				} else {
					fmt.Fprintf(stdout, "%d. %s\n", i+1, s.URL)
				}
			}
		}
	}
	return nil
}

type researchOutput struct {
	Text         string                `json:"text"`
	Agent        string                `json:"agent"`
	Status       string                `json:"status"`
	Sources      []interactions.Source  `json:"sources,omitempty"`
	InputTokens  int                   `json:"input_tokens,omitempty"`
	OutputTokens int                   `json:"output_tokens,omitempty"`
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Usage: jeep-research [flags] "research query"

Run deep research queries using Gemini via Vertex AI.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Examples:
  jeep-research "what is the current state of solid-state batteries?"
  jeep-research --format json "explain CRISPR gene editing"
  jeep-research -q "history of quantum computing"
  echo "analyze recent advances in fusion energy" | jeep-research
`)
}
