package main

import (
	"context"
	"encoding/binary"
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
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "jeep-tts: %v\n", err)
		code := 1
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, project, region, voice, output, format string
	var showVersion, quiet, listVoices, listModels, dryRun bool
	var timeout time.Duration
	var retries int

	fs := flag.NewFlagSet("jeep-tts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVar(&project, "project", "", "GCP project (default: auto-detect)")
	fs.StringVar(&region, "region", "global", "Vertex AI region")
	fs.StringVarP(&voice, "voice", "v", "Kore", "Voice name")
	fs.StringVarP(&output, "output", "o", "output.wav", "Output WAV file")
	fs.StringVar(&format, "format", "text", "Output format: text or json")
	fs.BoolVarP(&quiet, "quiet", "q", false, "Suppress stderr messages")
	fs.BoolVar(&dryRun, "dry-run", false, "Show what would be sent without making the API call")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.BoolVar(&listVoices, "list-voices", false, "List available voices and exit")
	fs.BoolVar(&showVersion, "version", false, "Print version and exit")
	fs.DurationVar(&timeout, "timeout", 5*time.Minute, "Request timeout")
	fs.IntVar(&retries, "retry", 0, "Retry transient errors with exponential backoff")
	fs.Usage = func() { printUsage(fs, stderr) }
	if err := fs.Parse(args); err != nil {
		return &cli.ExitError{Code: cli.ExitUsage, Err: err}
	}

	if showVersion {
		fmt.Fprintf(stdout, "jeep-tts %s\n", version.String())
		return nil
	}

	if listVoices {
		return printVoices(stdout, format)
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
		model = config.ResolveModel("TTS")
	}

	if dryRun {
		info := &cli.DryRunInfo{
			Tool:      "jeep-tts",
			Model:     model,
			Project:   project,
			Region:    region,
			PromptLen: len(prompt),
			Extra:     map[string]any{"voice": voice},
		}
		return info.Print(stdout, format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := gemini.GenerateSpeech(ctx, project, region, model, voice, prompt, retries)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	wav := pcmToWAV(result.PCMData, 24000, 1, 16)

	if err := os.WriteFile(output, wav, 0644); err != nil {
		return cli.Exitf(cli.ExitIO, "writing output: %w", err)
	}

	duration := float64(len(result.PCMData)) / 2 / 24000

	switch format {
	case "json":
		out := struct {
			Output   string  `json:"output"`
			Size     int     `json:"size"`
			Duration float64 `json:"duration_seconds"`
			Voice    string  `json:"voice"`
			Model    string  `json:"model,omitempty"`
		}{
			Output:   output,
			Size:     len(wav),
			Duration: duration,
			Voice:    voice,
			Model:    result.ModelVersion,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		if !quiet {
			fmt.Fprintf(stderr, "Saved %s (%d bytes, %.1fs)\n", output, len(wav), duration)
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

type voiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

var voices = []voiceInfo{
	{"Zephyr", "bright"}, {"Puck", "upbeat"}, {"Charon", "informative"},
	{"Kore", "firm"}, {"Fenrir", "excitable"}, {"Leda", "youthful"},
	{"Orus", "firm"}, {"Aoede", "breezy"}, {"Callirrhoe", "easy-going"},
	{"Autonoe", "bright"}, {"Enceladus", "breathy"}, {"Iapetus", "clear"},
	{"Umbriel", "easy-going"}, {"Algieba", "smooth"}, {"Despina", "smooth"},
	{"Erinome", "clear"}, {"Algenib", "gravelly"}, {"Rasalgethi", "informative"},
	{"Laomedeia", "upbeat"}, {"Achernar", "soft"}, {"Alnilam", "firm"},
	{"Schedar", "even"}, {"Gacrux", "mature"}, {"Pulcherrima", "forward"},
	{"Achird", "friendly"}, {"Zubenelgenubi", "casual"}, {"Vindemiatrix", "gentle"},
	{"Sadachbia", "lively"}, {"Sadaltager", "knowledgeable"}, {"Sulafat", "warm"},
}

func printVoices(w io.Writer, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(voices)
	}
	for _, v := range voices {
		fmt.Fprintf(w, "%-16s %s\n", v.Name, v.Description)
	}
	return nil
}

func pcmToWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	dataSize := len(pcm)
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcm)
	return buf
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Usage: jeep-tts [flags] "text to speak"

Generate speech audio from text using Gemini TTS via Vertex AI.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Available voices:
  Zephyr (bright)       Puck (upbeat)         Charon (informative)
  Kore (firm)           Fenrir (excitable)     Leda (youthful)
  Orus (firm)           Aoede (breezy)         Callirrhoe (easy-going)
  Autonoe (bright)      Enceladus (breathy)    Iapetus (clear)
  Umbriel (easy-going)  Algieba (smooth)       Despina (smooth)
  Erinome (clear)       Algenib (gravelly)     Rasalgethi (informative)
  Laomedeia (upbeat)    Achernar (soft)        Alnilam (firm)
  Schedar (even)        Gacrux (mature)        Pulcherrima (forward)
  Achird (friendly)     Zubenelgenubi (casual)  Vindemiatrix (gentle)
  Sadachbia (lively)    Sadaltager (knowledgeable) Sulafat (warm)

Examples:
  jeep-tts "Hello, how are you today?"
  jeep-tts -v Puck "Welcome to the show!" -o intro.wav
  jeep-tts -v Charon "Read this clearly: The meeting is at 3pm."
  echo "Say cheerfully: Have a wonderful day!" | jeep-tts -o greeting.wav
`)
}
