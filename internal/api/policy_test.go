package api_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// GET /api/policy renders the hard-coded policy for read-only display. Every
// assertion below compares against the constants themselves rather than a copy,
// which is the point: a policy change moves the endpoint with it.

func TestGetPolicy_IsDerivedFromTheCompiledConstants(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	got := decodeInto[gen.Policy](t, h.do(t, "GET", "/api/policy", nil), 200)

	snapshot := decide.Describe()

	require.Equal(t, decide.PolicyHash(), got.PolicyHash)
	require.Equal(t, decide.TagKeys(), got.TagKeys)

	require.Equal(t, snapshot.VideoCopyCodecs, got.Video.CopyRule.Codecs)
	require.Equal(t, snapshot.H264CopyProfiles, got.Video.CopyRule.H264Profiles)
	require.Equal(t, snapshot.HevcCopyProfiles, got.Video.CopyRule.HevcProfiles)
	require.Equal(t, snapshot.CopyChroma, got.Video.CopyRule.ChromaSubsampling)
	require.Equal(t, "4.2", got.Video.CopyRule.H264MaxLevel)
	require.True(t, got.Video.CopyRule.ProgressiveOnly)
	require.True(t, got.Video.CopyRule.UnknownScanIsProgressive)

	require.Equal(t, ffmpeg.LevelRewriteBSF, got.Video.LevelRewrite.BitstreamFilter)
	require.Equal(t, snapshot.LevelRewriteMaxRefs, got.Video.LevelRewrite.MaxRefs)
	require.Equal(t, snapshot.LevelRewriteMaxWidth, got.Video.LevelRewrite.MaxWidth)
	require.Equal(t, decide.HardwareDecodeCodecs(), got.Video.HardwareDecodeCodecs)

	require.Equal(t, snapshot.AudioCopyUpTo2Channels, got.Audio.CopyList[0].Codecs)
	require.Equal(t, snapshot.AudioCopy3PlusChannels, got.Audio.CopyList[1].Codecs)

	mp4 := decide.AudioEncodeTarget(domain.ContainerMP4, 6)
	require.Equal(t, mp4.Codec, got.Audio.Mp4MultichannelCodec)
	require.Equal(t, mp4.Bitrate/mp4.Channels/1000, got.Audio.Mp4KbpsPerChannel)

	require.Equal(t, snapshot.SubtitleTextCodecs, got.Subtitles.TextCodecs)
	require.Equal(t, snapshot.SubtitleImageCodecs, got.Subtitles.DropImageCodecs)
	require.Equal(t, snapshot.SubtitleBroadcastCodecs, got.Subtitles.DropBroadcastCodecs)
	require.True(t, got.Subtitles.DropForcedImageSubtitles)

	require.Equal(t, ffmpeg.MP4Movflags(), got.Container.Mp4Movflags)
	require.ElementsMatch(t, []string{".mkv", ".mp4", ".m4v"}, got.Container.PreserveExtensions)

	for _, ext := range got.Container.LegacyExtensions {
		require.NotContains(t, got.Container.PreserveExtensions, ext)
		require.True(t, slices.Contains(ingest.VideoExtensions(), ext), ext)
	}

	require.Equal(t, job.SweepVideoCodec, got.SpaceSweep.SourceCodec)
	require.InDelta(t, job.SweepMinSavingPct, got.SpaceSweep.MinProjectedSavingPct, 0.001)
	require.Equal(t, job.SweepMinBitrate/1000, got.SpaceSweep.MinVideoBitrateKbps)
	require.True(t, got.SpaceSweep.ManualOnly)

	require.Equal(t, ingest.MinSizeBytes, got.Exclusions.MinSizeBytes)
	require.Equal(t, ingest.ExtrasDirs(), got.Exclusions.ExtrasDirectories)
	require.Equal(t, ingest.PartialSuffixes(), got.Exclusions.PartialExtensions)
	require.Equal(t, int(ingest.StabilityWindow.Seconds()), got.Exclusions.StabilityGuardSeconds)
}

func TestGetPolicy_BitrateTableMatchesTheFloorsAndCeilings(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	got := decodeInto[gen.Policy](t, h.do(t, "GET", "/api/policy", nil), 200)

	require.InDelta(t, ffmpeg.HardwareCorrection, got.Bitrate.SampleProbe.HardwareCorrection, 0.0001)
	require.Equal(t, ffmpeg.SampleCRF, got.Bitrate.SampleProbe.Crf)
	require.Equal(t, ffmpeg.SamplePreset, got.Bitrate.SampleProbe.Preset)
	require.Equal(t, ffmpeg.SampleSegmentCount, got.Bitrate.SampleProbe.Segments)
	require.InDelta(t, ffmpeg.SourceClamp, got.Bitrate.SampleProbe.SourceClamp, 0.0001)
	require.InDelta(t, ffmpeg.MaxrateFactor, got.Bitrate.MaxrateFactor, 0.0001)
	require.InDelta(t, ffmpeg.BufsizeFactor, got.Bitrate.BufsizeFactor, 0.0001)

	require.Len(t, got.Bitrate.Table, 5)

	for _, row := range got.Bitrate.Table {
		res := ffmpeg.Resolution(row.Resolution)
		require.Equal(t, ffmpeg.Floor(res)/1000, row.FloorKbps, row.Resolution)
		require.Equal(t, ffmpeg.Ceiling(res)/1000, row.CeilingKbps, row.Resolution)
		require.InDelta(t, ffmpeg.BPP(res), row.Bpp, 0.0001, row.Resolution)
	}
}

func TestGetVersion_CarriesThePolicyHash(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	got := decodeInto[gen.Version](t, h.do(t, "GET", "/api/version", nil), 200)

	require.Equal(t, "test", got.Version)
	require.Equal(t, "abc123", got.Commit)
	require.NotNil(t, got.PolicyHash)
	require.Equal(t, decide.PolicyHash(), *got.PolicyHash)
}

// The spec the TypeScript client is generated from is served by the same binary
// that implements it, so an operator can always read what this build answers.
func TestServeSpec_ReturnsTheOpenAPIDocument(t *testing.T) {
	t.Parallel()

	rec := newHarness(t).do(t, "GET", "/api/openapi.json", nil)

	require.Equal(t, 200, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var spec map[string]any
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &spec))
	require.Contains(t, spec, "openapi")
	require.Contains(t, spec, "paths")
}
