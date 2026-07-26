package version

import "runtime/debug"

// Version is injected at release build time via
// -ldflags "-X github.com/tvaroska/jeep/internal/version.Version=<tag>".
// When empty (e.g. `go install`), it falls back to module build info.
var Version string

func String() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "(devel)"
}
