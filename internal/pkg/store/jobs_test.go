package store_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

// The partial unique index of 001_schema.sql: a webhook racing a manual trigger gives
// one job, and the loser is a no-op rather than an error.
func TestJobStore_EnqueueIsIdempotent(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/idempotent.mkv")

	first := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	second, created, err := s.EnqueueJob(t.Context(), domain.Job{
		MediaFileID: media.ID,
		Kind:        domain.KindFull,
		Origin:      domain.OriginManual,
		Priority:    domain.PriorityNormal,
		QueuedAt:    testTime(),
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, domain.Job{}, second)

	jobs, total, err := s.ListJobs(t.Context(), store.JobFilter{MediaFileID: &media.ID})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, jobs, 1)
	require.Equal(t, first.ID, jobs[0].ID)
	require.Equal(t, domain.OriginIngest, jobs[0].Origin)
}

// The other half of the index: only the active states block, so a re-encode after a
// failure is a new job rather than a silent no-op.
func TestJobStore_EnqueueAfterTerminalStateCreatesANewJob(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/retry.mkv")

	first := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)
	require.NoError(t, s.CancelJob(t.Context(), first.ID))

	second, created, err := s.EnqueueJob(t.Context(), domain.Job{
		MediaFileID: media.ID,
		Kind:        domain.KindFull,
		Origin:      domain.OriginManual,
		Priority:    domain.PriorityNormal,
		QueuedAt:    testTime(),
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, first.ID, second.ID)
}

func TestJobStore_EnqueueMarksMediaQueued(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/queued.mkv")

	seedJob(t, s, media.ID, domain.KindRemux, domain.PriorityQuick)

	reloaded, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaQueued, reloaded.Status)
}

func TestJobStore_ClaimNextJobTakesLowestPriorityThenOldest(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	full := seedJob(t, s, seedMedia(t, s, "/library/a.mkv").ID, domain.KindFull, domain.PriorityFull)
	quick := seedJob(t, s, seedMedia(t, s, "/library/b.mkv").ID, domain.KindRemux, domain.PriorityQuick)

	first, ok, err := s.ClaimNextJob(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, quick.ID, first.ID)
	require.Equal(t, domain.JobRunning, first.State)
	require.NotNil(t, first.StartedAt)

	second, ok, err := s.ClaimNextJob(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, full.ID, second.ID)

	_, ok, err = s.ClaimNextJob(t.Context())
	require.NoError(t, err)
	require.False(t, ok)
}

func TestJobStore_ClaimNextJobMarksMediaProcessing(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/processing.mkv")
	seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	_, ok, err := s.ClaimNextJob(t.Context())
	require.NoError(t, err)
	require.True(t, ok)

	reloaded, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaProcessing, reloaded.Status)
}

// The queue's safety property: the select and the transition share one transaction, so
// two callers racing on one queued row cannot both come away with it.
func TestJobStore_ConcurrentClaimsNeverReturnTheSameJob(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	const jobs = 12

	for i := range jobs {
		media := seedMedia(t, s, "/library/movies/claim-"+string(rune('a'+i))+".mkv")
		seedJob(t, s, media.ID, domain.KindFull, domain.PriorityNormal)
	}

	const claimers = 8

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed []int64
	)

	for range claimers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				job, ok, err := s.ClaimNextJob(t.Context())
				if err != nil {
					mu.Lock()
					claimed = append(claimed, -1)
					mu.Unlock()

					return
				}

				if !ok {
					return
				}

				mu.Lock()
				claimed = append(claimed, job.ID)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	require.Len(t, claimed, jobs)

	seen := map[int64]bool{}
	for _, id := range claimed {
		require.Positive(t, id)
		require.False(t, seen[id], "job %d was claimed twice", id)
		seen[id] = true
	}
}

// TestJobStore_SweepRequeuesInterruptedJobsAtTheFront covers plan.md 19.2: one
// worker process means anything still running at startup was interrupted.
func TestJobStore_SweepRequeuesInterruptedJobsAtTheFront(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	interrupted := seedJob(t, s, seedMedia(t, s, "/library/interrupted.mkv").ID,
		domain.KindFull, domain.PriorityFull)
	queued := seedJob(t, s, seedMedia(t, s, "/library/waiting.mkv").ID,
		domain.KindRemux, domain.PriorityQuick)

	forceJobState(t, db, interrupted.ID, domain.JobRunning, 0, "/library/.codarr-staging-1.mkv")

	results, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []store.SweepResult{{
		JobID:       interrupted.ID,
		MediaFileID: interrupted.MediaFileID,
		FoundState:  domain.JobRunning,
		Action:      store.SweepRequeued,
		Attempt:     1,
		StagingPath: "/library/.codarr-staging-1.mkv",
	}}, results)

	reloaded, err := s.GetJob(t.Context(), interrupted.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, reloaded.State)
	require.Equal(t, 1, reloaded.Attempt)
	require.Equal(t, domain.PriorityQuick-1, reloaded.Priority)
	require.Empty(t, reloaded.StagingPath)
	require.Nil(t, reloaded.StartedAt)

	next, ok, err := s.ClaimNextJob(t.Context())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, interrupted.ID, next.ID, "the requeued job runs ahead of job %d", queued.ID)
}

func TestJobStore_SweepRequeuesVerifyingJobs(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	job := seedJob(t, s, seedMedia(t, s, "/library/verifying.mkv").ID, domain.KindAudioOnly, domain.PriorityNormal)
	forceJobState(t, db, job.ID, domain.JobVerifying, 1, "")

	results, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, store.SweepRequeued, results[0].Action)
	require.Equal(t, 2, results[0].Attempt)
}

// TestJobStore_SweepFailsAtTheAttemptCap is the loop guard of plan.md 19.2:
// without it, a file that kills the process every time burns the array forever.
func TestJobStore_SweepFailsAtTheAttemptCap(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	media := seedMedia(t, s, "/library/cursed.mkv")
	job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	forceJobState(t, db, job.ID, domain.JobRunning, domain.MaxAutoAttempts, "")

	results, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, store.SweepFailed, results[0].Action)

	reloaded, err := s.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, reloaded.State)
	require.Equal(t, domain.FailInterrupted, reloaded.FailureCode)
	require.Contains(t, reloaded.FailureMessage, "interrupted 4 times")
	require.NotNil(t, reloaded.FinishedAt)

	failedMedia, err := s.GetMediaFile(t.Context(), media.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaFailed, failedMedia.Status)
}

// TestJobStore_SweepStopsShortOfTheCap proves attempt 2 still gets one more go,
// so the cap is three automatic restarts rather than two.
func TestJobStore_SweepStopsShortOfTheCap(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	job := seedJob(t, s, seedMedia(t, s, "/library/nearly.mkv").ID, domain.KindFull, domain.PriorityFull)
	forceJobState(t, db, job.ID, domain.JobRunning, domain.MaxAutoAttempts-1, "")

	results, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)
	require.Equal(t, store.SweepRequeued, results[0].Action)
	require.Equal(t, domain.MaxAutoAttempts, results[0].Attempt)
}

// promoting and awaiting_stream_end need the destination and staging files to decide,
// which the store has no business touching (plan.md 19.2).
func TestJobStore_SweepLeavesPromotingForTheCaller(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	promoting := seedJob(t, s, seedMedia(t, s, "/library/promoting.mkv").ID,
		domain.KindFull, domain.PriorityFull)
	awaiting := seedJob(t, s, seedMedia(t, s, "/library/awaiting.mkv").ID,
		domain.KindRemux, domain.PriorityQuick)

	forceJobState(t, db, promoting.ID, domain.JobPromoting, 0, "/library/.codarr-staging-1.mkv")
	forceJobState(t, db, awaiting.ID, domain.JobAwaitingStreamEnd, 0, "/library/.codarr-staging-2.mkv")

	results, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)
	require.Equal(t, []store.SweepResult{
		{
			JobID:       promoting.ID,
			MediaFileID: promoting.MediaFileID,
			FoundState:  domain.JobPromoting,
			Action:      store.SweepNeedsCheck,
			Attempt:     0,
			StagingPath: "/library/.codarr-staging-1.mkv",
		},
		{
			JobID:       awaiting.ID,
			MediaFileID: awaiting.MediaFileID,
			FoundState:  domain.JobAwaitingStreamEnd,
			Action:      store.SweepNeedsCheck,
			Attempt:     0,
			StagingPath: "/library/.codarr-staging-2.mkv",
		},
	}, results)

	still, err := s.GetJob(t.Context(), promoting.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobPromoting, still.State)

	requeued, err := s.RequeueInterruptedJob(t.Context(), promoting.ID)
	require.NoError(t, err)
	require.Equal(t, store.SweepRequeued, requeued.Action)
	require.Equal(t, 1, requeued.Attempt)
}

func TestJobStore_FailJobRequiresACodeAndAMessage(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	job := seedJob(t, s, seedMedia(t, s, "/library/bare.mkv").ID, domain.KindFull, domain.PriorityFull)

	require.ErrorIs(t, s.FailJob(t.Context(), job.ID, domain.FailFfmpeg, "", ""), store.ErrInvalidFailure)
	require.ErrorIs(t, s.FailJob(t.Context(), job.ID, "", "something broke", ""), store.ErrInvalidFailure)

	require.NoError(t, s.FailJob(t.Context(), job.ID, domain.FailFfmpeg,
		"ffmpeg exited 1", "x265 [error]: ..."))

	reloaded, err := s.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobFailed, reloaded.State)
	require.Equal(t, domain.FailFfmpeg, reloaded.FailureCode)
	require.Equal(t, "ffmpeg exited 1", reloaded.FailureMessage)
	require.Equal(t, "x265 [error]: ...", reloaded.StderrTail)
}

// TestJobStore_RestartResetsTheAttemptCounter: plan.md 19.2 makes a manual
// retry a fresh start, not a continuation of the interruption streak.
func TestJobStore_RestartResetsTheAttemptCounter(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	job := seedJob(t, s, seedMedia(t, s, "/library/restart.mkv").ID, domain.KindFull, domain.PriorityFull)
	forceJobState(t, db, job.ID, domain.JobRunning, domain.MaxAutoAttempts, "")

	_, err := s.SweepInterruptedJobs(t.Context())
	require.NoError(t, err)

	restarted, err := s.RestartJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, restarted.State)
	require.Equal(t, 0, restarted.Attempt)
	require.Empty(t, restarted.FailureCode)
	require.Empty(t, restarted.FailureMessage)
	require.Nil(t, restarted.FinishedAt)
}

func TestJobStore_ActiveJobForMedia(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/active.mkv")

	_, ok, err := s.ActiveJobForMedia(t.Context(), media.ID)
	require.NoError(t, err)
	require.False(t, ok)

	job := seedJob(t, s, media.ID, domain.KindFull, domain.PriorityFull)

	active, ok, err := s.ActiveJobForMedia(t.Context(), media.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, job.ID, active.ID)

	require.NoError(t, s.CancelJob(t.Context(), job.ID))

	_, ok, err = s.ActiveJobForMedia(t.Context(), media.ID)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestJobStore_TransformRecordRoundTrips(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	job := seedJob(t, s, seedMedia(t, s, "/library/transform.mkv").ID, domain.KindAudioOnly, domain.PriorityNormal)

	outputIndex := 0
	record := domain.TransformRecord{
		Container: domain.BeforeAfterString{Before: "matroska", After: "matroska"},
		Video: domain.VideoTransform{
			Action: domain.DecisionCopy,
			Reason: "h264 High L4.0 8-bit 4:2:0 progressive",
			Before: &domain.VideoState{Codec: "h264", Profile: "High", Level: "4.0", Width: 1920, Height: 1080},
			After:  &domain.VideoState{Codec: "h264", Profile: "High", Level: "4.0", Width: 1920, Height: 1080},
		},
		Audio: []domain.AudioTransform{{
			SourceIndex: 0,
			OutputIndex: &outputIndex,
			Language:    "eng",
			Action:      domain.DecisionEncode,
			Reason:      "dts not in copy list for 3+ channels",
			Before:      &domain.AudioState{Codec: "dts", Channels: 6, Layout: "5.1"},
			After:       &domain.AudioState{Codec: "ac3", Channels: 6, Layout: "5.1"},
		}},
		Size:     domain.SizeTransform{BeforeBytes: 9_871_234_567},
		Duration: domain.DurationTransform{Estimated: 240},
	}

	require.NoError(t, s.UpdateJobTransform(t.Context(), job.ID, record))

	reloaded, err := s.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, record, reloaded.Transform)
}

func TestJobStore_ExecutionAndProgressUpdates(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	job := seedJob(t, s, seedMedia(t, s, "/library/exec.mkv").ID, domain.KindFull, domain.PriorityFull)

	require.NoError(t, s.UpdateJobExecution(t.Context(), store.ExecutionUpdate{
		JobID:            job.ID,
		StagingPath:      "/library/.codarr-staging-1.mkv",
		UsedTempDir:      false,
		FfmpegArgv:       []string{"ffmpeg", "-i", "in.mkv", "out.mkv"},
		EncoderUsed:      domain.EncoderQSV,
		DecodePath:       domain.DecodeHardware,
		FellBack:         true,
		FallbackReason:   "qsv init failed",
		SourceSize:       9_871_234_567,
		EstimatedSeconds: 240,
		FinalOutTimeUS:   7_200_000_000,
	}))
	require.NoError(t, s.UpdateJobProgress(t.Context(), job.ID, 42.5, 3.75, 23.98, 180))

	reloaded, err := s.GetJob(t.Context(), job.ID)
	require.NoError(t, err)
	require.Equal(t, "/library/.codarr-staging-1.mkv", reloaded.StagingPath)
	require.Equal(t, []string{"ffmpeg", "-i", "in.mkv", "out.mkv"}, reloaded.FfmpegArgv)
	require.Equal(t, domain.EncoderQSV, reloaded.EncoderUsed)
	require.Equal(t, domain.DecodeHardware, reloaded.DecodePath)
	require.True(t, reloaded.FellBack)
	require.Equal(t, "qsv init failed", reloaded.FallbackReason)
	require.InEpsilon(t, 42.5, reloaded.ProgressPct, 0.0001)
	require.InEpsilon(t, 3.75, reloaded.ProgressSpeed, 0.0001)
	require.InEpsilon(t, 23.98, reloaded.ProgressFPS, 0.0001)
	require.Equal(t, 180, reloaded.EstimatedSeconds)

	// plan.md 15.3 needs ffmpeg's own out_time and 19.2 resumes in another process,
	// so it has to survive the round trip rather than live in the worker's memory.
	require.Equal(t, int64(7_200_000_000), reloaded.FinalOutTimeUS)
}

func TestJobStore_GetJobNotFound(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	_, err := s.GetJob(t.Context(), 404)
	require.ErrorIs(t, err, store.ErrNotFound)
}
