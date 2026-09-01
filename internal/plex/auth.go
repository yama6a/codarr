package plex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
)

// PlexTVBaseURL is where the PIN flow lives. It is not the media server.
const PlexTVBaseURL = "https://plex.tv"

// AuthAppURL is the page the operator opens to claim a PIN.
const AuthAppURL = "https://app.plex.tv/auth"

// Auth runs the plex.tv PIN flow of plan.md 16.1. It deliberately uses the
// legacy long-lived token rather than the newer JWT, which expires every seven
// days and would leave an unattended service signed out once a week.
type Auth struct {
	tr      *transport
	product string
}

// AuthConfig configures the PIN flow. BaseURL is only ever overridden by tests.
type AuthConfig struct {
	BaseURL    string
	Product    string
	HTTPClient *http.Client
	Clock      clock.Clock
	Retry      Retry
}

// NewAuth returns the plex.tv client.
func NewAuth(cfg AuthConfig) (*Auth, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = PlexTVBaseURL
	}

	if cfg.Product == "" {
		cfg.Product = Product
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}

	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}

	base, err := normaliseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	return &Auth{
		tr: &transport{
			base:    base,
			headers: http.Header{"Accept": {"application/json"}},
			client:  cfg.HTTPClient,
			clk:     cfg.Clock,
			retry:   cfg.Retry.normalise(),
		},
		product: cfg.Product,
	}, nil
}

// Pin is one plex.tv PIN. AuthToken is a secret: it is persisted and never
// logged, and the API layer never returns it (plan.md 18.4).
type Pin struct {
	ID               int64
	Code             string
	ClientIdentifier string
	AuthToken        string
	ExpiresAt        *time.Time
}

// Authorized reports whether the PIN has been claimed and carries a token.
func (p Pin) Authorized() bool { return p.AuthToken != "" }

//nolint:tagliatelle // plex.tv speaks camelCase; the repo's snake_case rule cannot apply to a foreign schema.
type pinResponse struct {
	ID               int64   `json:"id"`
	Code             string  `json:"code"`
	ClientIdentifier string  `json:"clientIdentifier"`
	AuthToken        *string `json:"authToken"`
	ExpiresAt        *string `json:"expiresAt"`
}

func (r pinResponse) pin() Pin {
	p := Pin{ID: r.ID, Code: r.Code, ClientIdentifier: r.ClientIdentifier}

	if r.AuthToken != nil {
		p.AuthToken = *r.AuthToken
	}

	if r.ExpiresAt != nil {
		if t, err := time.Parse(time.RFC3339, *r.ExpiresAt); err == nil {
			p.ExpiresAt = &t
		}
	}

	return p
}

// NewClientIdentifier generates the X-Plex-Client-Identifier. It is generated
// once and persisted: plex.tv ties the token to it, so a new one each start
// would register Codarr as a new device on every restart.
func NewClientIdentifier() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("plex: generating a client identifier failed: %w", err)
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	h := hex.EncodeToString(b[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// CreatePin starts the flow. strong=true asks plex.tv for a PIN that is not
// guessable from the four-character code alone.
func (a *Auth) CreatePin(ctx context.Context, clientIdentifier string) (Pin, error) {
	if clientIdentifier == "" {
		return Pin{}, fmt.Errorf("%w: no client identifier", ErrNotConfigured)
	}

	var resp pinResponse

	err := a.tr.do(ctx, request{
		method: http.MethodPost,
		path:   "/api/v2/pins",
		query:  url.Values{"strong": {"true"}},
		header: a.pinHeaders(clientIdentifier),
		out:    &resp,
	})
	if err != nil {
		return Pin{}, fmt.Errorf("plex: creating a plex.tv pin failed: %w", err)
	}

	return resp.pin(), nil
}

// CheckPin polls one PIN. An unclaimed PIN is a 200 with a null authToken, not
// an error, so the caller loops until Authorized or the PIN expires.
func (a *Auth) CheckPin(ctx context.Context, clientIdentifier string, id int64) (Pin, error) {
	var resp pinResponse

	err := a.tr.do(ctx, request{
		method: http.MethodGet,
		path:   fmt.Sprintf("/api/v2/pins/%d", id),
		header: a.pinHeaders(clientIdentifier),
		out:    &resp,
	})
	if err != nil {
		return Pin{}, fmt.Errorf("plex: polling plex.tv pin %d failed: %w", id, err)
	}

	return resp.pin(), nil
}

func (a *Auth) pinHeaders(clientIdentifier string) http.Header {
	return http.Header{
		"X-Plex-Client-Identifier": {clientIdentifier},
		"X-Plex-Product":           {a.product},
	}
}

// AuthURL is the page the UI opens for the operator to claim the PIN.
func (a *Auth) AuthURL(clientIdentifier, code string) string {
	q := url.Values{
		"clientID":                 {clientIdentifier},
		"code":                     {code},
		"context[device][product]": {a.product},
	}

	return AuthAppURL + "#?" + q.Encode()
}
