package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gemini"
)

func main() {
	cli.RunMain("jeep-image", run)
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, output string
	var listModels bool
	var files []string
	var common cli.CommonFlags

	fs := cli.NewFlagSet("jeep-image", stderr)
	common.Register(fs, 5*time.Minute)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVarP(&output, "output", "o", "output.png", "Output file path")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.StringArrayVarP(&files, "file", "f", nil, "Reference image (repeatable)")
	fs.Usage = func() { printUsage(fs, stderr) }
	if done, err := cli.Parse(fs, args, "jeep-image", stdout, &common); err != nil || done {
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

	prompt, err := cli.ResolvePrompt(fs.Args())
	if err != nil {
		return cli.Exitf(cli.ExitUsage, "%w", err)
	}

	if model == "" {
		model = config.ResolveModelWithConfig(cfg, "IMAGE")
	}

	inputs := gemini.ParseFileInputs(files)

	if common.DryRun {
		var dryFiles []cli.DryRunFile
		for _, fi := range inputs {
			dryFiles = append(dryFiles, cli.DryRunFile{Name: fi.Name, Path: fi.Path})
		}
		info := &cli.DryRunInfo{
			Tool:      "jeep-image",
			Model:     model,
			Project:   common.Project,
			Region:    common.Region,
			PromptLen: len(prompt),
			Files:     cli.ResolveDryRunFiles(dryFiles),
		}
		return info.Print(stdout, common.Format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
	defer cancel()

	result, err := gemini.GenerateImage(ctx, common.Project, common.Region, model, prompt, inputs, common.Retries)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	ext := extFromMIME(result.MIMEType)
	if ext != "" && filepath.Ext(output) == "" {
		output += ext
	} else if ext != "" && filepath.Ext(output) != ext && !common.Quiet {
		fmt.Fprintf(stderr, "Warning: model returned %s but output file is %s\n", result.MIMEType, filepath.Ext(output))
	}

	if err := os.WriteFile(output, result.Data, 0644); err != nil {
		return cli.Exitf(cli.ExitIO, "writing output: %w", err)
	}

	switch common.Format {
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
		if !common.Quiet {
			fmt.Fprintf(stderr, "Saved %s (%d bytes)\n", output, len(result.Data))
		}
		if result.Text != "" {
			fmt.Fprintln(stdout, result.Text)
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
