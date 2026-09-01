package arr

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrNotConfigured is returned when an instance has no usable base URL or
	// API key yet.
	ErrNotConfigured = errors.New("arr: not configured")

	// ErrUnauthorized is a rejected API key. Radarr and Sonarr both answer 401
	// with an empty body, so the status is all there is to go on.
	ErrUnauthorized = errors.New("arr: unauthorized")

	// ErrRequestFailed is any other non-2xx answer.
	ErrRequestFailed = errors.New("arr: request failed")

	// ErrUnreadable is a 2xx answer that is not the JSON the endpoint promises.
	ErrUnreadable = errors.New("arr: unreadable response")

	// ErrUnknownFlavour is a flavour that is neither radarr nor sonarr.
	ErrUnknownFlavour = errors.New("arr: unknown flavour")

	// ErrBadPayload is a webhook body that is not JSON, or that carries none of
	// the fields the event type needs.
	ErrBadPayload = errors.New("arr: unreadable webhook payload")

	// ErrNoItem is a rescan or unmonitor with no item id to act on.
	ErrNoItem = errors.New("arr: no item id")

	// ErrNoPathMapping is a root folder unchanged by the instance's mappings, which on
	// this cluster means four identical roots and no attribution (VERIFY.md, 16.2).
	ErrNoPathMapping = errors.New("arr: instance has no path mapping for its root folder")
)

// StatusError is a non-2xx answer from an instance.
type StatusError struct {
	Instance string
	Method   string
	Path     string
	Status   int
	Body     string
}

var _ error = (*StatusError)(nil)

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s: %s %s: %d %s", e.Instance, e.Method, e.Path, e.Status, http.StatusText(e.Status))
	if e.Body == "" {
		return msg
	}

	return msg + ": " + e.Body
}

func (e *StatusError) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return ErrUnauthorized
	}

	return ErrRequestFailed
}

func bodyPreview(b []byte) string {
	const limit = 200

	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > limit {
		return s[:limit] + "..."
	}

	return s
}
