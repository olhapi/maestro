package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/olhapi/maestro/internal/kanban"
)

const (
	exitCodeSuccess  = 0
	exitCodeUsage    = 2
	exitCodeNotFound = 3
	exitCodeRuntime  = 4
)

type cliError struct {
	code int
	msg  string
	err  error
}

func (e *cliError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.msg != "" && e.err != nil:
		return fmt.Sprintf("%s: %v", e.msg, e.err)
	case e.msg != "":
		return e.msg
	case e.err != nil:
		return e.err.Error()
	default:
		return ""
	}
}

func (e *cliError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func usageErrorf(format string, args ...interface{}) error {
	return &cliError{code: exitCodeUsage, msg: fmt.Sprintf(format, args...)}
}

func notFoundErrorf(format string, args ...interface{}) error {
	return &cliError{code: exitCodeNotFound, msg: fmt.Sprintf(format, args...)}
}

func runtimeErrorf(format string, args ...interface{}) error {
	return &cliError{code: exitCodeRuntime, msg: fmt.Sprintf(format, args...)}
}

func wrapRuntime(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return &cliError{code: exitCodeRuntime, msg: fmt.Sprintf(format, args...), err: err}
}

func exitCode(err error) int {
	if err == nil {
		return exitCodeSuccess
	}
	var cliErr *cliError
	if errors.As(err, &cliErr) {
		return cliErr.code
	}
	if kanban.IsNotFound(err) {
		return exitCodeNotFound
	}
	if kanban.IsValidation(err) {
		return exitCodeUsage
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unknown flag") || strings.Contains(msg, "flag needs an argument") || strings.Contains(msg, "unknown command") || strings.Contains(msg, "accepts") {
		return exitCodeUsage
	}
	return exitCodeRuntime
}
