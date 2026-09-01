package arr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
)

func TestRescan_SendsRescanMovieToRadarr(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"POST /api/v3/command": {status: http.StatusCreated, fixture: "radarr_command_rescan.json"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	require.NoError(t, c.Rescan(context.Background(), arr.ItemRef{MovieID: 412}))

	require.Equal(t, []recorded{{
		Method: http.MethodPost,
		Path:   "/api/v3/command",
		APIKey: testAPIKey,
		Accept: "application/json",
		Body:   `{"movieId":412,"name":"RescanMovie"}`,
	}}, s.seen())
}

func TestRescan_SendsRescanSeriesToSonarr(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"POST /api/v3/command": {status: http.StatusCreated, fixture: "sonarr_command_rescan.json"},
	})
	c := newClient(t, s, sonarrYama(), sonarrMapper())

	require.NoError(t, c.Rescan(context.Background(), arr.ItemRef{SeriesID: 77}))
	require.JSONEq(t, `{"name":"RescanSeries","seriesId":77}`, s.seen()[0].Body)
}

func TestRescan_RefusesWithoutAnItemID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		sonarr bool
		item   arr.ItemRef
	}{
		"radarr without a movie id":  {sonarr: false, item: arr.ItemRef{SeriesID: 77}},
		"sonarr without a series id": {sonarr: true, item: arr.ItemRef{MovieID: 412}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newServer(t, nil)

			instance, mapper := radarrYama(), radarrMapper()
			if tc.sonarr {
				instance, mapper = sonarrYama(), sonarrMapper()
			}

			err := newClient(t, s, instance, mapper).Rescan(context.Background(), tc.item)
			require.ErrorIs(t, err, arr.ErrNoItem)
			require.Equal(t, 0, s.count())
		})
	}
}

// The PUT replaces the whole resource, so the movie is round-tripped as a raw
// object. A typed model would blank every field Radarr has added since.
func TestUnmonitor_RoundTripsTheWholeMovieWithMonitoredFalse(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/movie/412": {fixture: "radarr_movie.json"},
		"PUT /api/v3/movie":     {fixture: "radarr_movie.json"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	require.NoError(t, c.Unmonitor(context.Background(), arr.ItemRef{MovieID: 412}))

	seen := s.seen()
	require.Len(t, seen, 2)
	require.Equal(t, http.MethodGet, seen[0].Method)
	require.Equal(t, http.MethodPut, seen[1].Method)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(seen[1].Body), &sent))

	var original map[string]any
	require.NoError(t, json.Unmarshal(fixture("radarr_movie.json"), &original))

	require.Equal(t, false, sent["monitored"])
	require.Len(t, sent, len(original))

	for key, value := range original {
		if key == "monitored" {
			continue
		}

		require.Equal(t, value, sent[key], key)
	}
}

func TestUnmonitor_SendsTheEpisodeIDsToSonarr(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"PUT /api/v3/episode/monitor": {fixture: "sonarr_episode_monitor.json"},
	})
	c := newClient(t, s, sonarrYama(), sonarrMapper())

	require.NoError(t, c.Unmonitor(context.Background(), arr.ItemRef{SeriesID: 77, EpisodeIDs: []int64{5501, 5502}}))
	require.JSONEq(t, `{"episodeIds":[5501,5502],"monitored":false}`, s.seen()[0].Body)
}

func TestUnmonitor_RefusesWithoutAnItemID(t *testing.T) {
	t.Parallel()

	s := newServer(t, nil)

	require.ErrorIs(t,
		newClient(t, s, radarrYama(), radarrMapper()).Unmonitor(context.Background(), arr.ItemRef{}),
		arr.ErrNoItem)
	require.ErrorIs(t,
		newClient(t, s, sonarrYama(), sonarrMapper()).Unmonitor(context.Background(), arr.ItemRef{SeriesID: 77}),
		arr.ErrNoItem)
	require.Equal(t, 0, s.count())
}

func TestUnmonitor_ReportsAMovieTheInstanceDoesNotHave(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /api/v3/movie/412": {body: "{}"},
	})
	c := newClient(t, s, radarrYama(), radarrMapper())

	require.ErrorIs(t, c.Unmonitor(context.Background(), arr.ItemRef{MovieID: 412}), arr.ErrNoItem)
	require.Equal(t, 1, s.count())
}
