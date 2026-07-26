package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/version"
)

// RunMain executes run with the process args and standard streams, then
// translates its error into a process exit. It writes "<name>: <err>" to stderr
// and exits with the ExitError code (or 1 for a plain error). Every command's
// main() is a one-line call to this.
func RunMain(name string, run func(args []string, stdout, stderr io.Writer) error) {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		code := 1
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

// NewFlagSet returns a ContinueOnError flag set that reports parse errors and
// usage to stderr, matching the behavior every command relied on.
func NewFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// CommonFlags holds the flag values shared by every jeep command.
type CommonFlags struct {
	Project string
	Region  string
	Format  string
	Quiet   bool
	DryRun  bool
	Version bool
	Timeout time.Duration
	Retries int
}

// Register wires the shared flags into fs. defaultTimeout lets jeep-research use
// a longer default than the generation tools.
func (c *CommonFlags) Register(fs *flag.FlagSet, defaultTimeout time.Duration) {
	fs.StringVar(&c.Project, "project", "", "GCP project (default: auto-detect)")
	fs.StringVar(&c.Region, "region", "global", "Vertex AI region")
	fs.StringVar(&c.Format, "format", "text", "Output format: text or json")
	fs.BoolVarP(&c.Quiet, "quiet", "q", false, "Suppress stderr messages")
	fs.BoolVar(&c.DryRun, "dry-run", false, "Show what would be sent without making the API call")
	fs.BoolVar(&c.Version, "version", false, "Print version and exit")
	fs.DurationVar(&c.Timeout, "timeout", defaultTimeout, "Request timeout")
	fs.IntVar(&c.Retries, "retry", 0, "Retry transient errors with exponential backoff")
}

// Parse parses args into fs and handles the shared --version short-circuit. It
// returns done=true when the command should exit successfully without further
// work (i.e. --version was printed). A parse failure is returned as an
// ExitUsage error. Callers set fs.Usage before calling Parse so parse errors
// print the command's usage.
func Parse(fs *flag.FlagSet, args []string, name string, stdout io.Writer, c *CommonFlags) (done bool, err error) {
	if err := fs.Parse(args); err != nil {
		return false, &ExitError{Code: ExitUsage, Err: err}
	}
	if c.Version {
		fmt.Fprintf(stdout, "%s %s\n", name, version.String())
		return true, nil
	}
	return false, nil
}
