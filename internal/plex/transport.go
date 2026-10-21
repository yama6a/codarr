package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/gokit/retryhttp"
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

func (r Retry) policy() retryhttp.Policy {
	r = r.normalise()

	return retryhttp.Policy{Attempts: r.Attempts, Base: r.Base, Max: r.Max}
}

// transport injects X-Plex-Token and Accept: application/json on every call, and never
// puts the token anywhere it could be logged.
type transport struct {
	base    *url.URL
	headers http.Header
	client  *http.Client
	policy  retryhttp.Policy
}

// retryingClient copies c with a transport that retries per the request's policy and
// waits on clk, so tests never sleep.
func retryingClient(c *http.Client, clk clock.Clock) *http.Client {
	rc := *c
	rc.Transport = retryhttp.New(c.Transport,
		retryhttp.WithNow(clk.Now),
		retryhttp.WithSleep(func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-clk.After(d):
				return nil
			}
		}))

	return &rc
}

type request struct {
	method string
	path   string
	query  url.Values
	header http.Header
	out    any
}

// do runs req, retrying 5xx, 429 and connection errors. An empty body with an out
// target is not an error, because refresh and analyze both answer with nothing.
func (t *transport) do(ctx context.Context, req request) error {
	body, err := t.send(retryhttp.WithPolicy(ctx, t.policy), req)
	if err != nil {
		return err
	}

	return decode(req, body)
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

func (t *transport) send(ctx context.Context, req request) ([]byte, error) {
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
		return nil, fmt.Errorf("%s %s: %w", req.method, req.path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s %s: reading the response failed: %w", req.method, req.path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(req, resp.StatusCode, body)
	}

	return body, nil
}

func statusError(req request, status int, body []byte) error {
	return &StatusError{Method: req.method, Path: req.path, Status: status, Body: bodyPreview(body)}
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
