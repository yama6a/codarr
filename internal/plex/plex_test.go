package plex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/plex"
)

func TestNew_RejectsAnUnusableConfiguration(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]plex.Config{
		"no base url": {Token: testToken},
		"no scheme":   {BaseURL: "192.168.100.21:32400", Token: testToken},
		"no token":    {BaseURL: "http://192.168.100.21:32400"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := plex.New(cfg)
			require.ErrorIs(t, err, plex.ErrNotConfigured)
		})
	}
}

func TestTest_ReportsTheServerNameAndSectionCount(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /": {fixture: "server.json"},
	}))
	c := newClient(t, s, nil)

	require.Equal(t, plex.TestResult{
		OK:            true,
		ServerName:    "Plex-Yama",
		ServerVersion: "1.43.3.10896-cb3ebc72d",
		Libraries:     4,
		Message:       "Connected to Plex-Yama (Plex Media Server 1.43.3.10896-cb3ebc72d), 4 library sections.",
	}, c.Test(context.Background()))
}

// The live server answers a rejected token with an HTML page, not JSON, so the
// status is the only thing that identifies it.
func TestTest_ExplainsARejectedToken(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /": {status: http.StatusUnauthorized, fixture: "unauthorized.html"},
	})
	c := newClient(t, s, nil)

	got := c.Test(context.Background())
	require.False(t, got.OK)
	require.Equal(t, "Plex answered but rejected the token. Re-enter it, or use the plex.tv sign-in.", got.Message)
}

func TestTest_ExplainsAnUnreachableServer(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	c, err := plex.New(plex.Config{
		BaseURL: url,
		Token:   testToken,
		Clock:   clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		Retry:   plex.Retry{Attempts: 1, Base: time.Millisecond, Max: time.Millisecond},
	})
	require.NoError(t, err)

	got := c.Test(context.Background())
	require.False(t, got.OK)
	require.True(t, strings.HasPrefix(got.Message, "Plex could not be reached: "), got.Message)
}

func TestTransport_RetriesA5xxAndSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	s := newServer(t, nil)
	s.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/library/sections" {
			return false
		}

		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)

			return true
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(readFixture("sections.json"))

		return true
	}

	c := newClient(t, s, nil)

	got, err := c.Sections(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Equal(t, int32(3), calls.Load())
}

// A 4xx is the server's final answer. Retrying it wastes time and, on 401,
// looks like a brute-force attempt.
func TestTransport_NeverRetriesA4xx(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /library/sections": {status: http.StatusUnauthorized, fixture: "unauthorized.html"},
	})
	c := newClient(t, s, nil)

	_, err := c.Sections(context.Background())
	require.ErrorIs(t, err, plex.ErrUnauthorized)
	require.Equal(t, 1, s.count())
}

func TestTransport_GivesUpAfterTheLastAttempt(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /library/sections": {status: http.StatusInternalServerError, body: "boom"},
	})
	c := newClient(t, s, nil)

	_, err := c.Sections(context.Background())
	require.ErrorIs(t, err, plex.ErrRequestFailed)
	require.Equal(t, 3, s.count())
}

func TestTransport_ReportsUnreadableJSON(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /library/sections": {body: "<html>not json</html>"},
	})
	c := newClient(t, s, nil)

	_, err := c.Sections(context.Background())
	require.ErrorIs(t, err, plex.ErrUnreadable)
}

func TestTransport_StopsOnACancelledContext(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /library/sections": {status: http.StatusServiceUnavailable, body: "{}"},
	})
	c := newClient(t, s, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Sections(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

// The token goes in a header and nowhere else, so it cannot leak through a
// logged URL or an error message.
func TestTransport_KeepsTheTokenOutOfTheURLAndOutOfErrors(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /library/sections": {status: http.StatusBadRequest, body: "bad request"},
	})
	c := newClient(t, s, nil)

	_, err := c.Sections(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), testToken)

	for _, r := range s.seen() {
		require.NotContains(t, r.Query, testToken)
		require.Equal(t, testToken, r.Token)
	}
}
