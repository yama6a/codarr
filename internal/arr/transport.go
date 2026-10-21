package arr

import (
	"bytes"
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

// maxResponseBytes caps one answer. A whole movie resource is the largest thing
// Codarr reads from an instance.
const maxResponseBytes = 8 << 20

// Retry is the backoff policy: 5xx, 429 and connection errors are transient,
// 4xx never is.
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

// transport is one instance's request plumbing. The API key lives in the header
// map and is never written into a URL, an error or a log line.
type transport struct {
	instance string
	base     *url.URL
	apiKey   string
	client   *http.Client
	policy   retryhttp.Policy
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

type call struct {
	method string
	path   string
	body   any
	out    any
}

func (t *transport) do(ctx context.Context, c call) error {
	payload, err := encodeBody(c)
	if err != nil {
		return err
	}

	raw, err := t.send(retryhttp.WithPolicy(ctx, t.policy), c, payload)
	if err != nil {
		return err
	}

	return unmarshal(c, raw)
}

func encodeBody(c call) ([]byte, error) {
	if c.body == nil {
		return nil, nil
	}

	payload, err := json.Marshal(c.body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: encoding the request body failed: %w", c.method, c.path, err)
	}

	return payload, nil
}

func unmarshal(c call, raw []byte) error {
	if c.out == nil || len(raw) == 0 {
		return nil
	}

	if err := json.Unmarshal(raw, c.out); err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnreadable, c.method, c.path, err)
	}

	return nil
}

func (t *transport) send(ctx context.Context, c call, payload []byte) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, c.method, t.base.JoinPath(c.path).String(), body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", c.method, c.path, err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", t.apiKey)

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %s %s: %w", t.instance, c.method, c.path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: %s %s: reading the response failed: %w", t.instance, c.method, c.path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, t.statusError(c, resp.StatusCode, raw)
	}

	return raw, nil
}

func (t *transport) statusError(c call, status int, raw []byte) error {
	return &StatusError{
		Instance: t.instance,
		Method:   c.method,
		Path:     c.path,
		Status:   status,
		Body:     bodyPreview(raw),
	}
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
