package job

import (
	"context"
	"errors"
	"fmt"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

// ErrCancelled is what a job that was cancelled from the UI returns. It is not
// a failure: the job ends in cancelled, with no failure code.
var ErrCancelled = errors.New("job: cancelled")

// ErrNotRunning is returned when a cancel names a job the worker is not
// running and the store will not move either.
var ErrNotRunning = errors.New("job: not the running job")

// ErrConfirmationRequired is returned when a bulk operation that destroys files
// is called without confirmation. plan.md 15.5 makes the confirmation mandatory.
var ErrConfirmationRequired = errors.New("job: the operation is irreversible and was not confirmed")

// Error is both halves of plan.md 19.1, a code and a message; a failed job
// without both is a bug, so every failing path goes through this type.
type Error struct {
	Code       domain.FailureCode
	Message    string
	StderrTail string
	Err        error
}

var _ error = (*Error)(nil)

func (f *Error) Error() string {
	if f.Err == nil {
		return f.Message
	}

	return f.Message + ": " + f.Err.Error()
}

func (f *Error) Unwrap() error { return f.Err }

func failf(code domain.FailureCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func wrapf(code domain.FailureCode, err error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Err: err}
}

// Everything the pipeline can produce lands on a code and a non-empty message;
// an unrecognised error becomes internal_error with the Go error text (19.1).
func classify(err error) *Error {
	var (
		own  *Error
		perr *promote.Error
	)

	switch {
	case err == nil:
		return nil
	case errors.As(err, &own):
		return own
	case errors.As(err, &perr):
		return &Error{Code: perr.Code, Message: perr.Message, Err: perr.Err}
	case errors.Is(err, ffmpeg.ErrFfmpegFailed), errors.Is(err, ffmpeg.ErrFfmpegStart):
		return wrapf(domain.FailFfmpeg, err, "ffmpeg did not complete")
	case errors.Is(err, ffprobe.ErrProbeFailed), errors.Is(err, ffprobe.ErrUnreadable):
		return wrapf(domain.FailProbe, err, "ffprobe did not return a usable result")
	default:
		return wrapf(domain.FailInternal, err, "the job stopped on an unexpected error")
	}
}

// cancelled reports an error that exists only because the job was cancelled or
// the process is shutting down, which must never be written as a failure.
func cancelled(ctx context.Context, err error) bool {
	return errors.Is(err, ErrCancelled) ||
		errors.Is(err, context.Canceled) ||
		ctx.Err() != nil
}
