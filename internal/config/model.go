package config

import (
	"os"
	"strings"
)

var defaults = map[string]string{
	"TEXT":     "gemini-3.5-flash",
	"IMAGE":    "gemini-3.1-flash-image",
	"TTS":      "gemini-3.1-flash-tts-preview",
	"RESEARCH": "deep-research-preview-04-2026",
}

func ResolveModel(use string) string {
	return ResolveModelWithConfig(Load(), use)
}

func ResolveModelWithConfig(cfg *Config, use string) string {
	if m := os.Getenv("JEEP_MODEL_" + use); m != "" {
		return m
	}
	if cfg != nil {
		if m, ok := cfg.Models[strings.ToLower(use)]; ok && m != "" {
			return m
		}
	}
	if d, ok := defaults[use]; ok {
		return d
	}
	return "gemini-3.5-flash"
}
