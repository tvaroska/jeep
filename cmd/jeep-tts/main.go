package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gemini"
)

func main() {
	cli.RunMain("jeep-tts", run)
}

func run(args []string, stdout, stderr io.Writer) error {
	var model, voice, output string
	var listVoices, listModels bool
	var common cli.CommonFlags

	fs := cli.NewFlagSet("jeep-tts", stderr)
	common.Register(fs, 5*time.Minute)
	fs.StringVar(&model, "model", "", "Model name")
	fs.StringVarP(&voice, "voice", "v", "Kore", "Voice name")
	fs.StringVarP(&output, "output", "o", "output.wav", "Output WAV file")
	fs.BoolVar(&listModels, "list-models", false, "List available models and exit")
	fs.BoolVar(&listVoices, "list-voices", false, "List available voices and exit")
	fs.Usage = func() { printUsage(fs, stderr) }
	if done, err := cli.Parse(fs, args, "jeep-tts", stdout, &common); err != nil || done {
		return err
	}

	if listVoices {
		return printVoices(stdout, common.Format)
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
		model = config.ResolveModelWithConfig(cfg, "TTS")
	}

	if common.DryRun {
		info := &cli.DryRunInfo{
			Tool:      "jeep-tts",
			Model:     model,
			Project:   common.Project,
			Region:    common.Region,
			PromptLen: len(prompt),
			Extra:     map[string]any{"voice": voice},
		}
		return info.Print(stdout, common.Format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
	defer cancel()

	result, err := gemini.GenerateSpeech(ctx, common.Project, common.Region, model, voice, prompt, common.Retries)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "%w", err)
	}

	wav := pcmToWAV(result.PCMData, 24000, 1, 16)

	if err := os.WriteFile(output, wav, 0644); err != nil {
		return cli.Exitf(cli.ExitIO, "writing output: %w", err)
	}

	duration := float64(len(result.PCMData)) / 2 / 24000

	switch common.Format {
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
		if !common.Quiet {
			fmt.Fprintf(stderr, "Saved %s (%d bytes, %.1fs)\n", output, len(wav), duration)
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
