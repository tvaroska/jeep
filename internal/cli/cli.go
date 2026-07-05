package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func ResolvePrompt(positional []string) (string, error) {
	if len(positional) > 0 {
		return strings.Join(positional, " "), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("no prompt provided; pass as argument or pipe via stdin")
}
