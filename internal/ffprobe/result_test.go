package ffprobe_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffprobe"
)

func load(t *testing.T, name string) *ffprobe.Result {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	res, err := ffprobe.Parse(raw)
	require.NoError(t, err)

	return res
}

func TestParse_ReadsFormatStreamsAndChapters(t *testing.T) {
	t.Parallel()

	res := load(t, "matroska_h264_dts.json")

	require.Equal(t, "matroska,webm", res.Format.FormatName)
	require.Equal(t, 7, res.Format.NBStreams)
	require.InDelta(t, 7200.0, res.Format.DurationSeconds(), 0.001)
	require.Equal(t, int64(9871234567), res.Format.SizeBytes())
	require.Equal(t, 10968038, res.Format.BitRateBPS())
	require.Len(t, res.Streams, 7)
	require.Len(t, res.Chapters, 2)
	require.Equal(t, "Chapter 02", res.Chapters[1].Tags["title"])
	require.Equal(t, raw(t, "matroska_h264_dts.json"), res.Raw)
}

func raw(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return b
}

func TestParse_RejectsGarbage(t *testing.T) {
	t.Parallel()

	_, err := ffprobe.Parse([]byte("not json"))
	require.ErrorIs(t, err, ffprobe.ErrUnreadable)
}

func TestParse_RejectsEmptyResult(t *testing.T) {
	t.Parallel()

	_, err := ffprobe.Parse([]byte(`{"streams":[],"format":{}}`))
	require.ErrorIs(t, err, ffprobe.ErrUnreadable)
}

func TestResult_PrimaryVideoSkipsAttachedPic(t *testing.T) {
	t.Parallel()

	res := load(t, "mp4_attached_pic.json")

	v, ok := res.PrimaryVideo()
	require.True(t, ok)
	require.Equal(t, 1, v.Index)
	require.Equal(t, "h264", v.CodecName)
	require.True(t, res.Streams[0].IsAttachedPic())
	require.False(t, v.IsAttachedPic())
}

func TestResult_PrimaryVideoAbsent(t *testing.T) {
	t.Parallel()

	res, err := ffprobe.Parse([]byte(`{"format":{"format_name":"matroska,webm"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"}]}`))
	require.NoError(t, err)

	_, ok := res.PrimaryVideo()
	require.False(t, ok)
}

func TestResult_StreamsOfType(t *testing.T) {
	t.Parallel()

	res := load(t, "matroska_h264_dts.json")

	require.Len(t, res.StreamsOfType(ffprobe.TypeVideo), 1)
	require.Len(t, res.StreamsOfType(ffprobe.TypeAudio), 2)
	require.Len(t, res.StreamsOfType(ffprobe.TypeSubtitle), 3)
	require.Len(t, res.StreamsOfType(ffprobe.TypeAttachment), 1)
	require.Empty(t, res.StreamsOfType(ffprobe.TypeData))
}

func TestResult_DurationFallsBackToStream(t *testing.T) {
	t.Parallel()

	res := load(t, "av1_no_level.json")

	require.Zero(t, res.Format.DurationSeconds())
	require.InDelta(t, 3600.0, res.Duration(), 0.001)
	require.InDelta(t, 7200.0, load(t, "matroska_h264_dts.json").Duration(), 0.001)
}

func TestResult_DurationUnknown(t *testing.T) {
	t.Parallel()

	res, err := ffprobe.Parse([]byte(`{"format":{"format_name":"avi"},"streams":[{"index":0,"codec_type":"video"}]}`))
	require.NoError(t, err)
	require.Zero(t, res.Duration())
}

func TestStream_FrameRatePrefersAverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    ffprobe.Stream
		want float64
	}{
		{"ntsc film", ffprobe.Stream{AvgFrameRate: "24000/1001", RFrameRate: "24000/1001"}, 23.976},
		{"pal", ffprobe.Stream{AvgFrameRate: "25/1", RFrameRate: "50/1"}, 25},
		{"avg unusable falls back", ffprobe.Stream{AvgFrameRate: "0/0", RFrameRate: "30000/1001"}, 29.97},
		{"both absent", ffprobe.Stream{}, 0},
		{"integer form", ffprobe.Stream{AvgFrameRate: "60"}, 60},
		{"zero denominator", ffprobe.Stream{AvgFrameRate: "60/0", RFrameRate: "24/1"}, 24},
		{"garbage", ffprobe.Stream{AvgFrameRate: "x/y", RFrameRate: "p"}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.InDelta(t, tc.want, tc.s.FrameRate(), 0.01)
		})
	}
}

func TestStream_LevelValueNormalisesPerCodec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		s       ffprobe.Stream
		want    float64
		ok      bool
		wantStr string
	}{
		{"h264 4.1", ffprobe.Stream{CodecName: "h264", Level: 41}, 4.1, true, "4.1"},
		{"h264 4.0", ffprobe.Stream{CodecName: "h264", Level: 40}, 4.0, true, "4.0"},
		{"h264 5.1", ffprobe.Stream{CodecName: "h264", Level: 51}, 5.1, true, "5.1"},
		{"hevc 5.1", ffprobe.Stream{CodecName: "hevc", Level: 153}, 5.1, true, "5.1"},
		{"hevc 4.0", ffprobe.Stream{CodecName: "hevc", Level: 120}, 4.0, true, "4.0"},
		{"mpeg2 has no level concept", ffprobe.Stream{CodecName: "mpeg2video", Level: 8}, 0, false, ""},
		{"absent", ffprobe.Stream{CodecName: "h264"}, 0, false, ""},
		{"ffprobe unknown sentinel", ffprobe.Stream{CodecName: "av1", Level: -99}, 0, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.s.LevelValue()
			require.Equal(t, tc.ok, ok)
			require.InDelta(t, tc.want, got, 0.0001)
			require.Equal(t, tc.wantStr, tc.s.LevelString())
		})
	}
}

func TestStream_InterlacedOnlyOnExplicitFieldOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fieldOrder string
		interlaced bool
		known      bool
	}{
		{"tt", true, true},
		{"bb", true, true},
		{"tb", true, true},
		{"bt", true, true},
		{"progressive", false, true},
		{"unknown", false, false},
		{"", false, false},
	}

	for _, tc := range tests {
		t.Run("field_order="+tc.fieldOrder, func(t *testing.T) {
			t.Parallel()

			s := ffprobe.Stream{FieldOrder: tc.fieldOrder}
			require.Equal(t, tc.interlaced, s.Interlaced())
			require.Equal(t, tc.known, s.FieldOrderKnown())
		})
	}
}

func TestStream_IsHDR(t *testing.T) {
	t.Parallel()

	require.True(t, load(t, "hevc_hdr10.json").Streams[0].IsHDR())
	require.True(t, ffprobe.Stream{ColorTransfer: "arib-std-b67"}.IsHDR())
	require.False(t, ffprobe.Stream{ColorTransfer: "bt709"}.IsHDR())
	require.False(t, ffprobe.Stream{}.IsHDR())
}

func TestStream_DolbyVisionProfile(t *testing.T) {
	t.Parallel()

	res := load(t, "dovi_profile5.json")

	p, ok := res.Streams[0].DolbyVisionProfile()
	require.True(t, ok)
	require.Equal(t, 5, p)

	_, ok = load(t, "hevc_hdr10.json").Streams[0].DolbyVisionProfile()
	require.False(t, ok)

	_, ok = ffprobe.Stream{SideDataList: []ffprobe.SideData{{Type: ffprobe.SideDataDOVI}}}.DolbyVisionProfile()
	require.False(t, ok, "a DOVI record without a profile number is not a claim of Dolby Vision")
}

func TestStream_TagsAreCaseInsensitive(t *testing.T) {
	t.Parallel()

	res := load(t, "mp4_attached_pic.json")

	v, ok := res.Format.Tag("codarr")
	require.True(t, ok)
	require.Equal(t, "1", v)

	v, ok = res.Format.Tag("CODARR_POLICY")
	require.True(t, ok)
	require.Equal(t, "a41f9c22", v)

	_, ok = res.Format.Tag("nope")
	require.False(t, ok)

	_, ok = ffprobe.Format{}.Tag("CODARR")
	require.False(t, ok)
}

func TestStream_LanguageAndTitle(t *testing.T) {
	t.Parallel()

	res := load(t, "matroska_h264_dts.json")

	require.Equal(t, "ger", res.Streams[2].Language())
	require.Equal(t, "Surround 5.1", res.Streams[1].Title())
	require.Equal(t, "und", res.Streams[0+6].Language(), "an attachment with no language tag")
	require.Empty(t, res.Streams[2].Title())
	require.Equal(t, "und", ffprobe.Stream{Tags: map[string]string{"language": ""}}.Language())
}

func TestStream_BitrateResolution(t *testing.T) {
	t.Parallel()

	res := load(t, "matroska_h264_dts.json")

	_, ok := res.Streams[0].BitRateBPS()
	require.False(t, ok, "Matroska rarely carries a per-stream bit_rate")

	bps, ok := res.Streams[0].BPSTagBPS()
	require.True(t, ok)
	require.Equal(t, 8420000, bps)

	br, ok := res.Streams[2].BitRateBPS()
	require.True(t, ok)
	require.Equal(t, 192000, br)

	_, ok = res.Streams[2].BPSTagBPS()
	require.False(t, ok)

	bps, ok = load(t, "hevc_hdr10.json").Streams[0].BPSTagBPS()
	require.True(t, ok)
	require.Equal(t, 48000000, bps, "the unsuffixed BPS spelling")

	_, ok = ffprobe.Stream{BitRate: "0"}.BitRateBPS()
	require.False(t, ok)

	_, ok = ffprobe.Stream{Tags: map[string]string{"BPS": "0", "BPS-eng": "0"}}.BPSTagBPS()
	require.False(t, ok)
}

func TestStream_ChromaAndBitDepthFromFixtures(t *testing.T) {
	t.Parallel()

	hdr := load(t, "hevc_hdr10.json").Streams[0]
	require.Equal(t, ffprobe.Chroma420, hdr.Chroma())
	require.Equal(t, 10, hdr.BitDepth())

	sdr := load(t, "matroska_h264_dts.json").Streams[0]
	require.Equal(t, ffprobe.Chroma420, sdr.Chroma())
	require.Equal(t, 8, sdr.BitDepth())
}
