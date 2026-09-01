package ingest_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	probemock "github.com/yama6a/codarr/internal/ffprobe/mock"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/ingest/mock"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/pkg/store"
)

const (
	moviesRoot = "/media/yama/movies"
	filePath   = "/media/yama/movies/Heat (1995)/Heat (1995).mkv"
	mp4Path    = "/media/yama/movies/Heat (1995)/Heat (1995).mp4"
)

var now = time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func fixture(t *testing.T, name string) *ffprobe.Result {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	res, err := ffprobe.Parse(raw)
	require.NoError(t, err)

	return res
}

func instanceID(id int64) *int64 { return &id }

func roots() []domain.Root {
	return []domain.Root{
		{ID: 1, Path: moviesRoot, ArrInstanceID: instanceID(10), Enabled: true},
		{ID: 2, Path: "/media/yama/tv", ArrInstanceID: instanceID(11), Enabled: true},
	}
}

func settings() domain.Settings {
	return domain.Settings{
		ScanEnabled:         true,
		ScanCron:            ingest.DefaultCron,
		ScanRateLimitFPS:    50,
		PrioritiseQuickJobs: true,
	}
}

func env() ingest.Env {
	return ingest.Env{Roots: roots(), Settings: settings(), Origin: domain.OriginIngest}
}

func statOf(size int64, mtime time.Time) fsx.FileInfo {
	return fsx.FileInfo{Size: size, MTime: mtime, NLink: 1}
}

// analysisFS answers a fixed stat for everything and a directory for anything
// with no extension.
func analysisFS(info fsx.FileInfo) *mock.FSMock {
	return &mock.FSMock{
		StatFunc: func(string) (fsx.FileInfo, error) { return info, nil },
	}
}

type storeState struct {
	existing domain.MediaFile
	upserted domain.MediaFile
	analysis store.AnalysisUpdate
	enqueued domain.Job
	statuses []domain.MediaStatus
}

func analysisStore(t *testing.T, state *storeState) *mock.AnalysisStoreMock {
	t.Helper()

	return &mock.AnalysisStoreMock{
		ListRootsFunc:   func(context.Context) ([]domain.Root, error) { return roots(), nil },
		GetSettingsFunc: func(context.Context) (domain.Settings, error) { return settings(), nil },
		GetMediaFileByPathFunc: func(_ context.Context, _ string) (domain.MediaFile, error) {
			if state.existing.ID == 0 {
				return domain.MediaFile{}, store.ErrNotFound
			}

			return state.existing, nil
		},
		UpsertMediaFileFunc: func(_ context.Context, m domain.MediaFile) (domain.MediaFile, error) {
			state.upserted = m
			out := m
			out.ID = 7
			out.CodarrOutputFingerprint = state.existing.CodarrOutputFingerprint
			out.Provenance = domain.DeriveProvenance(out.CodarrOutputFingerprint, m.Fingerprint)

			return out, nil
		},
		UpdateMediaAnalysisFunc: func(_ context.Context, u store.AnalysisUpdate) error {
			state.analysis = u

			return nil
		},
		SetMediaStatusFunc: func(_ context.Context, _ int64, s domain.MediaStatus, _ string) error {
			state.statuses = append(state.statuses, s)

			return nil
		},
		EnqueueJobFunc: func(_ context.Context, j domain.Job) (domain.Job, bool, error) {
			state.enqueued = j
			j.ID = 99

			return j, true, nil
		},
	}
}

func newAnalyzer(t *testing.T, fs ingest.FS, probe *ffprobe.Result, probeErr error,
	fp string, st ingest.AnalysisStore,
) *ingest.Analyzer {
	t.Helper()

	return ingest.NewAnalyzer(
		fs,
		&mock.FingerprinterMock{SparseFunc: func(string) (string, error) { return fp, nil }},
		&probemock.ProberMock{
			ProbeFunc: func(context.Context, string) (*ffprobe.Result, error) {
				return probe, probeErr
			},
		},
		st,
		clock.NewFake(now),
		discardLogger(),
	)
}

func TestAnalyzer_AnalyzeInPlansAndEnqueues(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)
	probe := fixture(t, "audio_only_mkv.json")

	res, err := newAnalyzer(t, analysisFS(statOf(9871234567, now.Add(-time.Hour))),
		probe, nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.NoError(t, err)

	require.Equal(t, ingest.Result{
		Path:        filePath,
		MediaFileID: 7,
		SkipReason:  "no CODARR tag",
		Provenance:  domain.ProvenanceUntouched,
		PlanKind:    domain.KindAudioOnly,
		JobID:       99,
		Queued:      true,
	}, res)

	require.Equal(t, domain.MediaFile{
		Path:            filePath,
		RootID:          instanceID(1),
		ArrInstanceID:   instanceID(10),
		SizeBytes:       9871234567,
		MTime:           now.Add(-time.Hour).Unix(),
		NLink:           1,
		Fingerprint:     "xxh3-128:aaa",
		FingerprintAlgo: "xxh3-128",
		Status:          domain.MediaNew,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, state.upserted)

	require.Equal(t, domain.MediaAnalyzed, state.analysis.Status)
	require.Equal(t, domain.KindAudioOnly, state.analysis.PlanKind)
	require.Equal(t, "h264", state.analysis.VideoCodec)
	require.Equal(t, "matroska", state.analysis.Container)
	require.False(t, state.analysis.CodarrTagged)
	require.Equal(t, string(probe.Raw), state.analysis.ProbeJSON)
	require.Equal(t, now, state.analysis.AnalyzedAt)

	require.Equal(t, domain.Job{
		MediaFileID: 7,
		Kind:        domain.KindAudioOnly,
		Origin:      domain.OriginIngest,
		Priority:    domain.PriorityQuick,
		Transform:   decide.NewTransform(probe, *state.analysis.Plan, 0),
		QueuedAt:    now,
	}, state.enqueued)
}

// plan.md 12: tag plus policy plus fingerprint. All three hold here, so the
// file is Codarr's own untouched output and no job is queued.
func TestAnalyzer_AnalyzeInSkipsItsOwnUnmodifiedOutput(t *testing.T) {
	t.Parallel()

	state := &storeState{existing: domain.MediaFile{
		ID:                      7,
		CodarrOutputFingerprint: "xxh3-128:same",
		CreatedAt:               now.Add(-24 * time.Hour),
	}}
	st := analysisStore(t, state)

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "tagged_mp4.json"), nil, "xxh3-128:same", st).
		AnalyzeIn(t.Context(), mp4Path, env())
	require.NoError(t, err)

	require.True(t, res.Skipped)
	require.Equal(t, "tagged, on the current policy, and unchanged since Codarr wrote it",
		res.SkipReason)
	require.Equal(t, domain.ProvenanceCodarrOutput, res.Provenance)
	require.Equal(t, domain.MediaDone, state.analysis.Status)
	require.True(t, state.analysis.CodarrTagged)
	require.Equal(t, decide.PolicyHash(), state.analysis.CodarrPolicyHash)
	require.Empty(t, st.EnqueueJobCalls())
	require.Equal(t, now.Add(-24*time.Hour), state.upserted.CreatedAt,
		"the original created_at survives a re-analysis")
}

// The realistic third-party rewrite: same tags, different bytes. plan.md 12
// wants this visible, not silently reprocessed and not silently skipped.
func TestAnalyzer_AnalyzeInReanalysesAModifiedOutput(t *testing.T) {
	t.Parallel()

	state := &storeState{existing: domain.MediaFile{
		ID:                      7,
		CodarrOutputFingerprint: "xxh3-128:written",
	}}
	st := analysisStore(t, state)

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "tagged_mp4.json"), nil, "xxh3-128:rewritten", st).
		AnalyzeIn(t.Context(), mp4Path, env())
	require.NoError(t, err)

	require.False(t, res.Skipped)
	require.Equal(t, domain.ProvenanceModified, res.Provenance)
	require.Equal(t,
		"tagged and on the current policy, but the file no longer matches the fingerprint Codarr recorded",
		res.SkipReason)
	require.True(t, state.analysis.CodarrTagged)
	require.Equal(t, domain.MediaSkipped, state.analysis.Status,
		"the plan for that fixture is skip, so nothing is queued")
	require.Empty(t, st.EnqueueJobCalls())
}

func TestAnalyzer_AnalyzeInStopsAtTheIgnoreListBeforeFingerprinting(t *testing.T) {
	t.Parallel()

	state := &storeState{existing: domain.MediaFile{ID: 7, Ignored: true}}
	st := analysisStore(t, state)

	fp := &mock.FingerprinterMock{SparseFunc: func(string) (string, error) { return "", nil }}
	prober := &probemock.ProberMock{}

	res, err := ingest.NewAnalyzer(analysisFS(statOf(big, now.Add(-time.Hour))), fp, prober,
		st, clock.NewFake(now), discardLogger()).
		AnalyzeIn(t.Context(), filePath, env())
	require.NoError(t, err)

	require.Equal(t, ingest.Result{
		Path:        filePath,
		MediaFileID: 7,
		Excluded:    ingest.ExcludedIgnored,
	}, res)
	require.Empty(t, fp.SparseCalls())
	require.Empty(t, st.UpsertMediaFileCalls())
}

func TestAnalyzer_AnalyzeInStopsAtAHardCodedExclusion(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))), nil, nil, "", st).
		AnalyzeIn(t.Context(), "/media/yama/movies/Heat/Heat-trailer.mkv", env())
	require.NoError(t, err)

	require.Equal(t, ingest.ExcludedTrailer, res.Excluded)
	require.Empty(t, st.GetMediaFileByPathCalls())
}

func TestAnalyzer_AnalyzeInRefusesAPathOutsideEveryRoot(t *testing.T) {
	t.Parallel()

	state := &storeState{}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))), nil, nil, "",
		analysisStore(t, state)).
		AnalyzeIn(t.Context(), "/mnt/elsewhere/Heat.mkv", env())
	require.ErrorIs(t, err, ingest.ErrOutsideRoots)
}

func TestAnalyzer_AnalyzeInRefusesADirectory(t *testing.T) {
	t.Parallel()

	fs := &mock.FSMock{
		StatFunc: func(string) (fsx.FileInfo, error) {
			return fsx.FileInfo{IsDir: true}, nil
		},
	}

	_, err := newAnalyzer(t, fs, nil, nil, "", analysisStore(t, &storeState{})).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorIs(t, err, ingest.ErrNotAFile)
}

func TestAnalyzer_AnalyzeInRecordsAProbeFailureOnTheRow(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))), nil,
		errors.New("moov atom not found"), "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "moov atom not found")

	require.Equal(t, []domain.MediaStatus{domain.MediaFailed}, state.statuses)
}

// plan.md 16.2: two enabled instances claiming the same root is a configuration
// error. The file is still processed, with no instance attributed.
func TestAnalyzer_AnalyzeInReportsARootCollisionAndAttributesNoInstance(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)

	colliding := ingest.Env{
		Roots: []domain.Root{
			{ID: 1, Path: moviesRoot, ArrInstanceID: instanceID(10), Enabled: true},
			{ID: 2, Path: moviesRoot, ArrInstanceID: instanceID(11), Enabled: true},
		},
		Settings: settings(),
		Origin:   domain.OriginIngest,
	}

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, colliding)
	require.NoError(t, err)

	require.NotNil(t, res.Conflict)
	require.Equal(t, []int64{10, 11}, res.Conflict.InstanceIDs)
	require.Nil(t, state.upserted.ArrInstanceID)
}

func TestAnalyzer_AnalyzeInRecordsTheWebhooksEntityID(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)

	e := env()
	e.ArrEntityID = instanceID(4242)

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, e)
	require.NoError(t, err)

	require.Equal(t, instanceID(4242), state.upserted.ArrEntityID)
}

func TestAnalyzer_AnalyzeLoadsItsOwnEnv(t *testing.T) {
	t.Parallel()

	state := &storeState{}
	st := analysisStore(t, state)

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		Analyze(t.Context(), filePath, domain.OriginManual)
	require.NoError(t, err)

	require.True(t, res.Queued)
	require.Equal(t, domain.OriginManual, state.enqueued.Origin)
}

func TestAnalyzer_AnalyzeSurfacesAnEnvFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return nil, errors.New("database is locked")
	}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now)), nil, nil, "", st).
		Analyze(t.Context(), filePath, domain.OriginIngest)
	require.ErrorContains(t, err, "list roots: database is locked")
}

func TestPriorityFor_QuickWinsClearAheadOfEncodes(t *testing.T) {
	t.Parallel()

	require.Equal(t, domain.PriorityFull, ingest.PriorityFor(domain.KindFull, true))
	require.Equal(t, domain.PriorityQuick, ingest.PriorityFor(domain.KindRemux, true))
	require.Equal(t, domain.PriorityQuick, ingest.PriorityFor(domain.KindAudioOnly, true))
	require.Equal(t, domain.PriorityNormal, ingest.PriorityFor(domain.KindAudioOnly, false))
	require.Equal(t, domain.PriorityNormal, ingest.PriorityFor(domain.KindSkip, true))
}

func TestAnalyzer_AnalyzeInSurfacesAFingerprintFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	fp := &mock.FingerprinterMock{
		SparseFunc: func(string) (string, error) { return "", errors.New("stale NFS file handle") },
	}

	_, err := ingest.NewAnalyzer(analysisFS(statOf(big, now.Add(-time.Hour))), fp,
		&probemock.ProberMock{}, st, clock.NewFake(now), discardLogger()).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "stale NFS file handle")
	require.Empty(t, st.UpsertMediaFileCalls())
}

func TestAnalyzer_AnalyzeInSurfacesAnUpsertFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.UpsertMediaFileFunc = func(context.Context, domain.MediaFile) (domain.MediaFile, error) {
		return domain.MediaFile{}, errors.New("database is locked")
	}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))), nil, nil,
		"xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "upsert media row")
}

func TestAnalyzer_AnalyzeInSurfacesARowLookupFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.GetMediaFileByPathFunc = func(context.Context, string) (domain.MediaFile, error) {
		return domain.MediaFile{}, errors.New("database is locked")
	}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))), nil, nil, "", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "load media row")
}

func TestAnalyzer_AnalyzeInSurfacesAnAnalysisWriteFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.UpdateMediaAnalysisFunc = func(context.Context, store.AnalysisUpdate) error {
		return errors.New("database is locked")
	}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "record analysis")
}

func TestAnalyzer_AnalyzeInSurfacesAnEnqueueFailure(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.EnqueueJobFunc = func(context.Context, domain.Job) (domain.Job, bool, error) {
		return domain.Job{}, false, errors.New("database is locked")
	}

	_, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "enqueue")
}

// plan.md 17.1: enqueue is idempotent, so a webhook racing the daily scan
// produces one job and the loser is a silent no-op.
func TestAnalyzer_AnalyzeInReportsAnIdempotentEnqueueAsNotQueued(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})
	st.EnqueueJobFunc = func(context.Context, domain.Job) (domain.Job, bool, error) {
		return domain.Job{}, false, nil
	}

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "audio_only_mkv.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), filePath, env())
	require.NoError(t, err)

	require.False(t, res.Queued)
	require.Zero(t, res.JobID)
}

func TestAnalyzer_AnalyzeInSurfacesAStatFailure(t *testing.T) {
	t.Parallel()

	fs := &mock.FSMock{
		StatFunc: func(string) (fsx.FileInfo, error) {
			return fsx.FileInfo{}, errors.New("stale NFS file handle")
		},
	}

	_, err := newAnalyzer(t, fs, nil, nil, "", analysisStore(t, &storeState{})).
		AnalyzeIn(t.Context(), filePath, env())
	require.ErrorContains(t, err, "stat "+filePath)
}

// A legacy codec with no field_order cannot be settled from the probe alone
// (6.2). The engine says so rather than guessing, and the caller has to run the
// idet sample.
func TestAnalyzer_AnalyzeInReportsWhenAnIdetSampleIsStillOwed(t *testing.T) {
	t.Parallel()

	st := analysisStore(t, &storeState{})

	res, err := newAnalyzer(t, analysisFS(statOf(big, now.Add(-time.Hour))),
		fixture(t, "mpeg2_interlaced.json"), nil, "xxh3-128:aaa", st).
		AnalyzeIn(t.Context(), "/media/yama/movies/Old/Old.mpg", env())
	require.NoError(t, err)

	require.Equal(t, domain.KindFull, res.PlanKind)
}
