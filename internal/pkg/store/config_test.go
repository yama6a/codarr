package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

func TestSettingsStore_EnsureThenUpdate(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	_, err := s.GetSettings(t.Context())
	require.ErrorIs(t, err, store.ErrNotFound)

	defaults := domain.Settings{
		TempDir:             "/tmp",
		QSVDevice:           "/dev/dri/renderD128",
		ScanEnabled:         true,
		ScanCron:            "0 4 * * *",
		ScanRateLimitFPS:    50,
		PrioritiseQuickJobs: true,
		UpdatedAt:           testTime(),
	}

	require.NoError(t, s.EnsureSettings(t.Context(), defaults))

	got, err := s.GetSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, defaults, got)

	// A second call must not clobber what the user has since configured.
	require.NoError(t, s.EnsureSettings(t.Context(), domain.Settings{
		TempDir: "/other", QSVDevice: "/dev/dri/renderD129", UpdatedAt: testTime(),
	}))

	unchanged, err := s.GetSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, defaults, unchanged)

	updated := defaults
	updated.TempDir = "/mnt/scratch"
	updated.QueuePaused = true
	updated.FullHashEnabled = true

	require.NoError(t, s.UpdateSettings(t.Context(), updated))

	reloaded, err := s.GetSettings(t.Context())
	require.NoError(t, err)
	require.Equal(t, updated, reloaded)
}

func TestPlexStore_RoundTrip(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	_, err := s.GetPlexConfig(t.Context())
	require.ErrorIs(t, err, store.ErrNotFound)

	cfg := domain.PlexConfig{
		BaseURL:            "http://plex:32400",
		Token:              "secret",
		ClientIdentifier:   "codarr",
		RefreshAfter:       true,
		AnalyzeAfter:       true,
		GuardActiveStreams: true,
		UpdatedAt:          testTime(),
	}

	require.NoError(t, s.UpdatePlexConfig(t.Context(), cfg))

	got, err := s.GetPlexConfig(t.Context())
	require.NoError(t, err)
	require.Equal(t, cfg, got)

	require.NoError(t, s.SetPlexTestResult(t.Context(), testTime(), "ok"))

	tested, err := s.GetPlexConfig(t.Context())
	require.NoError(t, err)
	require.Equal(t, "ok", tested.LastTestResult)
	require.NotNil(t, tested.LastTestedAt)
	require.Equal(t, testTime(), *tested.LastTestedAt)
}

func TestPlexStore_ReplacePathMappingsResequencesSort(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	require.NoError(t, s.ReplacePlexPathMappings(t.Context(), []domain.PathMapping{
		{Local: "/library", Remote: "/data"},
		{Local: "/library/movies", Remote: "/data/movies"},
	}))

	first, err := s.ListPlexPathMappings(t.Context())
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "/library", first[0].Local)
	require.Equal(t, 0, first[0].Sort)
	require.Equal(t, 1, first[1].Sort)

	require.NoError(t, s.ReplacePlexPathMappings(t.Context(), []domain.PathMapping{
		{Local: "/only", Remote: "/data/only"},
	}))

	second, err := s.ListPlexPathMappings(t.Context())
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "/only", second[0].Local)
}

func TestArrStore_CRUDAndWebhookLookup(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	created, err := s.CreateArrInstance(t.Context(), domain.ArrInstance{
		Name: "radarr-4k", Flavour: domain.FlavourRadarr, BaseURL: "http://radarr:7878",
		APIKey: "key", WebhookID: "hook-1", RescanAfter: true, Enabled: true,
		CreatedAt: testTime(), UpdatedAt: testTime(),
	})
	require.NoError(t, err)
	require.Positive(t, created.ID)

	got, err := s.GetArrInstance(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, created, got)

	byHook, err := s.GetArrInstanceByWebhookID(t.Context(), "hook-1")
	require.NoError(t, err)
	require.Equal(t, created, byHook)

	_, err = s.GetArrInstanceByWebhookID(t.Context(), "nope")
	require.ErrorIs(t, err, store.ErrNotFound)

	updated := created
	updated.Name = "radarr-hd"
	updated.UnmonitorAfter = true

	require.NoError(t, s.UpdateArrInstance(t.Context(), updated))
	require.NoError(t, s.SetArrTestResult(t.Context(), created.ID, testTime(), "ok"))

	reloaded, err := s.GetArrInstance(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "radarr-hd", reloaded.Name)
	require.True(t, reloaded.UnmonitorAfter)
	require.Equal(t, "ok", reloaded.LastTestResult)

	all, err := s.ListArrInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 1)

	require.NoError(t, s.ReplaceArrPathMappings(t.Context(), created.ID, []domain.PathMapping{
		{Local: "/library/movies", Remote: "/movies"},
	}))

	mappings, err := s.ListArrPathMappings(t.Context(), created.ID)
	require.NoError(t, err)
	require.Len(t, mappings, 1)
	require.Equal(t, "/movies", mappings[0].Remote)

	require.NoError(t, s.DeleteArrInstance(t.Context(), created.ID))
	require.ErrorIs(t, s.DeleteArrInstance(t.Context(), created.ID), store.ErrNotFound)

	// ON DELETE CASCADE, which only bites because foreign_keys is on.
	gone, err := s.ListArrPathMappings(t.Context(), created.ID)
	require.NoError(t, err)
	require.Empty(t, gone)
}

func TestRootStore_CRUD(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	instance, err := s.CreateArrInstance(t.Context(), domain.ArrInstance{
		Name: "sonarr", Flavour: domain.FlavourSonarr, BaseURL: "http://sonarr:8989",
		APIKey: "key", WebhookID: "hook-2", Enabled: true,
		CreatedAt: testTime(), UpdatedAt: testTime(),
	})
	require.NoError(t, err)

	root, err := s.CreateRoot(t.Context(), domain.Root{
		Path: "/library/shows", ArrInstanceID: &instance.ID, Imported: true,
		Enabled: true, CreatedAt: testTime(),
	})
	require.NoError(t, err)

	got, err := s.GetRoot(t.Context(), root.ID)
	require.NoError(t, err)
	require.Equal(t, root, got)

	require.NoError(t, s.SetRootEnabled(t.Context(), root.ID, false))

	disabled, err := s.GetRoot(t.Context(), root.ID)
	require.NoError(t, err)
	require.False(t, disabled.Enabled)

	all, err := s.ListRoots(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 1)

	// ON DELETE SET NULL: a root outlives the instance that named it.
	require.NoError(t, s.DeleteArrInstance(t.Context(), instance.ID))

	orphaned, err := s.GetRoot(t.Context(), root.ID)
	require.NoError(t, err)
	require.Nil(t, orphaned.ArrInstanceID)

	require.NoError(t, s.DeleteRoot(t.Context(), root.ID))
	require.ErrorIs(t, s.DeleteRoot(t.Context(), root.ID), store.ErrNotFound)
}

func TestHardwareStore_ReplaceSwapsTheWholeProbe(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	require.NoError(t, s.ReplaceHWCapabilities(t.Context(), []domain.HWCapability{
		{
			Backend: "qsv", Codec: "hevc", Profile: "main", Direction: "encode", Works: true,
			FfmpegVersion: "7.1", ProbedAt: testTime(),
		},
		{
			Backend: "qsv", Codec: "hevc", Profile: "main10", Direction: "encode", Works: false,
			Error: "unsupported", FfmpegVersion: "7.1", ProbedAt: testTime(),
		},
	}))

	first, err := s.ListHWCapabilities(t.Context())
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Equal(t, "main", first[0].Profile)
	require.True(t, first[0].Works)
	require.Equal(t, "unsupported", first[1].Error)

	require.NoError(t, s.ReplaceHWCapabilities(t.Context(), []domain.HWCapability{
		{
			Backend: "vaapi", Codec: "hevc", Profile: "main", Direction: "encode", Works: true,
			FfmpegVersion: "7.2", ProbedAt: testTime(),
		},
	}))

	second, err := s.ListHWCapabilities(t.Context())
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "vaapi", second[0].Backend)
}

func TestThroughputStore_UpsertKeysOnKindEncoderResolution(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	stat := domain.ThroughputStat{
		Kind: domain.KindFull, Encoder: "hevc_qsv", Resolution: "1080p",
		Samples: 3, AvgValue: 120.5, UpdatedAt: testTime(),
	}

	require.NoError(t, s.UpsertThroughputStat(t.Context(), stat))

	got, err := s.GetThroughputStat(t.Context(), domain.KindFull, "hevc_qsv", "1080p")
	require.NoError(t, err)
	require.Equal(t, stat.Samples, got.Samples)
	require.InEpsilon(t, stat.AvgValue, got.AvgValue, 0.0001)

	stat.Samples = 4
	stat.AvgValue = 118.25
	require.NoError(t, s.UpsertThroughputStat(t.Context(), stat))

	all, err := s.ListThroughputStats(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, 4, all[0].Samples)
	require.InEpsilon(t, 118.25, all[0].AvgValue, 0.0001)

	_, err = s.GetThroughputStat(t.Context(), domain.KindRemux, "", "")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestStatsStore_SumsCompletedJobs(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	for i, path := range []string{"/library/one.mkv", "/library/two.mkv"} {
		media := seedMedia(t, s, path)
		job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

		require.NoError(t, s.UpdateJobExecution(t.Context(), store.ExecutionUpdate{
			JobID: job.ID, SourceSize: 1000, EstimatedSeconds: 100,
		}))
		require.NoError(t, s.RecordPromotion(t.Context(), store.PromotionUpdate{
			JobID: job.ID, MediaFileID: media.ID, OutputFingerprint: "xxh3-128:out",
			OutputSize: 600, OutputMTime: int64(i), PolicyHash: "a41f9c22",
			ActualSeconds: 90, PromotedAt: testTime(),
		}))
	}

	stats, err := s.Stats(t.Context())
	require.NoError(t, err)
	require.Equal(t, store.Stats{
		FilesDone:     2,
		BytesIn:       2000,
		BytesOut:      1200,
		BytesSaved:    800,
		EncodeSeconds: 180,
	}, stats)

	counts, err := s.CountJobsByState(t.Context())
	require.NoError(t, err)
	require.Equal(t, map[domain.JobState]int{domain.JobDone: 2}, counts)
}
