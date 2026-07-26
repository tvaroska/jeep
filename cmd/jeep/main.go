package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gemini"
	"google.golang.org/genai"
)

func main() {
	cli.RunMain("jeep", run)
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, system, jsonFile, schemaFile string
	var search, listModels bool
	var files []string
	var temperature, topP float64
	var maxTokens int32
	var common cli.CommonFlags

	fs := cli.NewFlagSet("jeep", stderr)
	common.Register(fs, 5*time.Minute)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVar(&system, "system", "", "System instruction")
	fs.StringVar(&jsonFile, "json", "", "Load all parameters from a JSON file")
	fs.StringVar(&schemaFile, "schema", "", "JSON schema file for structured output")
	fs.BoolVar(&search, "search", false, "Ground responses with Google Search")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.StringArrayVarP(&files, "file", "f", nil, "File, YouTube URL, or gs:// URI (repeatable)")
	fs.Float64Var(&temperature, "temperature", -1, "Sampling temperature (0-2, default: model default)")
	fs.Float64Var(&topP, "top-p", -1, "Top-p sampling (0-1, default: model default)")
	fs.Int32Var(&maxTokens, "max-tokens", 0, "Maximum output tokens")
	fs.Usage = func() { printUsage(fs, stderr) }
	if done, err := cli.Parse(fs, args, "jeep", stdout, &common); err != nil || done {
		return err
	}

	cfg, err := cli.ResolveCommon(fs, &common.Project, &common.Region, &common.Quiet)
	if err != nil {
		return err
	}

	if listModels {
		ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
		defer cancel()
		return cli.PrintModels(ctx, stdout, common.Project, common.Region, common.Format)
	}

	var prompt string
	var messages []gemini.JSONMessage
	var inputs []gemini.FileInput
	var schema *genai.Schema

	if jsonFile != "" {
		req, err := gemini.LoadJSONRequest(jsonFile)
		if err != nil {
			return cli.Exitf(cli.ExitUsage, "%w", err)
		}
		prompt = req.Prompt
		messages = req.Messages
		if prompt == "" && len(messages) == 0 {
			return cli.Exitf(cli.ExitUsage, "JSON file must contain \"prompt\" or \"messages\"")
		}
		if req.Model != "" {
			model = req.Model
		}
		if req.System != "" {
			system = req.System
		}
		if req.Project != "" {
			common.Project = req.Project
		}
		if req.Region != "" {
			common.Region = req.Region
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
		model = config.ResolveModelWithConfig(cfg, "TEXT")
	}

	if common.DryRun {
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
			Project:   common.Project,
			Region:    common.Region,
			PromptLen: len(prompt),
			Files:     cli.ResolveDryRunFiles(dryFiles),
			Extra:     extra,
		}
		return info.Print(stdout, common.Format)
	}

	opts := &gemini.GenerateOptions{Retries: common.Retries}
	if temperature >= 0 {
		opts.Temperature = &temperature
	}
	if topP >= 0 {
		opts.TopP = &topP
	}
	if maxTokens > 0 {
		opts.MaxTokens = maxTokens
	}

	fileParts, err := gemini.BuildAllFileParts(inputs)
	if err != nil {
		return cli.Exitf(cli.ExitIO, "%w", err)
	}
	contents := gemini.BuildContents(prompt, messages, fileParts)

	ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
	defer cancel()

	result, err := gemini.Generate(ctx, common.Project, common.Region, model, system, contents, search, schema, opts)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	switch common.Format {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		fmt.Fprintln(stdout, result.Text)
		if !common.Quiet && len(result.Sources) > 0 {
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
  cat output.log | jeep "analyze this" -f -
`)
}
