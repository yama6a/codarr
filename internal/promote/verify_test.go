package promote_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

func TestVerify_AcceptsAMatchingOutput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	warnings, err := h.promoter.Verify(t.Context(), request())
	require.NoError(t, err)
	require.Empty(t, warnings)
}

func TestVerify_FailsWhenFfprobeCannotReadTheOutput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	boom := errors.New("moov atom not found")
	h.prober.ProbeFunc = func(context.Context, string) (promote.Output, error) { return promote.Output{}, boom }

	_, err := h.promoter.Verify(t.Context(), request())
	requireFailure(t, err, domain.FailVerification, "ffprobe could not read the output", "moov atom not found")
	require.ErrorIs(t, err, boom)
}

func TestVerify_FailsWhenTheOutputCannotBeStatted(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	require.NoError(t, h.fs.Remove(stagingPath))

	_, err := h.promoter.Verify(t.Context(), request())
	requireFailure(t, err, domain.FailVerification, "could not be stat'd")
}

// plan.md 19.1: not "verification failed" but the two durations that differ.
func TestVerify_DurationOutsideOnePercent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 4382 })

	req := request()
	req.Source.DurationSeconds = 5121

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "output duration 4382s differs from source 5121s by more than 1%")
}

func TestVerify_DurationExactlyAtTheTolerance(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 5000 * 0.99 })

	req := request()
	req.Source.DurationSeconds = 5000

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}

func TestVerify_UnknownSourceDurationOnANonLegacyContainer(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Source.DurationSeconds = 0

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "the source duration is unknown")
}

// plan.md 15.3: VOB and AVI headers routinely lie, so ffmpeg's own out_time is
// the ground truth for what it wrote. The source mismatch becomes a warning.
func TestVerify_LegacyContainerFallsBackToFfmpegOutTime(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 4382 })

	req := request()
	req.Source.DurationSeconds = 5121
	req.Source.LegacyContainer = true
	req.FinalOutTimeSeconds = 4380

	warnings, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, []string{
		"the legacy source container reported a duration of 5121s but ffmpeg wrote 4380s; trusting ffmpeg's out_time",
	}, warnings)
}

func TestVerify_LegacyContainerWithNoOutTimeToFallBackOn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 4382 })

	req := request()
	req.Source.DurationSeconds = 5121
	req.Source.LegacyContainer = true

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "no final out_time to check against")
}

func TestVerify_LegacyContainerWhereEvenOutTimeDisagrees(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 4382 })

	req := request()
	req.Source.DurationSeconds = 5121
	req.Source.LegacyContainer = true
	req.FinalOutTimeSeconds = 5100

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "differs from ffmpeg's own out_time 5100s by more than 1%")
}

func TestVerify_StreamCountMismatch(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams = o.Streams[:2] })

	_, err := h.promoter.Verify(t.Context(), request())
	requireFailure(t, err, domain.FailVerification, "the output has 2 streams, the plan expected 3")
}

func TestVerify_StreamTypeAtTheWrongOutputPosition(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[1], o.Streams[2] = o.Streams[2], o.Streams[1] })

	_, err := h.promoter.Verify(t.Context(), request())
	requireFailure(t, err, domain.FailVerification, "output stream 1 is subtitle, the plan expected audio at that position")
}

// The plan's output indices, not its source indices, decide the expected order.
func TestVerify_ExpectedOrderComesFromTheOutputIndices(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Plan.Streams = []domain.StreamPlan{
		{Type: domain.StreamSubtitle, SourceIndex: 9, OutputIndex: outIdx(2), Decision: domain.DecisionCopy, Language: "eng"},
		{Type: domain.StreamAudio, SourceIndex: 5, OutputIndex: outIdx(1), Decision: domain.DecisionConvert, Language: "eng"},
		{Type: domain.StreamVideo, SourceIndex: 7, OutputIndex: outIdx(0), Decision: domain.DecisionCopy},
	}

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}

func TestVerify_NoAudioStreams(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) {
		o.Streams = []promote.OutputStream{o.Streams[0], o.Streams[2]}
	})

	req := request()
	req.Plan.Streams = []domain.StreamPlan{
		{Type: domain.StreamVideo, OutputIndex: outIdx(0), Decision: domain.DecisionCopy},
		{Type: domain.StreamSubtitle, OutputIndex: outIdx(1), Decision: domain.DecisionCopy},
	}

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "the output has no audio streams at all")
}

func TestVerify_MissingExpectedAudioLanguage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Plan.Streams = append(req.Plan.Streams, domain.StreamPlan{
		Type: domain.StreamAudio, SourceIndex: 4, OutputIndex: outIdx(3), Decision: domain.DecisionCopy, Language: "fre",
	})
	h.probeReturns(func(o *promote.Output) {
		o.Streams = append(o.Streams, promote.OutputStream{Type: domain.StreamAudio, Codec: "ac3", Language: "ger"})
	})

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, `no audio stream tagged language "fre"`)
}

func TestVerify_AudioLanguageComparisonIgnoresCase(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[1].Language = "ENG" })

	_, err := h.promoter.Verify(t.Context(), request())
	require.NoError(t, err)
}

func TestVerify_CopiedVideoThatSilentlyReEncoded(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		mutate func(*promote.Output)
		want   string
	}{
		"codec": {
			mutate: func(o *promote.Output) { o.Streams[0].Codec = "h264" },
			want:   `the output video codec is "h264" and the source was "hevc"`,
		},
		"profile": {
			mutate: func(o *promote.Output) { o.Streams[0].Profile = "Main" },
			want:   `the output video profile is "Main" and the source was "Main 10"`,
		},
		"resolution": {
			mutate: func(o *promote.Output) { o.Streams[0].Width, o.Streams[0].Height = 1920, 1080 },
			want:   "the output resolution is 1920x1080 and the source was 3840x2160",
		},
		"level": {
			mutate: func(o *promote.Output) { o.Streams[0].Level = "4.0" },
			want:   `the output video level is "4.0" and the source was "5.1"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.probeReturns(tc.mutate)

			_, err := h.promoter.Verify(t.Context(), request())
			requireFailure(t, err, domain.FailVerification, tc.want, "the plan said copy")
		})
	}
}

func TestVerify_LevelComparisonIgnoresTheDot(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[0].Level = "51" })

	_, err := h.promoter.Verify(t.Context(), request())
	require.NoError(t, err)
}

// plan.md 15.3: the level is exempt exactly when the plan recorded a rewrite,
// and then the output has to be 4.2.
func TestVerify_LevelRewriteMustProduce42(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[0].Level = "42" })

	req := request()
	req.Plan.LevelRewrite = true

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}

func TestVerify_LevelRewriteThatDidNotTake(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Plan.LevelRewrite = true

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, `rewrote the H.264 level to 4.2 but the output reports level "5.1"`)
}

// A plan that copies the video but never maps it to an output position: the
// shape check passes and the video check is the one that catches it.
func TestVerify_CopiedVideoMissingFromTheOutput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams = o.Streams[1:] })

	req := request()
	req.Plan.Streams[0].OutputIndex = nil

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "the plan copies the video stream but the output has no video stream")
}

func TestVerify_EncodedVideoIsNotComparedAgainstTheSource(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) {
		o.Streams[0].Codec, o.Streams[0].Profile, o.Streams[0].Level = "hevc", "Main 10", "4.0"
	})

	req := request()
	req.Plan.Streams[0].Decision = domain.DecisionEncode
	req.Source.Video.Codec = "h264"

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}

func TestVerify_UnknownSourceVideoStateIsNotCompared(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[0].Codec = "h264" })

	req := request()
	req.Source.Video = nil

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}

// plan.md 9: profile 5 has no HDR10 base layer, so a lost RPU renders green and
// purple. That is a hard failure.
func TestVerify_DolbyVisionProfile5WithoutTheRecordFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Plan.DolbyVision = true
	req.Plan.DolbyVisionProfile = 5

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification,
		"Dolby Vision profile 5", "no DOVI configuration record", "renders with wrong colour")
}

func TestVerify_DolbyVisionProfile5WithTheRecordPasses(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.Streams[0].DolbyVision = true })

	req := request()
	req.Plan.DolbyVision = true
	req.Plan.DolbyVisionProfile = 5

	warnings, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
	require.Empty(t, warnings)
}

// Profiles 7 and 8 degrade to HDR10, so losing the record is a warning.
func TestVerify_DolbyVisionProfile7WithoutTheRecordWarns(t *testing.T) {
	t.Parallel()

	for _, profile := range []int{7, 8} {
		t.Run(string(rune('0'+profile)), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			req := request()
			req.Plan.DolbyVision = true
			req.Plan.DolbyVisionProfile = profile

			warnings, err := h.promoter.Verify(t.Context(), req)
			require.NoError(t, err)
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], "degrades to HDR10")
		})
	}
}

// plan.md 15.3: the size check is for full plans only. An audio_only job grows
// a file legitimately when a 1.5 Mbps DTS track becomes 640k AC3.
func TestVerify_FullPlanThatGrewTheFileFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.addFile(stagingPath, sourceSize+1)

	req := request()
	req.Plan.Kind = domain.KindFull

	_, err := h.promoter.Verify(t.Context(), req)
	requireFailure(t, err, domain.FailVerification, "larger than the 8.0 GiB (8589934592 bytes) source", "full transcode")
}

func TestVerify_AudioOnlyPlanMayGrowTheFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.addFile(stagingPath, sourceSize+1)

	_, err := h.promoter.Verify(t.Context(), request())
	require.NoError(t, err)
}

func TestVerify_FullPlanOfExactlyTheSourceSizePasses(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.addFile(stagingPath, sourceSize)

	req := request()
	req.Plan.Kind = domain.KindFull

	_, err := h.promoter.Verify(t.Context(), req)
	require.NoError(t, err)
}
