package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/gcp"
	"github.com/tvaroska/jeep/internal/gemini"
)

// ResolveCommon applies the shared project/region/quiet resolution used by every
// command. It loads the config file, fills unset values from it (config < flag),
// falls back to auto-detecting the GCP project, and returns the loaded config so
// callers can resolve models against it. The project/region/quiet arguments are
// updated in place. It returns an ExitConfig error if no project can be found.
func ResolveCommon(fs *flag.FlagSet, project, region *string, quiet *bool) (*config.Config, error) {
	cfg := config.Load()
	if !*quiet && cfg.Quiet {
		*quiet = true
	}
	if *region == "global" && !fs.Changed("region") && cfg.Region != "" {
		*region = cfg.Region
	}
	if *project == "" && cfg.Project != "" {
		*project = cfg.Project
	}
	if *project == "" {
		*project = gcp.ResolveProject()
	}
	if *project == "" {
		return nil, Exitf(ExitConfig, "could not determine GCP project; set GOOGLE_CLOUD_PROJECT or pass --project")
	}
	return cfg, nil
}

// PrintModels lists available models to w in the given format ("text" or "json").
func PrintModels(ctx context.Context, w io.Writer, project, region, format string) error {
	models, err := gemini.ListModels(ctx, project, region)
	if err != nil {
		return Exitf(ExitAPI, "%w", err)
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
