// Package plex talks to the one Plex Media Server (plan.md 16.1): the post-promotion
// scan and analyze, the active-stream guard, and the plex.tv PIN flow.
//
// Codarr never deletes and rescans, which would destroy watch state, ratings and
// collections, and never replaces a streaming file, which gives the reader ESTALE (15.6).
package plex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/promote"
)

// DefaultTimeout bounds a single call to the server. The refresh and analyze
// verbs return as soon as the work is queued, so nothing here is long-running.
const DefaultTimeout = 15 * time.Second

// DefaultPartTTL is how long a rating key's file paths are reused (plan.md 16.1). The
// sessions list itself is never cached, or the guard's last check reopens its own race.
const DefaultPartTTL = 30 * time.Second

// DefaultSectionTTL is how long GET /library/sections is reused. Sections
// change when an operator edits the library, which is rare.
const DefaultSectionTTL = 5 * time.Minute

// Product is the X-Plex-Product Codarr identifies itself with, in the PIN flow
// and on every call to the server.
const Product = "Codarr"

// Config is everything the client needs. Nothing is instantiated internally.
type Config struct {
	BaseURL          string
	Token            string
	ClientIdentifier string

	// A no-op today, since Plex mounts the export whole on this cluster (VERIFY.md),
	// but one mount change away from mattering.
	Mapper *pathmap.Mapper

	// RefreshAfter and AnalyzeAfter mirror the stored settings and gate the two
	// halves of NotifyPromoted independently.
	RefreshAfter bool
	AnalyzeAfter bool

	HTTPClient *http.Client
	Clock      clock.Clock
	Logger     *slog.Logger
	Retry      Retry
	SectionTTL time.Duration
	PartTTL    time.Duration
}

// Client is one Plex server. There is only ever one (plan.md 16.1).
type Client struct {
	tr           *transport
	mapper       *pathmap.Mapper
	log          *slog.Logger
	clk          clock.Clock
	refreshAfter bool
	analyzeAfter bool
	sections     *sectionCache
	parts        *partCache
}

var (
	_ promote.StreamGuard = (*Client)(nil)
	_ promote.Notifier    = (*Client)(nil)
)

// New returns a client for the configured server. It fails only on a base URL
// that is not usable; a bad token is not visible until a call is made.
func New(cfg Config) (*Client, error) {
	base, err := normaliseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("%w: no token", ErrNotConfigured)
	}

	cfg = cfg.withDefaults()

	return &Client{
		tr: &transport{
			base:    base,
			headers: serverHeaders(cfg.Token, cfg.ClientIdentifier),
			client:  cfg.HTTPClient,
			clk:     cfg.Clock,
			retry:   cfg.Retry.normalise(),
		},
		mapper:       cfg.Mapper,
		log:          cfg.Logger,
		clk:          cfg.Clock,
		refreshAfter: cfg.RefreshAfter,
		analyzeAfter: cfg.AnalyzeAfter,
		sections:     &sectionCache{ttl: cfg.SectionTTL},
		parts:        &partCache{ttl: cfg.PartTTL, entries: map[string]partEntry{}},
	}, nil
}

func (c Config) withDefaults() Config {
	if c.Mapper == nil {
		c.Mapper = pathmap.New(nil)
	}

	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}

	if c.Clock == nil {
		c.Clock = clock.System()
	}

	if c.Logger == nil {
		c.Logger = slog.Default()
	}

	if c.SectionTTL <= 0 {
		c.SectionTTL = DefaultSectionTTL
	}

	if c.PartTTL <= 0 {
		c.PartTTL = DefaultPartTTL
	}

	return c
}

// serverHeaders is the only place the token is written. It goes in a header
// rather than the query string so it can never end up in a URL that is logged.
func serverHeaders(token, clientIdentifier string) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("X-Plex-Token", token)
	h.Set("X-Plex-Product", Product)

	if clientIdentifier != "" {
		h.Set("X-Plex-Client-Identifier", clientIdentifier)
	}

	return h
}

// TestResult is what the UI's Test button shows. A reachable server that
// rejects the token is a successful test with OK false, not an error.
type TestResult struct {
	OK            bool
	Message       string
	ServerName    string
	ServerVersion string
	Libraries     int
}

// Test asks the server who it is and lists its sections, so a green result
// proves both that the token works and that Codarr can see the libraries.
func (c *Client) Test(ctx context.Context) TestResult {
	var server serverResponse

	err := c.tr.do(ctx, request{method: http.MethodGet, path: "/", out: &server})
	if err != nil {
		return TestResult{OK: false, Message: testMessage(err)}
	}

	name := server.MediaContainer.FriendlyName
	version := server.MediaContainer.Version

	sections, err := c.refreshSections(ctx)
	if err != nil {
		return TestResult{
			OK:            false,
			ServerName:    name,
			ServerVersion: version,
			Message:       fmt.Sprintf("%s answered but its library sections could not be read: %v", nameOr(name), err),
		}
	}

	return TestResult{
		OK:            true,
		ServerName:    name,
		ServerVersion: version,
		Libraries:     len(sections),
		Message: fmt.Sprintf("Connected to %s (Plex Media Server %s), %d library sections.",
			nameOr(name), versionOr(version), len(sections)),
	}
}

func testMessage(err error) string {
	var status *StatusError
	if errors.As(err, &status) && status.Status == http.StatusUnauthorized {
		return "Plex answered but rejected the token. Re-enter it, or use the plex.tv sign-in."
	}

	return "Plex could not be reached: " + err.Error()
}

func nameOr(name string) string {
	if name == "" {
		return "the Plex server"
	}

	return name
}

func versionOr(version string) string {
	if version == "" {
		return "version unknown"
	}

	return version
}
