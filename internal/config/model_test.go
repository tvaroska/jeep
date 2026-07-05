package config

import "testing"

func TestResolveModel_EnvOverride(t *testing.T) {
	t.Setenv("JEEP_MODEL_TEXT", "gemini-custom")
	if got := ResolveModel("TEXT"); got != "gemini-custom" {
		t.Errorf("ResolveModel(TEXT) = %q, want %q", got, "gemini-custom")
	}
}

func TestResolveModel_Defaults(t *testing.T) {
	tests := []struct {
		use  string
		want string
	}{
		{"TEXT", "gemini-3.5-flash"},
		{"IMAGE", "gemini-3.1-flash-image"},
		{"TTS", "gemini-3.1-flash-tts-preview"},
	}
	for _, tt := range tests {
		t.Run(tt.use, func(t *testing.T) {
			t.Setenv("JEEP_MODEL_"+tt.use, "")
			if got := ResolveModel(tt.use); got != tt.want {
				t.Errorf("ResolveModel(%s) = %q, want %q", tt.use, got, tt.want)
			}
		})
	}
}

func TestResolveModel_UnknownUse(t *testing.T) {
	t.Setenv("JEEP_MODEL_UNKNOWN", "")
	if got := ResolveModel("UNKNOWN"); got != "gemini-3.5-flash" {
		t.Errorf("ResolveModel(UNKNOWN) = %q, want %q", got, "gemini-3.5-flash")
	}
}
