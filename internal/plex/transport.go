package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
)

// maxResponseBytes caps what a single answer may cost in memory. A whole
// section listing is the biggest thing Codarr ever asks Plex for.
const maxResponseBytes = 8 << 20

// Retry is the backoff policy. plan.md has no number for this; the rule is 5xx
// and connection errors are transient, 4xx never is.
type Retry struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultRetry is three tries over roughly a second.
func DefaultRetry() Retry {
	return Retry{Attempts: 3, Base: 250 * time.Millisecond, Max: 2 * time.Second}
}

func (r Retry) normalise() Retry {
	if r.Attempts < 1 {
		r.Attempts = 1
	}

	if r.Base <= 0 {
		r.Base = DefaultRetry().Base
	}

	if r.Max < r.Base {
		r.Max = r.Base
	}

	return r
}

func (r Retry) wait(attempt int) time.Duration {
	d := r.Base
	for range attempt {
		d *= 2
		if d >= r.Max {
			return r.Max
		}
	}

	return d
}

// transport injects X-Plex-Token and Accept: application/json on every call, and never
// puts the token anywhere it could be logged.
type transport struct {
	base    *url.URL
	headers http.Header
	client  *http.Client
	clk     clock.Clock
	retry   Retry
}

type request struct {
	method string
	path   string
	query  url.Values
	header http.Header
	out    any
}

// do runs req, retrying transient failures. An empty body with an out target is not an
// error, because refresh and analyze both answer with nothing.
func (t *transport) do(ctx context.Context, req request) error {
	var lastErr error

	attempts := t.retry.Attempts

	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s %s: %w", req.method, req.path, ctx.Err())
			case <-t.clk.After(t.retry.wait(attempt - 1)):
			}
		}

		body, err := t.attempt(ctx, req)
		if err == nil {
			return decode(req, body)
		}

		if !retryable(err) {
			return err
		}

		lastErr = err
	}

	return lastErr
}

func decode(req request, body []byte) error {
	if req.out == nil || len(body) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, req.out); err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnreadable, req.method, req.path, err)
	}

	return nil
}

func (t *transport) attempt(ctx context.Context, req request) ([]byte, error) {
	u := t.base.JoinPath(req.path)
	if len(req.query) > 0 {
		u.RawQuery = req.query.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.method, req.path, err)
	}

	for k, vs := range t.headers {
		httpReq.Header[k] = vs
	}

	for k, vs := range req.header {
		httpReq.Header[k] = vs
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, &transientError{err: fmt.Errorf("%s %s: %w", req.method, req.path, err)}
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, &transientError{err: fmt.Errorf("%s %s: reading the response failed: %w", req.method, req.path, err)}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(req, resp.StatusCode, body)
	}

	return body, nil
}

func statusError(req request, status int, body []byte) error {
	err := &StatusError{Method: req.method, Path: req.path, Status: status, Body: bodyPreview(body)}
	if status >= 500 || status == http.StatusTooManyRequests {
		return &transientError{err: err}
	}

	return err
}

// transientError marks the failures worth retrying: connection errors, a short
// read, 5xx and 429. Everything else, 4xx above all, is retried never.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

func retryable(err error) bool {
	var t *transientError

	return errors.As(err, &t)
}

func normaliseBaseURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ErrNotConfigured
	}

	u, err := url.Parse(strings.TrimRight(trimmed, "/"))
	if err != nil {
		return nil, fmt.Errorf("%w: base url %q is not a url: %w", ErrNotConfigured, trimmed, err)
	}

	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: base url %q has no scheme or host", ErrNotConfigured, trimmed)
	}

	return u, nil
}
