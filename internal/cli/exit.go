package cli

import "fmt"

const (
	ExitUsage  = 1
	ExitConfig = 2
	ExitAPI    = 3
	ExitIO     = 4
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func Exitf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}
