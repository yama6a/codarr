package job_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

func TestService_MetricsRecordEveryTransitionOfACompletedJob(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		h.clk.Advance(7 * time.Minute)

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, []transition{
		{State: domain.JobRunning, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest},
		{State: domain.JobVerifying, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest},
		{State: domain.JobPromoting, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest},
		{State: domain.JobDone, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest},
	}, h.metrics.states())

	got := h.metrics.snapshot()
	require.Equal(t, []duration{{Kind: domain.KindAudioOnly, Seconds: 420}}, got.Durations)
	require.Len(t, got.EstimateErrors, 1)
	require.Empty(t, got.Failed)
	require.Empty(t, got.Errors)
}

// plan.md 14.3 stores the estimate and the measurement precisely so the delta
// between them can be watched; this is that delta.
func TestService_MetricsRecordTheEstimateErrorAsActualMinusEstimated(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		h.clk.Advance(10 * time.Minute)

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	done := h.jobRow(j.ID)
	require.Equal(t, 600, done.ActualSeconds)

	got := h.metrics.snapshot()
	require.Equal(t, []float64{float64(600 - done.EstimatedSeconds)}, got.EstimateErrors)
}

func TestService_MetricsRecordAFailureWithItsCode(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.promoter.PromoteFunc = func(context.Context, promote.Request) (promote.Result, error) {
		return promote.Result{}, &promote.Error{
			Code:    domain.FailVerification,
			Message: "output duration 4382s differs from source 5121s by more than 1%",
		}
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, []domain.FailureCode{domain.FailVerification}, h.metrics.snapshot().Failed)
	require.Contains(t, h.metrics.states(),
		transition{State: domain.JobFailed, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest})
}

func TestService_MetricsRecordTheDecodeFallback(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.store.putMedia(mediaFile(hwFullProbe()))

	attempts := 0
	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if strings.Contains(args[len(args)-1], ".codarr-probe-") {
			h.addFile(args[len(args)-1], sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		}

		attempts++
		if attempts == 1 {
			return ffmpeg.RunResult{StderrTail: "[h264_qsv @ 0x1] Error during QSV decoding"},
				errors.New("exit status 1")
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	got := h.metrics.snapshot()
	require.Equal(t, 1, got.DecodeChain)
	require.Empty(t, got.EncoderChain, "the decode retry runs first and succeeded, so the chain never stepped")
}

func TestService_MetricsRecordEveryStepOfTheEncoderChain(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if strings.Contains(args[len(args)-1], ".codarr-probe-") {
			h.addFile(args[len(args)-1], sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		}

		return ffmpeg.RunResult{StderrTail: "Device creation failed"}, errors.New("exit status 1")
	}

	h.queue(domain.KindFull, domain.OriginIngest)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	got := h.metrics.snapshot()
	require.Equal(t, []fallback{
		{From: domain.EncoderQSV, To: domain.EncoderVAAPI},
		{From: domain.EncoderVAAPI, To: domain.EncoderSoftware},
	}, got.EncoderChain)
	require.Equal(t, []domain.FailureCode{domain.FailFfmpeg}, got.Failed)
}

func TestService_MetricsRecordAnEnqueueAndTheQueueDepth(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginManual)
	require.NoError(t, err)
	require.True(t, res.Enqueued)

	require.Equal(t, []transition{
		{State: domain.JobQueued, Kind: domain.KindAudioOnly, Origin: domain.OriginManual},
	}, h.metrics.states())
	require.Equal(t, []int{1}, h.metrics.queueDepth)
	require.Equal(t, []int{0}, h.metrics.awaiting)
}

func TestService_MetricsRecordAJobDeferredByAStream(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.promoter.PromoteFunc = func(_ context.Context, req promote.Request) (promote.Result, error) {
		req.OnBlocked("Plex is streaming this file to living-room")

		return promote.Result{}, errors.New("waiting to replace was cancelled")
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Contains(t, h.metrics.states(),
		transition{State: domain.JobAwaitingStreamEnd, Kind: domain.KindAudioOnly, Origin: domain.OriginIngest})
	require.Contains(t, h.metrics.awaiting, 1)
}

func TestService_MetricsRecordAnAutomaticRequeueAfterAnInterruption(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.interrupted(domain.JobRunning, 1)

	require.NoError(t, h.svc.Recover(t.Context()))

	got := h.metrics.snapshot()
	require.Equal(t, 1, got.Requeued)
	require.Empty(t, got.Failed)
}

func TestService_MetricsRecordAnInterruptedJobThatHitTheAttemptCap(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.interrupted(domain.JobRunning, domain.MaxAutoAttempts)

	require.NoError(t, h.svc.Recover(t.Context()))

	got := h.metrics.snapshot()
	require.Equal(t, 0, got.Requeued)
	require.Equal(t, []domain.FailureCode{domain.FailInterrupted}, got.Failed)
}

// The swallowed errors are the ones jobs_failed_total can never show: the job
// carried on, so nothing else reports them.
func TestService_MetricsCategoriseAnErrorTheJobSurvived(t *testing.T) {
	t.Parallel()

	h := newMeteredHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if strings.Contains(args[len(args)-1], ".codarr-probe-") {
			return ffmpeg.RunResult{}, errors.New("exit status 1")
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, []string{"bitrate_probe"}, h.metrics.snapshot().Errors)
	require.Empty(t, h.metrics.snapshot().Failed, "8.2 is the fallback, so a failed sample probe is not a failed job")
}

// Deps.Metrics is optional, so the whole pipeline has to run without one. Every
// other test in this package proves it too, by never supplying one.
func TestService_MetricsAreOptional(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	require.Nil(t, h.metrics)

	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	require.NoError(t, h.svc.Recover(t.Context()))
}

// A nil metrics value must also cost nothing: the queue gauges are read from the
// store, and without a consumer that read never happens.
func TestService_MetricsAreNotCountedWhenNoneIsWired(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginManual)
	require.NoError(t, err)

	require.NotContains(t, h.store.callList(), "CountJobsByState")
}
