package job_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

func TestService_RunOnceExecutesAJobEndToEnd(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	done := h.jobRow(j.ID)
	require.Equal(t, domain.JobDone, done.State)
	require.Equal(t, stagingPath, done.StagingPath)
	require.Equal(t, outputFP, done.OutputFingerprint)
	require.Equal(t, sourceSize/2, done.OutputSize)
	require.Empty(t, done.EncoderUsed, "an audio_only job encodes no video, so it needs no video encoder")
	require.NotEmpty(t, done.FfmpegArgv)

	require.NotNil(t, done.Transform.OutputIdentity)
	require.Equal(t, outputFP, done.Transform.OutputIdentity.Fingerprint)
	require.NotNil(t, done.Transform.Duration.Actual)

	media := h.mediaRow()
	require.Equal(t, domain.MediaDone, media.Status)
	require.Equal(t, outputFP, media.CodarrOutputFingerprint)
	require.Equal(t, domain.ProvenanceCodarrOutput, media.Provenance)

	require.Equal(t, []domain.JobState{domain.JobVerifying, domain.JobPromoting}, statesSet(h))
}

// statesSet is every explicit state transition the worker wrote, in order.
func statesSet(h *harness) []domain.JobState {
	var out []domain.JobState

	for _, call := range h.store.callList() {
		if len(call) > len("SetJobState:") && call[:len("SetJobState:")] == "SetJobState:" {
			out = append(out, domain.JobState(call[len("SetJobState:"):]))
		}
	}

	return out
}

func TestService_RunOnceReportsNothingToDoOnAnEmptyQueue(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.False(t, ran)
}

func TestService_RunOnceStartsNothingWhilePaused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)

	require.NoError(t, h.svc.Pause(t.Context()))

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.False(t, ran)
	require.Equal(t, domain.JobQueued, h.jobRow(j.ID).State)

	require.NoError(t, h.svc.Resume(t.Context()))

	ran, err = h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)
}

func TestService_PausePersistsToSettings(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	require.NoError(t, h.svc.Pause(t.Context()))

	paused, err := h.svc.Paused(t.Context())
	require.NoError(t, err)
	require.True(t, paused)

	settings, err := h.store.GetSettings(t.Context())
	require.NoError(t, err)
	require.True(t, settings.QueuePaused)

	require.NoError(t, h.svc.Resume(t.Context()))

	settings, err = h.store.GetSettings(t.Context())
	require.NoError(t, err)
	require.False(t, settings.QueuePaused)
}

func TestService_CancelRunningJobCleansTheStagingFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/4)

	started := make(chan struct{})
	h.encoder.RunFunc = func(ctx context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		close(started)
		<-ctx.Done()

		return ffmpeg.RunResult{StderrTail: "Exiting normally, received signal 15."}, ctx.Err()
	}

	result := make(chan error, 1)

	go func() {
		_, err := h.svc.RunOnce(t.Context())
		result <- err
	}()

	<-started
	require.NoError(t, h.svc.Cancel(t.Context(), j.ID))
	require.NoError(t, <-result)

	cancelled := h.jobRow(j.ID)
	require.Equal(t, domain.JobCancelled, cancelled.State)
	require.Empty(t, cancelled.FailureCode, "a cancel is not a failure")
	require.Contains(t, h.removedPaths(), stagingPath)
	require.Equal(t, domain.MediaAnalyzed, h.mediaRow().Status)
}

func TestService_CancelQueuedJob(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)

	require.NoError(t, h.svc.Cancel(t.Context(), j.ID))
	require.Equal(t, domain.JobCancelled, h.jobRow(j.ID).State)
}

func TestService_RestartPutsACancelledJobAtTheFront(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	queued := h.store.putJob(domain.Job{
		MediaFileID: 99, Kind: domain.KindFull, State: domain.JobQueued,
		Priority: domain.PriorityFull, QueuedAt: h.clk.Now(),
	})
	cancelled := h.store.putJob(domain.Job{
		MediaFileID: mediaID, Kind: domain.KindAudioOnly, State: domain.JobCancelled,
		Priority: domain.PriorityQuick, QueuedAt: h.clk.Now(),
	})

	restarted, err := h.svc.Restart(t.Context(), cancelled.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobQueued, restarted.State)
	require.Equal(t, 0, restarted.Attempt)
	require.Less(t, restarted.Priority, h.jobRow(queued.ID).Priority,
		"a restarted job runs ahead of everything already queued (19)")
}

func TestService_ProgressReachesTheDatabaseEveryFiveSecondsNotPerLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue(domain.KindAudioOnly, domain.OriginIngest)

	h.encoder.RunFunc = func(_ context.Context, args []string, progress func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		// One progress block per second of wall time, which is roughly what
		// ffmpeg emits. Eleven of them, spanning ten seconds.
		for i := range 11 {
			progress(ffmpeg.Progress{Percent: float64(i) * 10, Speed: 2})
			h.clk.Advance(time.Second)
		}

		return ffmpeg.RunResult{FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	percents := make([]float64, 0, len(h.store.progress))
	for _, p := range h.store.progress {
		percents = append(percents, p.Pct)
	}

	require.Equal(t, []float64{0, 50, 100}, percents,
		"14.3: the live value stays in memory and is flushed every five seconds")
}

func TestService_RunStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- h.svc.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestService_ShutdownLeavesTheJobInFlightForTheStartupSweep(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})

	h.encoder.RunFunc = func(runCtx context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		close(started)
		<-runCtx.Done()

		return ffmpeg.RunResult{}, runCtx.Err()
	}

	result := make(chan error, 1)

	go func() {
		_, err := h.svc.RunOnce(ctx)
		result <- err
	}()

	<-started
	cancel()
	require.NoError(t, <-result)

	// plan.md 19.2 owns this row now: a shutdown is an interruption, not a
	// cancellation and not a failure.
	require.Equal(t, domain.JobRunning, h.jobRow(j.ID).State)
}

func TestService_VerificationFailureKeepsTheStagingFileForInspection(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.promoter.PromoteFunc = func(context.Context, promote.Request) (promote.Result, error) {
		return promote.Result{}, &promote.Error{
			Code:    domain.FailVerification,
			Message: "output duration 4382s differs from source 5121s by more than 1%",
		}
	}

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	h.requireFailed(j.ID, domain.FailVerification, "4382s", "5121s")
	require.NotContains(t, h.removedPaths(), stagingPath,
		"15.3 keeps the staged output for inspection when verification fails")
}

func TestService_PostRenameFailureStillPersistsTheOutputIdentity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.promoter.PromoteFunc = func(_ context.Context, req promote.Request) (promote.Result, error) {
		result := promote.Result{
			Identity: domain.OutputIdentity{
				Fingerprint: outputFP,
				SizeBytes:   sourceSize / 2,
				MTime:       sourceMTime,
				PolicyHash:  req.Plan.PolicyHash,
			},
			OutputSize: sourceSize / 2,
			Renamed:    true,
		}

		return result, &promote.Error{
			Code:    domain.FailPromote,
			Message: "the replace succeeded but restoring the modification time on " + sourcePath + " failed",
		}
	}

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	// The source is already gone, so the identity has to survive even though
	// the job failed; without it provenance reads untouched forever (12).
	media := h.mediaRow()
	require.Equal(t, outputFP, media.CodarrOutputFingerprint)
	require.Equal(t, sourceSize/2, media.CodarrOutputSize)

	failed := h.requireFailed(j.ID, domain.FailPromote, "restoring the modification time")
	require.NotNil(t, failed.Transform.OutputIdentity)
	require.Equal(t, outputFP, failed.Transform.OutputIdentity.Fingerprint)
}

func TestService_EveryFailurePathCarriesACodeAndAMessage(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	cases := []struct {
		name    string
		arrange func(h *harness)
		code    domain.FailureCode
		wants   []string
	}{
		{
			name: "preflight",
			arrange: func(h *harness) {
				h.promoter.PreflightFunc = func(req promote.PreflightRequest) (promote.Staging, error) {
					return promote.Staging{}, &promote.Error{
						Code:    domain.FailPreflight,
						Message: "the source " + req.SourcePath + " has 2 hard links; replacing it would damage the other copies",
					}
				}
			},
			code:  domain.FailPreflight,
			wants: []string{"2 hard links"},
		},
		{
			name: "ffmpeg",
			arrange: func(h *harness) {
				h.encoder.RunFunc = func(context.Context, []string, func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
					return ffmpeg.RunResult{StderrTail: "frame= 12\n[hevc_qsv @ 0x1] Error initializing an internal MFX session"},
						fmt.Errorf("%w: exit status 1", ffmpeg.ErrFfmpegFailed)
				}
			},
			code:  domain.FailFfmpeg,
			wants: []string{"Error initializing an internal MFX session"},
		},
		{
			name: "output probe",
			arrange: func(h *harness) {
				h.prober.ProbeFunc = func(context.Context, string) (*ffprobe.Result, error) {
					return nil, fmt.Errorf("%w: %w", ffprobe.ErrProbeFailed, boom)
				}
			},
			code:  domain.FailProbe,
			wants: []string{"could not be probed"},
		},
		{
			name: "promotion",
			arrange: func(h *harness) {
				h.promoter.PromoteFunc = func(context.Context, promote.Request) (promote.Result, error) {
					return promote.Result{}, &promote.Error{
						Code:    domain.FailPromote,
						Message: "fsync of the staging file " + stagingPath + " failed",
					}
				}
			},
			code:  domain.FailPromote,
			wants: []string{"fsync of the staging file"},
		},
		{
			name: "encoder selection",
			arrange: func(h *harness) {
				h.store.putMedia(mediaFile(fullProbe()))
				h.hw.CapabilitiesFunc = func(context.Context) (hardware.Capabilities, error) {
					return hardware.Capabilities{}, boom
				}
			},
			code:  domain.FailInternal,
			wants: []string{"hardware capabilities could not be read"},
		},
		{
			name: "unanalysed file",
			arrange: func(h *harness) {
				m := mediaFile(audioOnlyProbe())
				m.ProbeJSON = ""
				h.store.putMedia(m)
			},
			code:  domain.FailProbe,
			wants: []string{"no stored ffprobe result"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			tc.arrange(h)

			j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
			h.addFile(stagingPath, sourceSize/2)

			ran, err := h.svc.RunOnce(t.Context())
			require.NoError(t, err)
			require.True(t, ran)

			h.requireFailed(j.ID, tc.code, tc.wants...)
		})
	}
}

func TestService_FfmpegFailurePersistsTheStderrTail(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)

	tail := "frame= 12 fps=3\n[hevc_qsv @ 0x1] Error initializing an internal MFX session"
	h.encoder.RunFunc = func(context.Context, []string, func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		return ffmpeg.RunResult{StderrTail: tail}, fmt.Errorf("%w: exit status 1", ffmpeg.ErrFfmpegFailed)
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	failed := h.requireFailed(j.ID, domain.FailFfmpeg)
	require.Equal(t, tail, failed.StderrTail, "19.1: the stderr tail goes on the row for ffmpeg_failed")
}

// plan.md 15.2 step 4: Plex is streaming the target, so the job waits rather
// than replacing a file an NFS client has open (15.6).
func TestService_APromotionBlockedByPlexMovesTheJobToAwaitingStreamEnd(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	var blockedState domain.JobState

	h.promoter.PromoteFunc = func(_ context.Context, req promote.Request) (promote.Result, error) {
		req.OnBlocked("Alice is watching this file on Plex")
		blockedState = h.jobRow(j.ID).State

		return promote.Result{
			Identity:   domain.OutputIdentity{Fingerprint: outputFP, SizeBytes: sourceSize / 2},
			OutputSize: sourceSize / 2,
			Renamed:    true,
		}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, domain.JobAwaitingStreamEnd, blockedState)

	done := h.jobRow(j.ID)
	require.Equal(t, domain.JobDone, done.State)
	require.Empty(t, done.BlockedBy, "the reason is cleared once the file is promoted")
}

func TestService_RunDrainsTheQueue(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))

	first := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	second := h.store.putJob(domain.Job{
		MediaFileID: 11, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest,
		Priority: domain.PriorityNormal, State: domain.JobQueued, QueuedAt: h.clk.Now(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() { _ = h.svc.Run(ctx) }()

	require.Eventually(t, func() bool {
		return h.jobRow(first.ID).State == domain.JobDone && h.jobRow(second.ID).State == domain.JobDone
	}, 2*time.Second, time.Millisecond)
}

// TestService_FpsRidesTheSameThrottledProgressWrite is plan.md 18.1's fps on the
// current-job card. The parser has always read the key; what mattered here was
// that carrying it to the UI costs no second write (14.3).
func TestService_FpsRidesTheSameThrottledProgressWrite(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.encoder.RunFunc = func(_ context.Context, args []string, progress func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		for i := range 11 {
			progress(ffmpeg.Progress{Percent: float64(i) * 10, Speed: 2, FPS: 118.5})
			h.clk.Advance(time.Second)
		}

		return ffmpeg.RunResult{FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	estimate := h.jobRow(j.ID).EstimatedSeconds
	require.Positive(t, estimate)

	require.Equal(t, []progressWrite{
		{JobID: j.ID, Pct: 0, Speed: 2, FPS: 118.5, Estimated: estimate},
		{JobID: j.ID, Pct: 50, Speed: 2, FPS: 118.5, Estimated: estimate},
		{JobID: j.ID, Pct: 100, Speed: 2, FPS: 118.5, Estimated: estimate},
	}, h.store.progress)
}
