package xerror

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

type xerror struct {
	err        error
	stacktrace []string
}

func (e xerror) Error() string {
	return fmt.Sprintf("%s\n%s", e.err, strings.Join(e.stacktrace, "\n"))
}

func New(message string) error {
	_, file, line, _ := runtime.Caller(1)
	return xerror{
		err:        errors.New(message),
		stacktrace: []string{fmt.Sprintf("%s %d", file, line)},
	}
}

func Wrap(err error) error {
	return wrapWithCaller(err, 2)
}

func WrapWithMessage(err error, message string) error {
	return wrapWithCallerAndMessage(err, 2, message)
}

func wrapWithCaller(err error, skip int) error {
	_, file, line, _ := runtime.Caller(skip)

	if xe, ok := err.(xerror); ok {
		xe.stacktrace = append([]string{fmt.Sprintf("%s %d", file, line)}, xe.stacktrace...)
		return xe
	}

	xe := xerror{
		err:        err,
		stacktrace: []string{fmt.Sprintf("%s %d", file, line)},
	}

	return xe
}

func wrapWithCallerAndMessage(err error, skip int, message string) error {
	_, file, line, _ := runtime.Caller(skip)

	if xe, ok := err.(xerror); ok {
		xe.stacktrace = append([]string{fmt.Sprintf("%s %d", file, line)}, xe.stacktrace...)
		xe.err = fmt.Errorf("%s: %w", message, xe.err)
		return xe
	}

	xe := xerror{
		err:        fmt.Errorf("%s: %w", message, err),
		stacktrace: []string{fmt.Sprintf("%s %d", file, line)},
	}

	return xe
}
