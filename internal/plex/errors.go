package plex

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrNotConfigured is returned when no base URL or token has been stored yet,
	// which is the state a fresh install starts in (plan.md 21).
	ErrNotConfigured = errors.New("plex: not configured")

	// ErrUnauthorized is what a rejected token looks like. Plex answers 401 with
	// an HTML body, so it is never distinguishable from the payload alone.
	ErrUnauthorized = errors.New("plex: unauthorized")

	// ErrRequestFailed is any other non-2xx answer.
	ErrRequestFailed = errors.New("plex: request failed")

	// ErrUnreadable is a 2xx answer that is not the JSON the endpoint promises.
	ErrUnreadable = errors.New("plex: unreadable response")

	// ErrNoSection is returned when no library section's Location contains the
	// path, so there is nothing to refresh.
	ErrNoSection = errors.New("plex: no library section contains the path")

	// ErrNoRatingKey is returned when no item in the owning section has the path
	// as one of its parts, so there is nothing to analyze.
	ErrNoRatingKey = errors.New("plex: no library item has that file")
)

// StatusError is a non-2xx answer, carrying enough of the body to act on
// without turning on request logging.
type StatusError struct {
	Method string
	Path   string
	Status int
	Body   string
}

var _ error = (*StatusError)(nil)

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
	}

	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.Path, e.Status, http.StatusText(e.Status), e.Body)
}

func (e *StatusError) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return ErrUnauthorized
	}

	return ErrRequestFailed
}

// bodyPreview keeps an error message useful without pasting a whole HTML page
// into the events table.
func bodyPreview(b []byte) string {
	const limit = 200

	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > limit {
		return s[:limit] + "..."
	}

	return s
}
