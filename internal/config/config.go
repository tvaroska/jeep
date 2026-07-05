package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Project string            `json:"project,omitempty"`
	Region  string            `json:"region,omitempty"`
	Models  map[string]string `json:"models,omitempty"`
	Quiet   bool              `json:"quiet,omitempty"`
}

func Load() *Config {
	path := configPath()
	if path == "" {
		return &Config{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &Config{}
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return &Config{}
	}
	return &cfg
}

func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "jeep", "config.json")
}
