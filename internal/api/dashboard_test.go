package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// plan.md 18.1 and 18.6: one call returns the current job, the queue, the
// awaiting-stream-end list, recent completions, failures, stats and the
// compatibility summary, because the UI polls it every ten seconds.

func dashboardStore(h *harness) {
	started := testNow.Add(-90 * time.Second)

	jobs := map[domain.JobState][]domain.Job{
		domain.JobQueued: {{ID: 2, MediaFileID: 20, Kind: domain.KindRemux, State: domain.JobQueued, Priority: 90}},
		domain.JobRunning: {{
			ID: 1, MediaFileID: 10, Kind: domain.KindFull, State: domain.JobRunning,
			StartedAt: &started, ProgressPct: 42.5, ProgressSpeed: 3.1,
			EncoderUsed: domain.EncoderQSV, DecodePath: domain.DecodeHardware,
		}},
		domain.JobAwaitingStreamEnd: {{
			ID: 3, MediaFileID: 30, Kind: domain.KindFull, State: domain.JobAwaitingStreamEnd,
			BlockedBy: "alice is watching Dune on Apple TV", StartedAt: &started,
		}},
		domain.JobDone:   {{ID: 4, MediaFileID: 40, Kind: domain.KindAudioOnly, State: domain.JobDone}},
		domain.JobFailed: {{ID: 5, MediaFileID: 50, Kind: domain.KindFull, State: domain.JobFailed, FailureCode: domain.FailFfmpeg, FailureMessage: "encoder died"}},
	}

	h.store.ListJobsFunc = func(_ context.Context, f store.JobFilter) ([]domain.Job, int, error) {
		var out []domain.Job
		for _, state := range f.State {
			out = append(out, jobs[state]...)
		}

		return out, len(out), nil
	}

	h.store.GetMediaFileFunc = func(_ context.Context, id int64) (domain.MediaFile, error) {
		return domain.MediaFile{ID: id, Path: "/media/movies/file.mkv"}, nil
	}

	h.store.CountJobsByStateFunc = func(context.Context) (map[domain.JobState]int, error) {
		return map[domain.JobState]int{domain.JobQueued: 1, domain.JobAwaitingStreamEnd: 1, domain.JobDone: 1}, nil
	}

	h.store.CountMediaByStatusFunc = func(context.Context) (map[domain.MediaStatus]int, error) {
		return map[domain.MediaStatus]int{domain.MediaDone: 900, domain.MediaAnalyzed: 90, domain.MediaNew: 10}, nil
	}

	h.store.CountMediaByPlanKindFunc = func(context.Context) (map[domain.Kind]int, error) {
		return map[domain.Kind]int{domain.KindSkip: 900, domain.KindFull: 60, domain.KindRemux: 30}, nil
	}

	h.store.StatsFunc = func(context.Context) (store.Stats, error) {
		return store.Stats{FilesDone: 900, BytesIn: 1000, BytesOut: 600, BytesSaved: 400, EncodeSeconds: 7200}, nil
	}

	h.store.ListMediaFilesFunc = func(context.Context, store.MediaFilter) ([]domain.MediaFile, int, error) {
		return []domain.MediaFile{
			{ID: 1, Plan: &domain.Plan{
				SourceContainer: "matroska", OutputContainer: domain.ContainerMatroska,
				Streams: []domain.StreamPlan{
					{Type: domain.StreamVideo, Decision: domain.DecisionEncode},
					{Type: domain.StreamAudio, Decision: domain.DecisionEncode},
					{Type: domain.StreamSubtitle, Decision: domain.DecisionDrop},
				},
			}},
			{ID: 2, Plan: &domain.Plan{
				SourceContainer: "avi", OutputContainer: domain.ContainerMatroska,
				Streams: []domain.StreamPlan{{Type: domain.StreamAudio, Decision: domain.DecisionEncode}},
			}},
		}, 2, nil
	}

	h.queue.PausedFunc = func(context.Context) (bool, error) { return false, nil }
}

func TestGetDashboard_ReturnsEverythingThePollNeeds(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	dashboardStore(h)

	got := decodeInto[gen.Dashboard](t, h.do(t, "GET", "/api/dashboard", nil), 200)

	require.NotNil(t, got.CurrentJob)
	require.Equal(t, int64(1), got.CurrentJob.Id)
	require.Equal(t, gen.JobStateRunning, got.CurrentJob.State)
	require.NotNil(t, got.CurrentJob.ProgressPct)
	require.InDelta(t, 42.5, *got.CurrentJob.ProgressPct, 0.001)

	require.Len(t, got.Queue, 1)
	require.Equal(t, int64(2), got.Queue[0].Id)
	require.Equal(t, 1, got.QueueDepth)
	require.False(t, got.QueuePaused)

	require.Len(t, got.AwaitingStreamEnd, 1)
	require.Equal(t, int64(3), got.AwaitingStreamEnd[0].JobId)
	require.Equal(t, 90, got.AwaitingStreamEnd[0].WaitingSeconds)
	require.NotNil(t, got.AwaitingStreamEnd[0].SessionUser)

	require.Len(t, got.RecentCompletions, 1)
	require.Len(t, got.Failures, 1)
	require.NotNil(t, got.Failures[0].FailureCode)
	require.Equal(t, gen.FailureCodeFfmpegFailed, *got.Failures[0].FailureCode)

	require.Equal(t, int64(400), got.Stats.BytesSaved)
	require.Equal(t, 1000, got.Stats.FilesTotal)
	require.Equal(t, 900, got.Stats.FilesDone)

	require.Equal(t, 990, got.Compatibility.FilesAnalyzed)
	require.Equal(t, 900, got.Compatibility.FilesCompatible)
	require.Equal(t, 90, got.Compatibility.FilesNeedingWork)
	require.Equal(t, 10, got.Compatibility.FilesUnanalyzed)
	require.Equal(t, gen.PlanKindBreakdown{Full: 60, Remux: 30, Skip: 900}, got.Compatibility.ByPlanKind)
	require.Equal(t, gen.CompatibilityReasons{Audio: 2, Container: 1, Subtitles: 1, Video: 1}, got.Compatibility.ByReason)
	require.Equal(t, testNow, got.GeneratedAt.UTC())
}

// The compatibility breakdown is the one part that reads every non-skip plan, so
// it is memoised: a second poll inside the TTL must not walk the library again.
func TestGetDashboard_CompatibilityBreakdownIsMemoised(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	dashboardStore(h)

	require.Equal(t, 200, h.do(t, "GET", "/api/dashboard", nil).Code)
	after := len(h.store.ListMediaFilesCalls())

	require.Equal(t, 200, h.do(t, "GET", "/api/dashboard", nil).Code)
	require.Len(t, h.store.ListMediaFilesCalls(), after)
}

func TestGetQueue_ReportsTheWorkersView(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	dashboardStore(h)

	got := decodeInto[gen.QueueState](t, h.do(t, "GET", "/api/queue", nil), 200)

	require.NotNil(t, got.Running)
	require.Equal(t, int64(1), got.Running.Id)
	require.Equal(t, 1, got.Depth)
	require.Len(t, got.Queued, 1)
	require.Len(t, got.AwaitingStreamEnd, 1)
}

func TestPauseAndResumeQueue_GoThroughTheWorkerNotTheSettingsRow(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	dashboardStore(h)

	paused := false
	h.queue.PausedFunc = func(context.Context) (bool, error) { return paused, nil }
	h.queue.PauseFunc = func(context.Context) error { paused = true; return nil }
	h.queue.ResumeFunc = func(context.Context) error { paused = false; return nil }

	require.True(t, decodeInto[gen.QueueState](t, h.do(t, "POST", "/api/queue/pause", nil), 200).Paused)
	require.False(t, decodeInto[gen.QueueState](t, h.do(t, "POST", "/api/queue/resume", nil), 200).Paused)
	require.Len(t, h.queue.PauseCalls(), 1)
	require.Len(t, h.queue.ResumeCalls(), 1)
	require.Empty(t, h.store.UpdateSettingsCalls())
}
