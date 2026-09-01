package plex_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/plex"
	"github.com/yama6a/codarr/internal/promote"
)

var _ promote.StreamGuard = (*plex.Client)(nil)

const episodePath = "/media/yama/tv/Severance/Season 01/Severance - S01E01 - Good News About Hell.mkv"

func TestSessions_ReadsTheFileStraightOffADirectPlaySession(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_directplay.json"},
	})
	c := newClient(t, s, nil)

	got, err := c.Sessions(context.Background())
	require.NoError(t, err)

	require.Equal(t, []plex.Session{{
		RatingKey:   "1201",
		Title:       "Arrival",
		User:        "yama",
		Player:      "Chrome (Plex Web)",
		Transcoding: false,
		LocalPaths:  []string{moviePath},
	}}, got)

	require.Equal(t, 1, s.count())
}

// plan.md 16.1: a transcoding session's Part carries no file attribute, so the
// path only exists on the item itself.
func TestSessions_FetchesTheItemWhenTheSessionCarriesNoFile(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions":       {fixture: "sessions_transcode.json"},
		"GET /library/metadata/1477": {fixture: "metadata_1477.json"},
	})
	c := newClient(t, s, nil)

	got, err := c.Sessions(context.Background())
	require.NoError(t, err)

	require.Equal(t, []plex.Session{{
		RatingKey:   "1477",
		Title:       "Severance - S01E01 - Good News About Hell",
		User:        "kostas",
		Player:      "Living Room (Plex for Apple TV)",
		Transcoding: true,
		LocalPaths:  []string{episodePath},
	}}, got)
}

func TestSessions_IsEmptyOnAnIdleServer(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_empty.json"},
	})
	c := newClient(t, s, nil)

	got, err := c.Sessions(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

// The sessions listing is never cached: promote asks again immediately before the
// rename, and a cached answer reopens the race the guard closes (plan.md 15.6).
func TestSessions_IsNeverServedFromCache(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_directplay.json"},
	})
	c := newClient(t, s, nil)

	for range 3 {
		_, err := c.Sessions(context.Background())
		require.NoError(t, err)
	}

	require.Equal(t, 3, s.count())
}

func TestSessions_ReusesTheItemLookupUntilItsTTLExpires(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	s := newServer(t, map[string]route{
		"GET /status/sessions":       {fixture: "sessions_transcode.json"},
		"GET /library/metadata/1477": {fixture: "metadata_1477.json"},
	})
	c := newClient(t, s, func(cfg *plex.Config) {
		cfg.Clock = fake
		cfg.PartTTL = 30 * time.Second
	})

	for range 3 {
		_, err := c.Sessions(context.Background())
		require.NoError(t, err)
	}

	metadataCalls := 0

	for _, r := range s.seen() {
		if r.Path == "/library/metadata/1477" {
			metadataCalls++
		}
	}

	require.Equal(t, 1, metadataCalls)

	fake.Advance(30 * time.Second)

	_, err := c.Sessions(context.Background())
	require.NoError(t, err)

	metadataCalls = 0

	for _, r := range s.seen() {
		if r.Path == "/library/metadata/1477" {
			metadataCalls++
		}
	}

	require.Equal(t, 2, metadataCalls)
}

func TestIsStreaming_BlocksTheFileBeingDirectPlayed(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_directplay.json"},
	})
	c := newClient(t, s, nil)

	streaming, who, err := c.IsStreaming(context.Background(), moviePath)
	require.NoError(t, err)
	require.True(t, streaming)
	require.Equal(t, "yama is watching Arrival on Chrome (Plex Web)", who)
}

func TestIsStreaming_BlocksTheFileBeingTranscoded(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions":       {fixture: "sessions_transcode.json"},
		"GET /library/metadata/1477": {fixture: "metadata_1477.json"},
	})
	c := newClient(t, s, nil)

	streaming, who, err := c.IsStreaming(context.Background(), episodePath)
	require.NoError(t, err)
	require.True(t, streaming)
	require.Equal(t,
		"kostas is watching Severance - S01E01 - Good News About Hell on Living Room (Plex for Apple TV) (transcoding)",
		who)
}

func TestIsStreaming_LetsAnUnrelatedFileThrough(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_directplay.json"},
	})
	c := newClient(t, s, nil)

	streaming, who, err := c.IsStreaming(context.Background(), episodePath)
	require.NoError(t, err)
	require.False(t, streaming)
	require.Empty(t, who)
}

// The mapping is a no-op on this cluster (VERIFY.md), but the reverse step still has to
// run or a mapped deployment compares a Plex path to a local one and never matches.
func TestIsStreaming_ReversesThePathMappingBeforeComparing(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {fixture: "sessions_directplay.json"},
	})
	c := newClient(t, s, func(cfg *plex.Config) { cfg.Mapper = mappedMapper() })

	streaming, _, err := c.IsStreaming(context.Background(),
		"/mnt/pool/media/yama/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv")
	require.NoError(t, err)
	require.True(t, streaming)
}

func TestIsStreaming_RejectsARelativePath(t *testing.T) {
	t.Parallel()

	s := newServer(t, nil)
	c := newClient(t, s, nil)

	_, _, err := c.IsStreaming(context.Background(), "relative/path.mkv")
	require.Error(t, err)
	require.Equal(t, 0, s.count())
}

// An unanswerable Plex is an error here, never a false "not streaming". promote
// turns it into a deferral, which is the safe closed state (plan.md 15.6).
func TestIsStreaming_ReportsAnUnreachableServerRatherThanGuessing(t *testing.T) {
	t.Parallel()

	s := newServer(t, map[string]route{
		"GET /status/sessions": {status: http.StatusInternalServerError, body: `{}`},
	})
	c := newClient(t, s, nil)

	streaming, _, err := c.IsStreaming(context.Background(), moviePath)
	require.Error(t, err)
	require.False(t, streaming)
}
