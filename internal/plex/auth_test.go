package plex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/plex"
)

type authRecorded struct {
	Method     string
	Path       string
	Query      string
	Product    string
	Identifier string
	Accept     string
}

type authServer struct {
	*httptest.Server

	mu       sync.Mutex
	handler  http.HandlerFunc
	requests []authRecorded
}

func newAuthServer(t *testing.T, handler http.HandlerFunc) *authServer {
	t.Helper()

	s := &authServer{handler: handler}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, authRecorded{
			Method:     r.Method,
			Path:       r.URL.Path,
			Query:      r.URL.RawQuery,
			Product:    r.Header.Get("X-Plex-Product"),
			Identifier: r.Header.Get("X-Plex-Client-Identifier"),
			Accept:     r.Header.Get("Accept"),
		})
		s.mu.Unlock()

		s.handler(w, r)
	}))
	t.Cleanup(s.Close)

	return s
}

func (s *authServer) seen() []authRecorded {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]authRecorded(nil), s.requests...)
}

func newAuth(t *testing.T, s *authServer) *plex.Auth {
	t.Helper()

	a, err := plex.NewAuth(plex.AuthConfig{
		BaseURL: s.URL,
		Clock:   clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		Retry:   plex.Retry{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond},
	})
	require.NoError(t, err)

	return a
}

const testIdentifier = "00000000-0000-4000-8000-000000000000"

func TestCreatePin_AsksForAStrongPinAsCodarr(t *testing.T) {
	t.Parallel()

	s := newAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readFixture("pin_created.json"))
	})

	got, err := newAuth(t, s).CreatePin(context.Background(), testIdentifier)
	require.NoError(t, err)

	require.Equal(t, int64(987654321), got.ID)
	require.Equal(t, "ABCD", got.Code)
	require.Equal(t, testIdentifier, got.ClientIdentifier)
	require.False(t, got.Authorized())
	require.NotNil(t, got.ExpiresAt)
	require.Equal(t, time.Date(2026, 9, 1, 10, 15, 0, 0, time.UTC), got.ExpiresAt.UTC())

	require.Equal(t, []authRecorded{{
		Method:     http.MethodPost,
		Path:       "/api/v2/pins",
		Query:      "strong=true",
		Product:    plex.Product,
		Identifier: testIdentifier,
		Accept:     "application/json",
	}}, s.seen())
}

func TestCreatePin_RefusesWithoutAClientIdentifier(t *testing.T) {
	t.Parallel()

	s := newAuthServer(t, func(http.ResponseWriter, *http.Request) {})

	_, err := newAuth(t, s).CreatePin(context.Background(), "")
	require.ErrorIs(t, err, plex.ErrNotConfigured)
	require.Empty(t, s.seen())
}

// An unclaimed PIN is a 200 with a null authToken, so the poll loop keeps going
// rather than treating it as a failure.
func TestCheckPin_ReportsAnUnclaimedPinAsNotAuthorized(t *testing.T) {
	t.Parallel()

	s := newAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readFixture("pin_pending.json"))
	})

	got, err := newAuth(t, s).CheckPin(context.Background(), testIdentifier, 987654321)
	require.NoError(t, err)
	require.False(t, got.Authorized())
	require.Empty(t, got.AuthToken)
	require.Equal(t, "/api/v2/pins/987654321", s.seen()[0].Path)
}

func TestCheckPin_ReturnsTheTokenOnceThePinIsClaimed(t *testing.T) {
	t.Parallel()

	s := newAuthServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readFixture("pin_claimed.json"))
	})

	got, err := newAuth(t, s).CheckPin(context.Background(), testIdentifier, 987654321)
	require.NoError(t, err)
	require.True(t, got.Authorized())
	require.Equal(t, "PLACEHOLDER-PLEX-TOKEN", got.AuthToken)
}

func TestAuthURL_BuildsThePageTheOperatorOpens(t *testing.T) {
	t.Parallel()

	s := newAuthServer(t, func(http.ResponseWriter, *http.Request) {})

	require.Equal(t,
		"https://app.plex.tv/auth#?clientID="+testIdentifier+"&code=ABCD&context%5Bdevice%5D%5Bproduct%5D=Codarr",
		newAuth(t, s).AuthURL(testIdentifier, "ABCD"))
}

// The identifier is generated once and persisted: plex.tv ties the token to it,
// so a fresh one per start would register a new device every restart.
func TestNewClientIdentifier_IsAStableLookingUniqueUUID(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	first, err := plex.NewClientIdentifier()
	require.NoError(t, err)
	require.Regexp(t, pattern, first)

	second, err := plex.NewClientIdentifier()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}
