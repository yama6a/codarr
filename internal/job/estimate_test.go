package job_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// plan.md 14.3: seed both estimates with conservative defaults until data
// exists. An I/O bound job moves the whole file twice.
func TestService_EnqueueEstimateUsesTheSeedUntilSomethingHasBeenMeasured(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)

	want := int(math.Ceil(float64(sourceSize) * 2 / job.SeedThroughput))
	require.Equal(t, want, h.jobRow(*res.JobID).Transform.Duration.Estimated)
}

func TestService_EnqueueEstimateForAFullJobIsEncodeBound(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)

	// media_duration / speed_ratio, not bytes: the cost is the encoder.
	require.Equal(t, int(mediaDur/job.SeedSpeedHardware), h.jobRow(*res.JobID).Transform.Duration.Estimated)
}

// plan.md 14.3: the enqueue estimate for a full job is rough because neither
// the sample probe nor the encoder choice has happened. Starting the job
// refines it against the encoder actually selected.
func TestService_TheEstimateIsRefinedWhenTheJobStarts(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))
	encoderWithSamples(h)

	h.store.throughput[throughputKey(domain.KindFull, string(domain.EncoderQSV), "1080p")] = domain.ThroughputStat{
		Kind: domain.KindFull, Encoder: string(domain.EncoderQSV), Resolution: "1080p",
		Samples: 4, AvgValue: 3,
	}

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)
	require.Equal(t, 4800, h.jobRow(*res.JobID).Transform.Duration.Estimated)

	h.addFile(stagingPath, sourceSize/2)

	_, err = h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, 2400, h.store.executions[0].EstimatedSeconds,
		"the measured speed of this encoder at this resolution replaces the seed")
}

func TestService_TheRollingAverageBlendsSuccessiveJobs(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.throughput[throughputKey(domain.KindAudioOnly, "", "")] = domain.ThroughputStat{
		Kind: domain.KindAudioOnly, Samples: 1, AvgValue: 100 << 20,
	}

	h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		h.clk.Advance(10 * time.Minute)

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	stat, err := h.store.GetThroughputStat(t.Context(), domain.KindAudioOnly, "", "")
	require.NoError(t, err)
	require.Equal(t, 2, stat.Samples)

	observed := float64(sourceSize) * 2 / 600
	require.InDelta(t, (float64(100<<20)+observed)/2, stat.AvgValue, 1)
}

// plan.md 14.3 stores both, and the UI shows both.
func TestService_BothTheEstimateAndTheMeasurementAreKept(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		h.clk.Advance(7 * time.Minute)

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Len(t, h.store.promotions, 1)
	require.Equal(t, 420, h.store.promotions[0].ActualSeconds)

	transform := h.store.promotions[0].Transform
	require.Positive(t, transform.Duration.Estimated)
	require.NotNil(t, transform.Duration.Actual)
	require.Equal(t, 420, *transform.Duration.Actual)
}
