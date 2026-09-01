package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
)

func TestGetHealth_IsAlwaysOk(t *testing.T) {
	t.Parallel()

	got := decodeInto[gen.Health](t, newHarness(t).do(t, "GET", "/api/health", nil), 200)
	require.Equal(t, gen.HealthStatusOk, got.Status)
}

// plan.md 20: readiness reports per-dependency checks. The database is the only
// hard dependency; an unconfigured Plex is a state, not an outage.
func TestGetReady_Is503WhenTheDatabaseDoesNotAnswer(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.db.PingContextFunc = func(context.Context) error { return errUnreadable }

	got := decodeInto[gen.Readiness](t, h.do(t, "GET", "/api/ready", nil), 503)

	require.False(t, got.Ready)
	require.Len(t, got.Checks, 1)
	require.Equal(t, "database", got.Checks[0].Name)
	require.False(t, got.Checks[0].Ok)
	require.NotNil(t, got.Checks[0].Message)
}

func TestGetReady_Is200WhenTheDatabaseAnswers(t *testing.T) {
	t.Parallel()

	got := decodeInto[gen.Readiness](t, newHarness(t).do(t, "GET", "/api/ready", nil), 200)

	require.True(t, got.Ready)
	require.True(t, got.Checks[0].Ok)
}

func TestGetStats_SumsTheStatusCountsIntoATotal(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	dashboardStore(h)

	got := decodeInto[gen.Stats](t, h.do(t, "GET", "/api/stats", nil), 200)

	require.Equal(t, 1000, got.FilesTotal)
	require.Equal(t, 100, got.FilesPending)
	require.Equal(t, int64(400), got.BytesSaved)
	require.NotNil(t, got.AvgSavingPct)
	require.InDelta(t, 40.0, *got.AvgSavingPct, 0.001)
}

// plan.md 18.5: the log view is a cursor read, so has_more and next_since_id are
// what the poll uses rather than a page number.
func TestListEvents_PagesByCursorAndExpandsTheMinimumLevel(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	var seen store.EventFilter

	h.store.ListEventsFunc = func(_ context.Context, f store.EventFilter) ([]domain.Event, error) {
		seen = f

		rows := make([]domain.Event, 0, f.Limit)
		for i := range f.Limit {
			rows = append(rows, domain.Event{
				ID: int64(100 + i), Level: "warn", Category: "job",
				Message: "something", CreatedAt: testNow,
			})
		}

		return rows, nil
	}

	got := decodeInto[gen.EventPage](t, h.do(t, "GET", "/api/events?level=warn&category=job&since_id=99&limit=2", nil), 200)

	require.Equal(t, []string{"warn", "error"}, seen.Level)
	require.Equal(t, []string{"job"}, seen.Category)
	require.Equal(t, int64(99), seen.SinceID)
	require.Equal(t, 3, seen.Limit, "one row over the limit is what tells has_more from a full page")

	require.Len(t, got.Items, 2)
	require.True(t, got.HasMore)
	require.Equal(t, int64(101), got.NextSinceId)
	require.Equal(t, gen.EventLevelWarn, got.Items[0].Level)
}

func TestListEvents_EmptyPageKeepsTheCursor(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.ListEventsFunc = func(context.Context, store.EventFilter) ([]domain.Event, error) {
		return nil, nil
	}

	got := decodeInto[gen.EventPage](t, h.do(t, "GET", "/api/events?since_id=42", nil), 200)

	require.False(t, got.HasMore)
	require.Empty(t, got.Items)
	require.Equal(t, int64(42), got.NextSinceId)
}

func TestGetSettings_AndUpdateSettings(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	current := domain.Settings{
		TempDir: "/tmp", QSVDevice: "/dev/dri/renderD128", ScanEnabled: true,
		ScanCron: "0 4 * * *", ScanRateLimitFPS: 50, QueuePaused: true, PrioritiseQuickJobs: true,
	}

	h.store.GetSettingsFunc = func(context.Context) (domain.Settings, error) { return current, nil }
	h.store.UpdateSettingsFunc = func(_ context.Context, s domain.Settings) error {
		current = s

		return nil
	}

	got := decodeInto[gen.Settings](t, h.do(t, "GET", "/api/settings", nil), 200)
	require.Equal(t, "/tmp", got.TempDir)
	require.True(t, got.QueuePaused)

	updated := decodeInto[gen.Settings](t, h.do(t, "PUT", "/api/settings", gen.SettingsUpdate{
		FullHashEnabled:     true,
		PrioritiseQuickJobs: false,
		QsvDevice:           "/dev/dri/renderD129",
		ScanCron:            "30 3 * * *",
		ScanEnabled:         false,
		ScanRateLimitFps:    120,
		TempDir:             "/var/tmp/codarr",
	}), 200)

	require.Equal(t, "/var/tmp/codarr", updated.TempDir)
	require.Equal(t, "/dev/dri/renderD129", updated.QsvDevice)
	require.Equal(t, 120, updated.ScanRateLimitFps)
	require.True(t, updated.FullHashEnabled)

	// Pausing is a queue operation, so a settings PUT must not change it.
	require.True(t, updated.QueuePaused)
}

func TestUpdateSettings_RejectsAnAbsurdScanRate(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		return domain.Settings{TempDir: "/tmp", QSVDevice: "/dev/dri/renderD128"}, nil
	}

	rec := h.do(t, "PUT", "/api/settings", gen.SettingsUpdate{
		QsvDevice:        "/dev/dri/renderD128",
		ScanRateLimitFps: 1_000_000,
		TempDir:          "/tmp",
	})

	require.Equal(t, 400, rec.Code, rec.Body.String())
	require.Empty(t, h.store.UpdateSettingsCalls())
}

func TestGetHardware_ServesTheCacheAndProbeForcesAFreshRun(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	caps := hardware.Capabilities{
		Device:        "/dev/dri/renderD128",
		FfmpegVersion: "7.1",
		ProbedAt:      testNow,
		Entries: []domain.HWCapability{
			{Backend: "qsv", Codec: "hevc", Profile: "main", Direction: "encode", Works: true, ProbedAt: testNow},
			{Backend: "qsv", Codec: "hevc", Profile: "main10", Direction: "encode", Works: true, ProbedAt: testNow},
		},
	}

	h.hardware.CapabilitiesFunc = func(context.Context) (hardware.Capabilities, error) { return caps, nil }
	h.hardware.ProbeFunc = func(context.Context) (hardware.Capabilities, error) { return caps, nil }

	got := decodeInto[gen.Hardware](t, h.do(t, "GET", "/api/hardware", nil), 200)

	require.Len(t, got.Capabilities, 2)
	require.Equal(t, "/dev/dri/renderD128", got.QsvDevice)
	require.NotNil(t, got.FfmpegVersion)
	require.Equal(t, "7.1", *got.FfmpegVersion)
	require.NotNil(t, got.SelectedEncoder)
	require.Equal(t, gen.EncoderHevcQsv, *got.SelectedEncoder)
	require.NotEmpty(t, got.HardwareDecodeCodecs)
	require.Len(t, h.hardware.CapabilitiesCalls(), 1)
	require.Empty(t, h.hardware.ProbeCalls())

	require.Equal(t, 200, h.do(t, "POST", "/api/hardware/probe", nil).Code)
	require.Len(t, h.hardware.ProbeCalls(), 1)
}

func TestListJobs_AndGetJobCarryTheTransformRecord(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)

	actual := 1180
	j := domain.Job{
		ID: 4, MediaFileID: 7, Kind: domain.KindFull, Origin: domain.OriginIngest,
		State: domain.JobDone, Priority: 110, EncoderUsed: domain.EncoderQSV,
		DecodePath: domain.DecodeHardware, SourceSize: 8_000, OutputSize: 4_000,
		ActualSeconds: actual, FfmpegArgv: []string{"-nostdin", "-i", "in.mkv"},
		Transform: domain.TransformRecord{
			Container: domain.BeforeAfterString{Before: "matroska", After: "matroska"},
			Video: domain.VideoTransform{
				Action: domain.DecisionEncode, Reason: "h264 High",
				Before: &domain.VideoState{Codec: "h264", Width: 1920, Height: 1080, Scan: domain.ScanProgressive},
				After:  &domain.VideoState{Codec: "hevc", Width: 1920, Height: 1080, Scan: domain.ScanProgressive},
			},
			Audio: []domain.AudioTransform{{
				SourceIndex: 0, Language: "eng", Action: domain.DecisionEncode,
				Before: &domain.AudioState{Codec: "dts", Channels: 6, Layout: "5.1"},
				After:  &domain.AudioState{Codec: "ac3", Channels: 6, Layout: "5.1"},
			}},
			Subtitles: []domain.SubtitleTransform{{
				SourceIndex: 0, Language: "eng", Action: domain.DecisionDrop,
				Before: &domain.SubtitleState{Codec: "hdmv_pgs_subtitle", Forced: true},
			}},
			Size:     domain.SizeTransform{BeforeBytes: 8_000, AfterBytes: 4_000},
			Duration: domain.DurationTransform{Estimated: 1200, Actual: &actual},
			OutputIdentity: &domain.OutputIdentity{
				Fingerprint: "xxh3-128:out", SizeBytes: 4_000, MTime: 1, PolicyHash: "914f0f87", RecordedAt: testNow,
			},
		},
	}

	h.store.ListJobsFunc = func(context.Context, store.JobFilter) ([]domain.Job, int, error) {
		return []domain.Job{j}, 1, nil
	}
	h.store.GetJobFunc = func(context.Context, int64) (domain.Job, error) { return j, nil }
	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return mediaFixture(), nil }

	page := decodeInto[gen.JobPage](t, h.do(t, "GET", "/api/jobs?state=done&page=1&page_size=10", nil), 200)
	require.Equal(t, 1, page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, "Dune.mkv", page.Items[0].MediaFilename)

	detail := decodeInto[gen.Job](t, h.do(t, "GET", "/api/jobs/4", nil), 200)
	require.Equal(t, gen.DecisionEncode, detail.Transform.Video.Action)
	require.Equal(t, "hevc", detail.Transform.Video.After.Codec)
	require.Len(t, detail.Transform.Audio, 1)
	require.Len(t, detail.Transform.Subtitles, 1)
	require.NotNil(t, detail.Transform.OutputIdentity)
	require.Equal(t, "xxh3-128:out", detail.Transform.OutputIdentity.Fingerprint)
	require.NotNil(t, detail.FfmpegArgv)
	require.Equal(t, []string{"-nostdin", "-i", "in.mkv"}, *detail.FfmpegArgv)
}

func TestCancelAndRestartJob(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	noInstances(h.store)

	j := domain.Job{ID: 4, MediaFileID: 7, Kind: domain.KindFull, Origin: domain.OriginManual, State: domain.JobRunning}

	h.store.GetJobFunc = func(context.Context, int64) (domain.Job, error) { return j, nil }
	h.store.GetMediaFileFunc = func(context.Context, int64) (domain.MediaFile, error) { return mediaFixture(), nil }
	h.queue.CancelFunc = func(_ context.Context, id int64) error {
		require.Equal(t, int64(4), id)
		j.State = domain.JobCancelled

		return nil
	}
	h.queue.RestartFunc = func(context.Context, int64) (domain.Job, error) {
		j.State = domain.JobQueued

		return j, nil
	}

	require.Equal(t, gen.JobStateCancelled,
		decodeInto[gen.Job](t, h.do(t, "POST", "/api/jobs/4/cancel", nil), 200).State)
	require.Equal(t, gen.JobStateQueued,
		decodeInto[gen.Job](t, h.do(t, "POST", "/api/jobs/4/restart", nil), 200).State)
}

func TestListRootsAndDeleteRoot(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		id := int64(1)

		return []domain.Root{{ID: 3, Path: "/media/movies", ArrInstanceID: &id, Enabled: true, Imported: true}}, nil
	}
	h.store.DeleteRootFunc = func(context.Context, int64) error { return nil }

	list := decodeInto[gen.RootList](t, h.do(t, "GET", "/api/roots", nil), 200)
	require.Len(t, list.Roots, 1)
	require.NotNil(t, list.Roots[0].ArrInstanceName)
	require.Equal(t, "radarr-4k", *list.Roots[0].ArrInstanceName)
	require.True(t, list.Roots[0].Imported)
	require.Empty(t, list.Conflicts)

	require.Equal(t, 204, h.do(t, "DELETE", "/api/roots/3", nil).Code)
	require.Len(t, h.store.DeleteRootCalls(), 1)
}

func TestDeleteArrInstance(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.DeleteArrInstanceFunc = func(context.Context, int64) error { return nil }

	require.Equal(t, 204, h.do(t, "DELETE", "/api/arr/1", nil).Code)
	require.Len(t, h.store.DeleteArrInstanceCalls(), 1)
}

// A reachable instance that rejects the API key is a successful test with ok
// false, not an error.
func TestTestArrInstance_RecordsTheResult(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	h.store.SetArrTestResultFunc = func(context.Context, int64, time.Time, string) error { return nil }
	h.arr.TestFunc = func(context.Context) arr.TestResult {
		return arr.TestResult{OK: false, Message: "radarr rejected the api key", AppName: "Radarr", Version: "5.0"}
	}

	got := decodeInto[gen.TestResult](t, h.do(t, "POST", "/api/arr/1/test", nil), 200)

	require.False(t, got.Ok)
	require.Equal(t, "radarr rejected the api key", got.Message)
	require.Equal(t, testNow, got.TestedAt.UTC())
	require.Len(t, h.store.SetArrTestResultCalls(), 1)
}

func TestTestPlex_RecordsTheResult(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cfg := domain.PlexConfig{BaseURL: "http://plex:32400", Token: storedToken}
	withPlexConfig(h.store, &cfg)

	h.store.SetPlexTestResultFunc = func(context.Context, time.Time, string) error { return nil }
	h.plex.TestFunc = func(context.Context) plex.TestResult {
		return plex.TestResult{OK: true, Message: "connected", ServerName: "tower", ServerVersion: "1.40"}
	}

	got := decodeInto[gen.TestResult](t, h.do(t, "POST", "/api/plex/test", nil), 200)

	require.True(t, got.Ok)
	require.NotNil(t, got.ServerName)
	require.Equal(t, "tower", *got.ServerName)
}

func TestListPlexLibraries(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.plex.SectionsFunc = func(context.Context) ([]plex.Section, error) {
		return []plex.Section{{Key: "1", Title: "Movies", Type: "movie", Locations: []string{"/data/movies"}}}, nil
	}

	got := decodeInto[[]gen.PlexLibrary](t, h.do(t, "GET", "/api/plex/libraries", nil), 200)

	require.Len(t, got, 1)
	require.Equal(t, "Movies", got[0].Title)
	require.Equal(t, []string{"/data/movies"}, got[0].Locations)
}

func TestListArrRootFolders_ReportsTheMappedLocalPath(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) { return nil, nil }
	h.arr.RootFoldersFunc = func(context.Context) ([]arr.RootFolder, error) {
		return []arr.RootFolder{{
			ID: 1, Accessible: true,
			Imported: pathmap.ImportedRoot{
				ArrInstanceID: 1, ReportedPath: "/media", Path: "/media/movies", Mapped: true,
			},
		}}, nil
	}

	got := decodeInto[[]gen.ArrRootFolder](t, h.do(t, "GET", "/api/arr/1/rootfolders", nil), 200)

	require.Len(t, got, 1)
	require.Equal(t, "/media", got[0].Path)
	require.Equal(t, "/media/movies", got[0].LocalPath)
	require.NotNil(t, got[0].AlreadyImported)
	require.False(t, *got[0].AlreadyImported)
}

// plan.md 16.2: a root another enabled instance already claims is surfaced, not
// guessed at.
func TestImportArrRoots_SurfacesConflictsAndSkipsWhatItAlreadyOwns(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	own, other := int64(1), int64(2)
	instances := []domain.ArrInstance{
		{ID: 1, Name: "radarr-4k", Flavour: domain.FlavourRadarr, APIKey: "k", Enabled: true},
		{ID: 2, Name: "radarr-hd", Flavour: domain.FlavourRadarr, APIKey: "k", Enabled: true},
	}

	h.store.ListArrInstancesFunc = func(context.Context) ([]domain.ArrInstance, error) { return instances, nil }
	h.store.GetArrInstanceFunc = func(_ context.Context, id int64) (domain.ArrInstance, error) {
		return instances[id-1], nil
	}
	h.store.ListArrPathMappingsFunc = func(context.Context, int64) ([]domain.PathMapping, error) { return nil, nil }
	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return []domain.Root{
			{ID: 1, Path: "/media/movies", ArrInstanceID: &own, Enabled: true},
			{ID: 2, Path: "/media/movies-hd", ArrInstanceID: &other, Enabled: true},
		}, nil
	}
	h.store.CreateRootFunc = func(_ context.Context, r domain.Root) (domain.Root, error) {
		r.ID = 9

		return r, nil
	}

	h.arr.RootFoldersFunc = func(context.Context) ([]arr.RootFolder, error) {
		return []arr.RootFolder{
			{ID: 1, Imported: pathmap.ImportedRoot{ArrInstanceID: 1, Path: "/media/movies", Mapped: true}},
			{ID: 2, Imported: pathmap.ImportedRoot{ArrInstanceID: 1, Path: "/media/movies-hd", Mapped: true}},
			{ID: 3, Imported: pathmap.ImportedRoot{ArrInstanceID: 1, Path: "/media/movies-4k", Mapped: true}},
		}, nil
	}

	got := decodeInto[gen.ImportRootsResult](t, h.do(t, "POST", "/api/arr/1/import-roots", nil), 200)

	require.Equal(t, 1, got.Skipped)
	require.Equal(t, 1, got.Imported)
	require.Len(t, got.Roots, 1)
	require.Equal(t, "/media/movies-4k", got.Roots[0].Path)
	require.Len(t, got.Conflicts, 1)
	require.Equal(t, "/media/movies-hd", got.Conflicts[0].Path)
	require.Equal(t, "radarr-hd", got.Conflicts[0].OwningArrInstanceName)
}

// An unmapped root folder means every instance would claim the same path, which
// is a configuration error rather than something to import.
func TestImportArrRoots_RefusesAnUnmappedRootFolder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	instance := arrInstanceFixture()
	withArrInstance(h.store, &instance)

	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) { return nil, nil }
	h.arr.RootFoldersFunc = func(context.Context) ([]arr.RootFolder, error) {
		return []arr.RootFolder{
			{ID: 1, Imported: pathmap.ImportedRoot{ArrInstanceID: 1, ReportedPath: "/media", Path: "/media"}},
		}, nil
	}

	body := decodeInto[gen.Error](t, h.do(t, "POST", "/api/arr/1/import-roots", nil), 409)

	require.Equal(t, "missing_path_mapping", body.Error)
	require.Empty(t, h.store.CreateRootCalls())
}

// The standing error of plan.md 18.4, riding on the roots listing so the settings page
// has it on every poll rather than only after an import.
func TestListRoots_ReportsARootTwoEnabledInstancesBothClaim(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	radarr := int64(1)
	sonarr := int64(2)

	h.store.ListArrInstancesFunc = func(context.Context) ([]domain.ArrInstance, error) {
		return []domain.ArrInstance{
			{ID: radarr, Name: "radarr-4k", Flavour: domain.FlavourRadarr, Enabled: true},
			{ID: sonarr, Name: "sonarr-hd", Flavour: domain.FlavourSonarr, Enabled: true},
		}, nil
	}
	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return []domain.Root{
			{ID: 3, Path: "/media/shared", ArrInstanceID: &radarr, Enabled: true},
			{ID: 4, Path: "/media/shared", ArrInstanceID: &sonarr, Enabled: true},
			{ID: 5, Path: "/media/movies", ArrInstanceID: &radarr, Enabled: true},
		}, nil
	}

	list := decodeInto[gen.RootList](t, h.do(t, "GET", "/api/roots", nil), 200)
	require.Len(t, list.Roots, 3)
	require.Equal(t, []gen.ContestedRoot{{
		Path: "/media/shared",
		Instances: []gen.ArrInstanceRef{
			{Id: radarr, Name: "radarr-4k"},
			{Id: sonarr, Name: "sonarr-hd"},
		},
	}}, list.Conflicts)
}

// A disabled instance is not a conflict: plan.md 16.2 attributes on enabled
// roots only, so a root the operator has parked claims nothing.
func TestListRoots_IgnoresARootWhoseSecondClaimIsDisabled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	radarr := int64(1)
	sonarr := int64(2)

	h.store.ListArrInstancesFunc = func(context.Context) ([]domain.ArrInstance, error) {
		return []domain.ArrInstance{
			{ID: radarr, Name: "radarr-4k", Flavour: domain.FlavourRadarr, Enabled: true},
			{ID: sonarr, Name: "sonarr-hd", Flavour: domain.FlavourSonarr, Enabled: false},
		}, nil
	}
	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return []domain.Root{
			{ID: 3, Path: "/media/shared", ArrInstanceID: &radarr, Enabled: true},
			{ID: 4, Path: "/media/shared", ArrInstanceID: &sonarr, Enabled: false},
		}, nil
	}

	list := decodeInto[gen.RootList](t, h.do(t, "GET", "/api/roots", nil), 200)
	require.Empty(t, list.Conflicts)
}
