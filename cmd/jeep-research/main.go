package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/interactions"
)

func main() {
	cli.RunMain("jeep-research", run)
}

func run(args []string, stdout, stderr io.Writer) error {
	var agent string
	var common cli.CommonFlags

	fs := cli.NewFlagSet("jeep-research", stderr)
	common.Register(fs, 30*time.Minute)
	fs.StringVar(&agent, "agent", "", "Agent name")
	fs.Usage = func() { printUsage(fs, stderr) }
	if done, err := cli.Parse(fs, args, "jeep-research", stdout, &common); err != nil || done {
		return err
	}

	cfg, err := cli.ResolveCommon(fs, &common.Project, &common.Region, &common.Quiet)
	if err != nil {
		return err
	}

	prompt, err := cli.ResolvePrompt(fs.Args())
	if err != nil {
		return cli.Exitf(cli.ExitUsage, "%w", err)
	}

	if agent == "" {
		agent = config.ResolveModelWithConfig(cfg, "RESEARCH")
	}

	if common.DryRun {
		info := &cli.DryRunInfo{
			Tool:      "jeep-research",
			Model:     agent,
			Project:   common.Project,
			Region:    common.Region,
			PromptLen: len(prompt),
		}
		return info.Print(stdout, common.Format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
	defer cancel()

	client, err := interactions.NewClient(ctx, common.Project, common.Region)
	if err != nil {
		return cli.Exitf(cli.ExitConfig, "%w", err)
	}
	client.Retries = common.Retries

	var onStatus func(string)
	if !common.Quiet {
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

	switch common.Format {
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
	Sources      []interactions.Source `json:"sources,omitempty"`
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
