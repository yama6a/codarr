package promote

import (
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Error carries both halves of plan.md 19.1: a machine-readable failure code
// for the jobs row and a message specific enough to act on without reading the
// logs. "verification failed" is not a message; naming the duration that
// differed is.
type Error struct {
	Code    domain.FailureCode
	Message string
	Err     error
}

var _ error = (*Error)(nil)

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Message
	}

	return e.Message + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code domain.FailureCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func wrap(code domain.FailureCode, err error, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Err: fmt.Errorf("%w", err)}
}

func fmtf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
