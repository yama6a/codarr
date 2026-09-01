package arr_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

func parse(t *testing.T, flavour domain.Flavour, name string) arr.Event {
	t.Helper()

	e, err := arr.ParseWebhook(flavour, fixture(name))
	require.NoError(t, err)

	return e
}

func TestParseWebhook_ReadsARadarrDownload(t *testing.T) {
	t.Parallel()

	require.Equal(t, arr.Event{
		Flavour:      domain.FlavourRadarr,
		Type:         arr.EventDownload,
		InstanceName: "Radarr-Yama",
		Title:        "Arrival",
		IsUpgrade:    false,
		Item:         arr.ItemRef{MovieID: 412},
		Files: []arr.EventFile{{
			ID:           901,
			RelativePath: "Arrival (2016) Bluray-1080p.mkv",
			RemotePath:   "/media/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv",
		}},
	}, parse(t, domain.FlavourRadarr, "radarr_webhook_download.json"))
}

func TestParseWebhook_ReadsARadarrUpgrade(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourRadarr, "radarr_webhook_upgrade.json")
	require.True(t, got.IsUpgrade)
	require.Equal(t, "/media/movies/Arrival (2016)/Arrival (2016) Bluray-2160p.mkv", got.Files[0].RemotePath)
}

func TestParseWebhook_ReadsASonarrDownload(t *testing.T) {
	t.Parallel()

	require.Equal(t, arr.Event{
		Flavour:      domain.FlavourSonarr,
		Type:         arr.EventDownload,
		InstanceName: "Sonarr-Yama",
		Title:        "Severance",
		Item:         arr.ItemRef{SeriesID: 77, EpisodeIDs: []int64{5501}},
		Files: []arr.EventFile{{
			ID:           9001,
			RelativePath: "Season 01/Severance - S01E01 - Good News About Hell WEBDL-1080p.mkv",
			RemotePath:   "/media/Severance/Season 01/Severance - S01E01 - Good News About Hell WEBDL-1080p.mkv",
		}},
	}, parse(t, domain.FlavourSonarr, "sonarr_webhook_download.json"))
}

// A multi-episode file carries every episode id, which is what unmonitoring
// needs on Sonarr (plan.md 16.2).
func TestParseWebhook_CollectsEveryEpisodeIDOnAMultiEpisodeFile(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourSonarr, "sonarr_webhook_multiepisode.json")
	require.Equal(t, arr.ItemRef{SeriesID: 77, EpisodeIDs: []int64{5501, 5502}}, got.Item)
	require.True(t, got.IsUpgrade)
}

// Rename carries renamedMovieFiles, not movieFile, which plan.md 13.1 does not
// say. Reading only movieFile would parse a rename as an event with no files
// and leave the stored path pointing at a file that no longer exists.
func TestParseWebhook_ReadsARadarrRenameFromRenamedMovieFiles(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourRadarr, "radarr_webhook_rename.json")

	require.Equal(t, arr.EventRename, got.Type)
	require.Equal(t, []arr.EventFile{{
		ID:                 901,
		RelativePath:       "Arrival (2016) Bluray-1080p x265 DTS.mkv",
		RemotePath:         "/media/movies/Arrival (2016)/Arrival (2016) Bluray-1080p x265 DTS.mkv",
		PreviousRemotePath: "/media/movies/Arrival (2016)/Arrival (2016) Bluray-1080p x264 DTS.mkv",
	}}, got.Files)
}

func TestParseWebhook_ReadsASonarrRenameFromRenamedEpisodeFiles(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourSonarr, "sonarr_webhook_rename.json")

	require.Equal(t, arr.EventRename, got.Type)
	require.Len(t, got.Files, 1)
	require.Contains(t, got.Files[0].PreviousRemotePath, "x264")
	require.Contains(t, got.Files[0].RemotePath, "x265")
}

func TestParseWebhook_ReadsAMovieFileDelete(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourRadarr, "radarr_webhook_moviefiledelete.json")

	require.Equal(t, arr.EventMovieFileDelete, got.Type)
	require.True(t, got.Deletion())
	require.Equal(t, "Upgrade", got.DeleteReason)
	require.Equal(t, "/media/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv", got.Files[0].RemotePath)
}

func TestParseWebhook_ReadsAnEpisodeFileDelete(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourSonarr, "sonarr_webhook_episodefiledelete.json")

	require.Equal(t, arr.EventEpisodeFileDelete, got.Type)
	require.True(t, got.Deletion())
	require.Equal(t, arr.ItemRef{SeriesID: 77, EpisodeIDs: []int64{5501}}, got.Item)
}

// The Test payload has a Windows placeholder path and no file at all. It must
// still parse, because the handler has to answer 200 with a body for the *arr's
// Test button to report success (plan.md 13.1).
func TestParseWebhook_AcceptsTheTestPayloadWithNoFile(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		flavour domain.Flavour
		fixture string
	}{
		"radarr": {domain.FlavourRadarr, "radarr_webhook_test.json"},
		"sonarr": {domain.FlavourSonarr, "sonarr_webhook_test.json"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := parse(t, tc.flavour, tc.fixture)
			require.Equal(t, arr.EventTest, got.Type)
			require.True(t, got.Handled())
			require.Empty(t, got.Files)
		})
	}
}

func TestParseWebhook_ParsesAnUnhandledEventWithoutFailing(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourRadarr, "radarr_webhook_grab.json")

	require.Equal(t, arr.EventType("Grab"), got.Type)
	require.False(t, got.Handled())
	require.False(t, got.Deletion())
}

// The payloads carry far more than this. Ignoring the rest is the point: they
// are external schemas that change with every *arr release (plan.md 13.1).
func TestParseWebhook_IgnoresFieldsItDoesNotRead(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"eventType": "Download",
		"movie": {"id": 9, "title": "X", "folderPath": "/media/X", "somethingNew": {"deeply": ["nested"]}},
		"movieFile": {"id": 3, "relativePath": "X.mkv", "path": "/media/X/X.mkv", "futureField": 1},
		"grabbedFromTheFuture": true
	}`)

	got, err := arr.ParseWebhook(domain.FlavourRadarr, body)
	require.NoError(t, err)
	require.Equal(t, int64(9), got.Item.MovieID)
	require.Equal(t, "/media/X/X.mkv", got.Files[0].RemotePath)
}

// Both *arrs set path today. The fallback removes the dependency on that
// staying true across releases.
func TestParseWebhook_FallsBackToTheFolderPlusRelativePath(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"eventType": "Download",
		"series": {"id": 1, "title": "S", "path": "/media/S"},
		"episodes": [{"id": 2}],
		"episodeFile": {"id": 3, "relativePath": "Season 01/S - S01E01.mkv"}
	}`)

	got, err := arr.ParseWebhook(domain.FlavourSonarr, body)
	require.NoError(t, err)
	require.Equal(t, "/media/S/Season 01/S - S01E01.mkv", got.Files[0].RemotePath)
}

func TestParseWebhook_RejectsAHandledEventWithNoPath(t *testing.T) {
	t.Parallel()

	_, err := arr.ParseWebhook(domain.FlavourRadarr, []byte(`{"eventType":"Download","movie":{"id":1}}`))
	require.ErrorIs(t, err, arr.ErrBadPayload)
}

func TestParseWebhook_RejectsNonJSON(t *testing.T) {
	t.Parallel()

	_, err := arr.ParseWebhook(domain.FlavourSonarr, []byte("not json"))
	require.ErrorIs(t, err, arr.ErrBadPayload)
}

func TestParseWebhook_RejectsAnUnknownFlavour(t *testing.T) {
	t.Parallel()

	_, err := arr.ParseWebhook("lidarr", []byte("{}"))
	require.ErrorIs(t, err, arr.ErrUnknownFlavour)
}

// The instance's own view of the filesystem is "/media"; only its mappings turn
// that into a path Codarr can open (plan.md 13.1, VERIFY.md).
func TestEventLocal_RewritesWithTheSendingInstancesMappings(t *testing.T) {
	t.Parallel()

	got := parse(t, domain.FlavourSonarr, "sonarr_webhook_download.json").Local(sonarrMapper())

	require.Equal(t, []arr.LocalFile{{
		ID:     9001,
		Path:   "/media/yama/tv/Severance/Season 01/Severance - S01E01 - Good News About Hell WEBDL-1080p.mkv",
		Mapped: true,
	}}, got)
}

func TestEventLocal_RewritesBothSidesOfARename(t *testing.T) {
	t.Parallel()

	mapper := pathmap.New([]domain.PathMapping{{Local: "/media/yama/movies", Remote: "/media/movies"}})
	got := parse(t, domain.FlavourRadarr, "radarr_webhook_rename.json").Local(mapper)

	require.Equal(t, []arr.LocalFile{{
		ID:           901,
		Path:         "/media/yama/movies/Arrival (2016)/Arrival (2016) Bluray-1080p x265 DTS.mkv",
		PreviousPath: "/media/yama/movies/Arrival (2016)/Arrival (2016) Bluray-1080p x264 DTS.mkv",
		Mapped:       true,
	}}, got)
}

func TestEventLocal_ReportsAPathNoMappingRewrote(t *testing.T) {
	t.Parallel()

	mapper := pathmap.New([]domain.PathMapping{{Local: "/media/yama/tv", Remote: "/data"}})
	got := parse(t, domain.FlavourRadarr, "radarr_webhook_download.json").Local(mapper)

	require.False(t, got[0].Mapped)
	require.Equal(t, "/media/movies/Arrival (2016)/Arrival (2016) Bluray-1080p.mkv", got[0].Path)
}

func TestEventLocal_IsEmptyForTheTestPayload(t *testing.T) {
	t.Parallel()

	require.Empty(t, parse(t, domain.FlavourRadarr, "radarr_webhook_test.json").Local(radarrMapper()))
}
