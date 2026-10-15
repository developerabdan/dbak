package app

import (
	"errors"
	"fmt"
)

const (
	ExitOK            = 0
	ExitGeneral       = 1
	ExitConfigInvalid = 2
	ExitDatabase      = 3
	ExitDumpFailed    = 4
	ExitUploadFailed  = 5
	ExitPartial       = 6
	ExitLocked        = 7
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func Exit(code int, err error) error {
	return &ExitError{Code: code, Err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return ExitGeneral
}
