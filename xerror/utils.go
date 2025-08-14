package xerror

import "errors"

func Is(err error, target error) bool {
	if xerr, ok := err.(xerror); ok {
		return errors.Is(xerr.err, target)
	}

	return errors.Is(err, target)
}
