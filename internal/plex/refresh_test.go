package plex_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/plex"
)

const moviePath = "/media/yama/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv"

func TestRefreshPath_ScansTheContainingDirectoryOfTheOwningSection(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/refresh": {status: http.StatusOK},
	}))
	c := newClient(t, s, nil)

	require.NoError(t, c.RefreshPath(context.Background(), moviePath))

	require.Equal(t, recorded{
		Method: http.MethodGet,
		Path:   "/library/sections/1/refresh",
		Query:  "path=%2Fmedia%2Fyama%2Fmovies%2FArrival+%282016%29",
		Token:  testToken,
		Accept: "application/json",
	}, s.seen()[1])
}

func TestAnalyze_PutsTheAnalyzeVerbOnTheItem(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"PUT /library/metadata/1201/analyze": {status: http.StatusOK},
	})
	c := newClient(t, s, nil)

	require.NoError(t, c.Analyze(context.Background(), "1201"))

	require.Equal(t, []recorded{
		{Method: http.MethodPut, Path: "/library/metadata/1201/analyze", Token: testToken, Accept: "application/json"},
	}, s.seen())
}

func TestAnalyze_RefusesAnEmptyRatingKey(t *testing.T) {
	t.Parallel()

	s := newServer(t, nil)
	c := newClient(t, s, nil)

	require.ErrorIs(t, c.Analyze(context.Background(), ""), plex.ErrNoRatingKey)
	require.Equal(t, 0, s.count())
}

func TestRatingKeyFor_FindsTheItemWhosePartIsTheFile(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/all": {fixture: "section_all_match.json"},
	}))
	c := newClient(t, s, nil)

	got, err := c.RatingKeyFor(context.Background(), moviePath)
	require.NoError(t, err)
	require.Equal(t, "1201", got)

	require.Equal(t,
		"file=%2Fmedia%2Fyama%2Fmovies%2FArrival+%282016%29%2FArrival+%282016%29+Bluray-1080p.mkv&type=1",
		s.seen()[1].Query)
}

// A show section's /all lists series, so the episode type has to be asked for
// explicitly or the answer carries no parts at all.
func TestRatingKeyFor_AsksForEpisodesInAShowSection(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/2/all": {fixture: "section_all_empty.json"},
	}))
	c := newClient(t, s, nil)

	_, err := c.RatingKeyFor(context.Background(), "/media/yama/tv/Severance/Season 01/ep.mkv")
	require.ErrorIs(t, err, plex.ErrNoRatingKey)
	require.Contains(t, s.seen()[1].Query, "type=4")
}

// Plex accepts and silently ignores unknown query filters, so a returned item
// only counts when one of its parts really is the file.
func TestRatingKeyFor_IgnoresAnItemWhosePartsAreADifferentFile(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/all": {fixture: "section_all_match.json"},
	}))
	c := newClient(t, s, nil)

	_, err := c.RatingKeyFor(context.Background(), "/media/yama/movies/Other (2020)/Other.mkv")
	require.ErrorIs(t, err, plex.ErrNoRatingKey)
}

func TestNotifyPromoted_RefreshesThenAnalyzesTheItemItFoundFirst(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/all":        {fixture: "section_all_match.json"},
		"GET /library/sections/1/refresh":    {status: http.StatusOK},
		"PUT /library/metadata/1201/analyze": {status: http.StatusOK},
	}))
	c := newClient(t, s, nil)

	require.NoError(t, c.NotifyPromoted(context.Background(), moviePath))

	paths := make([]string, 0, s.count())
	for _, r := range s.seen() {
		paths = append(paths, r.Method+" "+r.Path)
	}

	require.Equal(t, []string{
		"GET /library/sections",
		"GET /library/sections/1/all",
		"GET /library/sections/1/refresh",
		"PUT /library/metadata/1201/analyze",
	}, paths)
}

// A full job can turn a legacy container into MKV, which changes the path, so
// the item is not there before the scan. One retry after it is all that is
// worth spending against an asynchronous scan.
func TestNotifyPromoted_LooksAgainAfterTheRefreshWhenTheItemWasNotThere(t *testing.T) {
	t.Parallel()

	var calls int

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/refresh":    {status: http.StatusOK},
		"PUT /library/metadata/1201/analyze": {status: http.StatusOK},
	}))
	s.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/library/sections/1/all" {
			return false
		}

		calls++
		w.Header().Set("Content-Type", "application/json")

		if calls == 1 {
			_, _ = w.Write(readFixture("section_all_empty.json"))
		} else {
			_, _ = w.Write(readFixture("section_all_match.json"))
		}

		return true
	}

	c := newClient(t, s, nil)

	require.NoError(t, c.NotifyPromoted(context.Background(), moviePath))
	require.Equal(t, 2, calls)
}

func TestNotifyPromoted_SkipsAnalyzeWhenItIsTurnedOff(t *testing.T) {
	t.Parallel()

	s := newServer(t, merge(sectionRoutes(), map[string]route{
		"GET /library/sections/1/refresh": {status: http.StatusOK},
	}))
	c := newClient(t, s, func(cfg *plex.Config) { cfg.AnalyzeAfter = false })

	require.NoError(t, c.NotifyPromoted(context.Background(), moviePath))
	require.Equal(t, 2, s.count())
}

func TestNotifyPromoted_DoesNothingWhenBothHalvesAreOff(t *testing.T) {
	t.Parallel()

	s := newServer(t, nil)
	c := newClient(t, s, func(cfg *plex.Config) {
		cfg.RefreshAfter = false
		cfg.AnalyzeAfter = false
	})

	require.NoError(t, c.NotifyPromoted(context.Background(), moviePath))
	require.Equal(t, 0, s.count())
}

func TestNotifyPromoted_ReportsAPathNoSectionCovers(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	err := c.NotifyPromoted(context.Background(), "/media/elsewhere/x.mkv")
	require.ErrorIs(t, err, plex.ErrNoSection)
}
