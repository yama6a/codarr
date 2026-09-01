package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
)

// Error is the failure the API renders. plan.md's spec has one default response
// per operation carrying {error, message, details}, so the status code travels
// on the error rather than in the response type.
type Error struct {
	Status  int
	Code    string
	Message string
	Err     error
}

var _ error = (*Error)(nil)

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Code + ": " + e.Message
	}

	return e.Code + ": " + e.Message + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func badRequest(format string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: fmt.Sprintf(format, args...)}
}

func conflict(code, format string, args ...any) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: fmt.Sprintf(format, args...)}
}

// errBody has the exact shape every generated `<Operation>defaultJSONResponse`
// has, so one helper can build the body for all fifty of them.
type errBody struct {
	Body       gen.Error
	StatusCode int
}

// fail turns any error into the response body, logging server-side faults.
// Client mistakes are not logged: a 404 from a stale UI tab is not an incident.
func (s *Server) fail(ctx context.Context, err error) errBody {
	status, code, message := classify(err)

	if status >= http.StatusInternalServerError {
		s.log.ErrorContext(ctx, "request failed",
			slog.String("code", code), slog.String("error", err.Error()))

		if s.metrics != nil {
			s.metrics.Error(code)
		}
	}

	return errBody{
		Body:       gen.Error{Error: code, Message: message},
		StatusCode: status,
	}
}

//nolint:cyclop // one flat mapping table; splitting it would only hide the mapping
func classify(err error) (status int, code, message string) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Status, apiErr.Code, apiErr.Message
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not_found", err.Error()
	case errors.Is(err, ingest.ErrUnknownWebhook):
		return http.StatusNotFound, "unknown_webhook", err.Error()
	case errors.Is(err, ingest.ErrOutsideRoots):
		return http.StatusBadRequest, "path_outside_roots", err.Error()
	case errors.Is(err, ingest.ErrNotAFile):
		return http.StatusBadRequest, "not_a_file", err.Error()
	case errors.Is(err, job.ErrConfirmationRequired):
		return http.StatusBadRequest, "confirmation_required", err.Error()
	case errors.Is(err, job.ErrNotRunning):
		return http.StatusConflict, "not_running", err.Error()
	case errors.Is(err, arr.ErrUnknownFlavour), errors.Is(err, arr.ErrBadPayload):
		return http.StatusBadRequest, "bad_request", err.Error()
	case errors.Is(err, arr.ErrNoPathMapping):
		return http.StatusConflict, "missing_path_mapping", err.Error()
	case errors.Is(err, plex.ErrNotConfigured), errors.Is(err, arr.ErrNotConfigured):
		return http.StatusConflict, "not_configured", err.Error()
	case errors.Is(err, plex.ErrUnauthorized), errors.Is(err, arr.ErrUnauthorized):
		return http.StatusBadGateway, "upstream_unauthorized", err.Error()
	case errors.Is(err, plex.ErrRequestFailed), errors.Is(err, plex.ErrUnreadable),
		errors.Is(err, arr.ErrRequestFailed), errors.Is(err, arr.ErrUnreadable):
		return http.StatusBadGateway, "upstream_error", err.Error()
	case errors.Is(err, plex.ErrNoSection), errors.Is(err, plex.ErrNoRatingKey):
		return http.StatusNotFound, "not_found", err.Error()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, "cancelled", err.Error()
	default:
		return http.StatusInternalServerError, "internal_error", err.Error()
	}
}

// writeError is the escape hatch for the two handlers that write the response
// themselves rather than returning a generated response object.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	s.log.ErrorContext(r.Context(), "request failed",
		slog.String("code", code), slog.String("error", message))

	body, err := json.Marshal(gen.Error{Error: code, Message: message})
	if err != nil {
		body = []byte(`{"error":"internal_error","message":"the error could not be encoded"}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
