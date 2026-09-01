package job_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// 30 MB per 60-second sample is exactly 4 Mbps, which after the hardware
// correction still sits inside the 1080p clamps and under the 0.85 source clamp.
const sampleSize = int64(30_000_000)

// encoderWithSamples answers the sample probe with real files and the encode
// with success, which is what lets a test assert the order of the two.
func encoderWithSamples(h *harness) {
	h.encoder.RunFunc = func(_ context.Context, args []string, progress func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		out := args[len(args)-1]
		if strings.Contains(out, ".codarr-probe-") {
			h.addFile(out, sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		}

		if progress != nil {
			progress(ffmpeg.Progress{Percent: 100, Speed: 1.5})
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}
}

func TestService_FullJobRunsTheSampleProbeBeforeTheEncode(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))
	encoderWithSamples(h)

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	runs := h.runArgs()
	require.Len(t, runs, 4, "three sample segments (8.1) and then the encode")

	for _, args := range runs[:3] {
		require.True(t, containsArg(args, ".codarr-probe-"), "the first three runs are the sample probe")
		require.True(t, containsArg(args, "libx265"))
	}

	require.False(t, containsArg(runs[3], ".codarr-probe-"))
	require.True(t, containsArg(runs[3], stagingPath))
}

func TestService_FullJobFillsTheTargetBitrateTheTransformLeftNull(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))
	encoderWithSamples(h)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)
	h.addFile(stagingPath, sourceSize/2)

	// 17.2: the record is written at enqueue with a null target because the
	// sample probe has not run, and the UI shows "calculating" until here.
	require.Nil(t, h.jobRow(*res.JobID).Transform.Video.After.BitrateKbps)

	_, err = h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	written := h.store.executions
	require.NotEmpty(t, written)
	require.True(t, containsArg(written[0].FfmpegArgv, sampledTarget()),
		"the measured target reaches the argv: %v", written[0].FfmpegArgv)

	// The transform is overwritten with the measured result at completion, so
	// the value under test is the one the encode actually used.
	require.Contains(t, strings.Join(written[0].FfmpegArgv, " "), "-b:v "+sampledTarget())
}

func TestService_FullJobFallsBackToTheFormulaWhenTheSampleProbeFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if strings.Contains(args[len(args)-1], ".codarr-probe-") {
			return ffmpeg.RunResult{}, errors.New("libx265 not available")
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	// 8.2 exists precisely so a failed probe is not a failed job.
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	fallback := ffmpeg.TargetFromFallback(ffmpeg.BitrateInput{
		Width: 1920, Height: 1080, FPS: 24000.0 / 1001.0, SourceBitrate: 8_420_000,
	})
	require.Positive(t, fallback)
	require.Contains(t, strings.Join(h.store.executions[0].FfmpegArgv, " "),
		"-b:v "+itoa(fallback))
}

func TestService_IdetSampleDecidesTheScanAndRePlansTheJob(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(idetProbe()))

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		out := args[len(args)-1]

		switch {
		case strings.Contains(out, ".codarr-probe-"):
			h.addFile(out, sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		case containsArg(args, "idet"):
			return ffmpeg.RunResult{Argv: args, StderrTail: strings.Join([]string{
				"[Parsed_idet_0 @ 0x1] Repeated Fields: Neither:   480 Top:     0 Bottom:     0",
				"[Parsed_idet_0 @ 0x1] Single frame detection: TFF:   400 BFF:    20 Progressive:    60 Undetermined:    20",
				"[Parsed_idet_0 @ 0x1] Multi frame detection: TFF:   420 BFF:    20 Progressive:    40 Undetermined:    20",
			}, "\n")}, nil
		default:
			return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
		}
	}

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	runs := h.runArgs()
	require.Len(t, runs, 5, "three samples, the idet sample, then the encode")
	require.True(t, containsArg(runs[3], "idet"))
	require.True(t, containsArg(runs[3], "-frames:v"))

	require.True(t, containsArg(runs[4], "deinterlace"),
		"6.2: the sample said interlaced, so the re-plan deinterlaces: %v", runs[4])
}

func TestService_IdetSampleFailureLeavesTheSourceProgressive(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(idetProbe()))

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		out := args[len(args)-1]

		switch {
		case strings.Contains(out, ".codarr-probe-"):
			h.addFile(out, sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		case containsArg(args, "idet"):
			return ffmpeg.RunResult{}, errors.New("decoder unavailable")
		default:
			return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
		}
	}

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	runs := h.runArgs()
	require.False(t, containsArg(runs[len(runs)-1], "deinterlace"),
		"an unreadable idet sample is progressive, which is the default of 6.2")
}

func TestService_HardwareDecodeFailureRetriesOnceInSoftware(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
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

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	done := h.jobRow(j.ID)
	require.Equal(t, domain.JobDone, done.State)
	require.Equal(t, 2, attempts, "10.1 retries a failed hardware decode exactly once")
	require.Equal(t, domain.DecodeSoftware, done.DecodePath)
	require.True(t, done.FellBack)
	require.Contains(t, done.FallbackReason, "hardware decode failed")
}

func TestService_SoftwareEncoderFallbackIsRecordedOnTheJob(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))
	encoderWithSamples(h)

	h.hw.CapabilitiesFunc = func(context.Context) (hardware.Capabilities, error) {
		return softwareOnly(), nil
	}

	j := h.queue(domain.KindFull, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	done := h.jobRow(j.ID)
	require.Equal(t, domain.EncoderSoftware, done.EncoderUsed)
	require.True(t, done.FellBack)
	require.Contains(t, done.FallbackReason, "neither QSV nor VAAPI encodes HEVC main")
}

func TestService_SpaceSweepJobForcesAVideoEncodeThePolicyWouldCopy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	encoderWithSamples(h)

	// The default fixture plans as audio_only: its H.264 video passes the copy
	// test. Only the sweep re-encodes it (11).
	require.Equal(t, domain.KindAudioOnly, h.mediaRow().PlanKind)

	j := h.queue(domain.KindFull, domain.OriginSpaceSweep)
	h.addFile(stagingPath, sourceSize/2)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)

	runs := h.runArgs()
	require.Len(t, runs, 4, "the sweep re-encodes, so the sample probe runs too")

	encode := strings.Join(runs[3], " ")
	require.Contains(t, encode, "-c:v hevc_qsv")
	require.Contains(t, encode, "-b:v "+sampledTarget())
}

func TestService_ThroughputIsMeasuredAtCompletion(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
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
	require.Equal(t, 1, stat.Samples)

	// 14.3: an I/O bound job moves the file twice, so the observed rate is
	// source_bytes * 2 / elapsed.
	require.InDelta(t, float64(sourceSize)*2/600, stat.AvgValue, 1)
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func TestService_ExhaustingBothFallbackChainsFailsOnceNamingEveryAttempt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(hwFullProbe()))

	var encodes int

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		if strings.Contains(args[len(args)-1], ".codarr-probe-") {
			h.addFile(args[len(args)-1], sampleSize)

			return ffmpeg.RunResult{Argv: args}, nil
		}

		encodes++

		return ffmpeg.RunResult{StderrTail: "[hevc @ 0x1] Error while encoding"},
			fmt.Errorf("%w: exit status 1", ffmpeg.ErrFfmpegFailed)
	}

	j := h.queue(domain.KindFull, domain.OriginIngest)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	// 10.1 then 10.2: the decode retry on each hardware encoder, then the next
	// encoder down, ending at libx265 where neither chain has anywhere to go.
	require.Equal(t, 5, encodes)

	failed := h.requireFailed(j.ID, domain.FailFfmpeg,
		"hevc_qsv with hardware decode",
		"hevc_qsv with software decode",
		"hevc_vaapi with hardware decode",
		"hevc_vaapi with software decode",
		"libx265 with software decode",
		"Error while encoding")

	require.Equal(t, 0, failed.Attempt, "the in-job retries are not the interruption budget of 19.2")
	require.True(t, failed.FellBack)
}

func TestService_EncoderChainIsNotSteppedForAJobThatCopiesVideo(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	var encodes int

	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)
		encodes++

		return ffmpeg.RunResult{StderrTail: "[matroska @ 0x1] Could not write header"},
			fmt.Errorf("%w: exit status 1", ffmpeg.ErrFfmpegFailed)
	}

	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Equal(t, 1, encodes, "no video is encoded, so there is no encoder to fall back to")
	h.requireFailed(j.ID, domain.FailFfmpeg, "stream copy with software decode")
}

func TestService_TheFinalOutTimeIsPersistedWhenTheEncodeEnds(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.queue(domain.KindAudioOnly, domain.OriginIngest)
	h.addFile(stagingPath, sourceSize/2)

	_, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)

	require.Len(t, h.store.executions, 2, "recorded before the run for 19.2, and again once out_time is known")
	require.Zero(t, h.store.executions[0].FinalOutTimeUS)
	require.Equal(t, int64(7_200_000_000), h.store.executions[1].FinalOutTimeUS)
	require.Equal(t, int64(7_200_000_000), h.jobRow(j.ID).FinalOutTimeUS)

	promoteCalls := h.promoter.PromoteCalls()
	require.Len(t, promoteCalls, 1)
	require.InDelta(t, mediaDur, promoteCalls[0].Req.FinalOutTimeSeconds, 0.001)
}

// Derived from the constant so retuning it does not fail tests that are about the
// plumbing, not the value.
func sampledTarget() string {
	return strconv.Itoa(int(4_000_000 * ffmpeg.HardwareCorrection))
}
