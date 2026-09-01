package plex_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/plex"
)

func TestSections_ReadsTheLiveSectionList(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	got, err := c.Sections(context.Background())
	require.NoError(t, err)

	require.Equal(t, []plex.Section{
		{Key: "3", Title: "Movies-Kostas", Type: "movie", Locations: []string{"/media/kostas/movies"}},
		{Key: "1", Title: "Movies-Yama", Type: "movie", Locations: []string{"/media/yama/movies"}},
		{Key: "4", Title: "TV-Kostas", Type: "show", Locations: []string{"/media/kostas/tv"}},
		{Key: "2", Title: "TV-Yama", Type: "show", Locations: []string{"/media/yama/tv"}},
	}, got)
}

func TestSections_SendsTheTokenAsAHeader(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	_, err := c.Sections(context.Background())
	require.NoError(t, err)

	require.Equal(t, []recorded{
		{Method: http.MethodGet, Path: "/library/sections", Token: testToken, Accept: "application/json"},
	}, s.seen())
}

func TestSections_IsCachedUntilTheTTLExpires(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	s := newServer(t, sectionRoutes())
	c := newClient(t, s, func(cfg *plex.Config) {
		cfg.Clock = fake
		cfg.SectionTTL = time.Minute
	})

	_, err := c.Sections(context.Background())
	require.NoError(t, err)

	_, err = c.Sections(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, s.count())

	fake.Advance(time.Minute)

	_, err = c.Sections(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, s.count())
}

func TestSections_InvalidateForcesAReread(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, func(cfg *plex.Config) { cfg.SectionTTL = time.Hour })

	_, err := c.Sections(context.Background())
	require.NoError(t, err)

	c.InvalidateSections()

	_, err = c.Sections(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, s.count())
}

func TestResolve_PicksTheSectionWhoseLocationContainsThePath(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	got, err := c.Resolve(context.Background(), "/media/yama/tv/Severance/Season 01/ep.mkv")
	require.NoError(t, err)

	require.Equal(t, plex.Target{
		Section:    plex.Section{Key: "2", Title: "TV-Yama", Type: "show", Locations: []string{"/media/yama/tv"}},
		RemotePath: "/media/yama/tv/Severance/Season 01/ep.mkv",
		RemoteDir:  "/media/yama/tv/Severance/Season 01",
	}, got)
}

func TestResolve_AppliesThePathMappingBeforeMatching(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, func(cfg *plex.Config) { cfg.Mapper = mappedMapper() })

	got, err := c.Resolve(context.Background(), "/mnt/pool/media/yama/movies/Arrival (2016)/Arrival.mkv")
	require.NoError(t, err)

	require.Equal(t, "1", got.Section.Key)
	require.Equal(t, "/media/yama/movies/Arrival (2016)/Arrival.mkv", got.RemotePath)
	require.Equal(t, "/media/yama/movies/Arrival (2016)", got.RemoteDir)
}

func TestResolve_FailsWhenNoSectionCoversThePath(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	_, err := c.Resolve(context.Background(), "/media/elsewhere/movies/x.mkv")
	require.ErrorIs(t, err, plex.ErrNoSection)
}

func TestResolve_RejectsARelativePath(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	_, err := c.Resolve(context.Background(), "media/yama/movies/x.mkv")
	require.Error(t, err)
	require.Equal(t, 0, s.count())
}

// /media never resolves inside /media-archive: prefixes are compared by whole
// component, so a sibling directory with a longer name cannot swallow a path.
func TestResolve_DoesNotMatchASiblingWithASharedPrefix(t *testing.T) {
	t.Parallel()

	s := newServer(t, sectionRoutes())
	c := newClient(t, s, nil)

	_, err := c.Resolve(context.Background(), "/media/yama/movies-archive/x.mkv")
	require.ErrorIs(t, err, plex.ErrNoSection)
}
