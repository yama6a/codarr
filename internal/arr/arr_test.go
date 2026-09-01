package arr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestNew_RejectsAnUnusableInstance(t *testing.T) {
	t.Parallel()

	for name, instance := range map[string]domain.ArrInstance{
		"unknown flavour": {Name: "x", Flavour: "lidarr", BaseURL: "http://host:7878", APIKey: testAPIKey},
		"no base url":     {Name: "x", Flavour: domain.FlavourRadarr, APIKey: testAPIKey},
		"no api key":      {Name: "x", Flavour: domain.FlavourRadarr, BaseURL: "http://host:7878"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := arr.New(arr.Config{Instance: instance})
			require.Error(t, err)
		})
	}
}

func TestIdentity_CarriesNoSecret(t *testing.T) {
	t.Parallel()

	s := newServer(t, nil)
	c := newClient(t, s, radarrYama(), radarrMapper())

	require.Equal(t, arr.Identity{ID: 1, Name: "radarr-yama", Flavour: domain.FlavourRadarr}, c.Identity())
}

func TestTest_ReportsTheAppAndVersionRadarrRuns(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/system/status": {fixture: "radarr_system_status.json"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	require.Equal(t, arr.TestResult{
		OK:      true,
		AppName: "Radarr",
		Version: "6.4.2.10590",
		Message: "Connected to Radarr 6.4.2.10590 (Radarr-Yama).",
	}, c.Test(context.Background()))
}

func TestTest_ReportsTheAppAndVersionSonarrRuns(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/system/status": {fixture: "sonarr_system_status.json"},
	})
	c := newClient(t, s, sonarrYama(), sonarrMapper())

	got := c.Test(context.Background())
	require.True(t, got.OK)
	require.Equal(t, "Sonarr", got.AppName)
	require.Equal(t, "4.0.19.3008", got.Version)
}

// Two Radarr and two Sonarr behind similar URLs make a paste error easy, and a
// Radarr configured as a Sonarr fails much later and much less clearly.
func TestTest_CatchesAnInstanceConfiguredAsTheWrongFlavour(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/system/status": {fixture: "radarr_system_status.json"},
	})
	c := newClient(t, s, sonarrYama(), sonarrMapper())

	got := c.Test(context.Background())
	require.False(t, got.OK)
	require.Equal(t, "sonarr-yama is configured as sonarr but the server at that address is Radarr.", got.Message)
}

// The live instances answer a bad key with a bare 401 and no body at all.
func TestTest_ExplainsARejectedAPIKey(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/system/status": {status: http.StatusUnauthorized},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	got := c.Test(context.Background())
	require.False(t, got.OK)
	require.Equal(t, "radarr-yama answered but rejected the API key.", got.Message)
}

func TestTest_ExplainsAnUnreachableInstance(t *testing.T) {
	t.Parallel()

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	instance := radarrYama()
	instance.BaseURL = url

	c, err := arr.New(arr.Config{
		Instance: instance,
		Clock:    clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		Retry:    arr.Retry{Attempts: 1, Base: time.Millisecond, Max: time.Millisecond},
	})
	require.NoError(t, err)

	got := c.Test(context.Background())
	require.False(t, got.OK)
	require.True(t, strings.HasPrefix(got.Message, "radarr-yama could not be reached: "), got.Message)
}

func TestTransport_RetriesA5xxAndSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	s := newServer(t, nil)
	s.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v3/system/status" {
			return false
		}

		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)

			return true
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture("radarr_system_status.json"))

		return true
	}

	c := newClient(t, s, radarrYama(), radarrMapper())

	require.True(t, c.Test(context.Background()).OK)
	require.Equal(t, int32(3), calls.Load())
}

func TestTransport_NeverRetriesA4xx(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {status: http.StatusUnauthorized},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	_, err := c.RootFolders(context.Background())
	require.ErrorIs(t, err, arr.ErrUnauthorized)
	require.Equal(t, 1, s.count())
}

func TestTransport_ReportsUnreadableJSON(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {body: "<html>not json</html>"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	_, err := c.RootFolders(context.Background())
	require.ErrorIs(t, err, arr.ErrUnreadable)
}

func TestTransport_StopsOnACancelledContext(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {status: http.StatusServiceUnavailable, body: "{}"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.RootFolders(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTransport_KeepsTheAPIKeyOutOfErrors(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/rootfolder": {status: http.StatusBadRequest, body: "bad request"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	_, err := c.RootFolders(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), testAPIKey)
	require.Equal(t, testAPIKey, s.seen()[0].APIKey)
}
