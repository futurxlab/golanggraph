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
	return WrapWithCaller(err, 2)
}

func WrapWithCaller(err error, skip int) error {
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
