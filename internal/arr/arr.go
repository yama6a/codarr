// Package arr talks to Radarr and Sonarr. plan.md 16.2: there are several of
// each, so everything here is per instance from the start - base URL, API key,
// path mappings and root folders. There is no single-instance shortcut to
// generalise later.
//
// The one shape both flavours share is behind Client; the two places they
// differ, the rescan command and how an item is unmonitored, are switches
// inside the one implementation rather than two parallel clients.
package arr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/arr_mock.go -pkg mock . Client MediaServer OwnerResolver

// DefaultTimeout bounds one call. Every verb here queues work and returns.
const DefaultTimeout = 15 * time.Second

// Identity is who an instance is. It deliberately carries no API key, so it is
// safe to log and to hand to the API layer (plan.md 18.4).
type Identity struct {
	ID      int64
	Name    string
	Flavour domain.Flavour
}

// ItemRef is the item on the *arr side that a promoted file belongs to. Which
// field matters depends on the flavour: Radarr rescans a movie, Sonarr a
// series, and Sonarr unmonitors per episode.
type ItemRef struct {
	MovieID    int64
	SeriesID   int64
	EpisodeIDs []int64
}

// Client is one Radarr or Sonarr instance.
type Client interface {
	Identity() Identity
	Test(ctx context.Context) TestResult
	RootFolders(ctx context.Context) ([]RootFolder, error)
	Rescan(ctx context.Context, item ItemRef) error
	Unmonitor(ctx context.Context, item ItemRef) error
}

// Config is everything one instance's client needs.
type Config struct {
	Instance domain.ArrInstance

	// Mapper rewrites between this instance's view of the filesystem and
	// Codarr's. It is mandatory in practice: VERIFY.md records all four live
	// instances reporting the same literal root, "/media".
	Mapper *pathmap.Mapper

	HTTPClient *http.Client
	Clock      clock.Clock
	Logger     *slog.Logger
	Retry      Retry
}

// API is the HTTP implementation of Client.
type API struct {
	tr       *transport
	identity Identity
	mapper   *pathmap.Mapper
	log      *slog.Logger
}

var _ Client = (*API)(nil)

// New returns a client for one instance.
func New(cfg Config) (*API, error) {
	if cfg.Instance.Flavour != domain.FlavourRadarr && cfg.Instance.Flavour != domain.FlavourSonarr {
		return nil, fmt.Errorf("%w: %q", ErrUnknownFlavour, cfg.Instance.Flavour)
	}

	base, err := normaliseBaseURL(cfg.Instance.BaseURL)
	if err != nil {
		return nil, err
	}

	if cfg.Instance.APIKey == "" {
		return nil, fmt.Errorf("%w: %s has no api key", ErrNotConfigured, cfg.Instance.Name)
	}

	cfg = cfg.withDefaults()

	return &API{
		tr: &transport{
			instance: cfg.Instance.Name,
			base:     base,
			apiKey:   cfg.Instance.APIKey,
			client:   cfg.HTTPClient,
			clk:      cfg.Clock,
			retry:    cfg.Retry.normalise(),
		},
		identity: Identity{ID: cfg.Instance.ID, Name: cfg.Instance.Name, Flavour: cfg.Instance.Flavour},
		mapper:   cfg.Mapper,
		log:      cfg.Logger,
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

	return c
}

// Identity returns who this instance is.
func (a *API) Identity() Identity { return a.identity }

// Mapper is this instance's path mappings, for the webhook handler that has to
// rewrite an incoming path with the mappings of the instance that sent it
// (plan.md 13.1).
func (a *API) Mapper() *pathmap.Mapper { return a.mapper }

// TestResult is what the UI's Test button shows. A reachable instance that
// rejects the API key is a successful test with OK false, not an error.
type TestResult struct {
	OK      bool
	Message string
	AppName string
	Version string
}

//nolint:tagliatelle // Radarr and Sonarr speak camelCase; the repo's snake_case rule cannot apply to a foreign schema.
type systemStatus struct {
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	Version      string `json:"version"`
}

// Test reads the instance's system status, which proves both that it answers
// and that the API key is accepted.
func (a *API) Test(ctx context.Context) TestResult {
	var status systemStatus

	err := a.tr.do(ctx, call{method: http.MethodGet, path: "/api/v3/system/status", out: &status})
	if err != nil {
		return TestResult{OK: false, Message: testMessage(a.identity, err)}
	}

	if !flavourMatches(a.identity.Flavour, status.AppName) {
		return TestResult{
			OK:      false,
			AppName: status.AppName,
			Version: status.Version,
			Message: fmt.Sprintf("%s is configured as %s but the server at that address is %s.",
				a.identity.Name, a.identity.Flavour, status.AppName),
		}
	}

	return TestResult{
		OK:      true,
		AppName: status.AppName,
		Version: status.Version,
		Message: fmt.Sprintf("Connected to %s %s (%s).", status.AppName, status.Version, status.InstanceName),
	}
}

func testMessage(id Identity, err error) string {
	var status *StatusError
	if errors.As(err, &status) && status.Status == http.StatusUnauthorized {
		return id.Name + " answered but rejected the API key."
	}

	return id.Name + " could not be reached: " + err.Error()
}

func flavourMatches(flavour domain.Flavour, appName string) bool {
	switch flavour {
	case domain.FlavourRadarr:
		return appName == "" || appName == "Radarr"
	case domain.FlavourSonarr:
		return appName == "" || appName == "Sonarr"
	default:
		return false
	}
}
