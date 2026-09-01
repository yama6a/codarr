package ffmpeg_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
)

func TestResolutionOf_TiersOnWidth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		width, height int
		want          ffmpeg.Resolution
	}{
		{"pal dvd", 720, 576, ffmpeg.Res576p},
		{"ntsc dvd", 720, 480, ffmpeg.Res576p},
		{"720p", 1280, 720, ffmpeg.Res720p},
		{"720p scope, letterboxed height", 1280, 536, ffmpeg.Res720p},
		{"1080p", 1920, 1080, ffmpeg.Res1080p},
		{"1080p scope", 1920, 800, ffmpeg.Res1080p},
		{"1440p", 2560, 1440, ffmpeg.Res1440p},
		{"2160p", 3840, 2160, ffmpeg.Res2160p},
		{"2160p scope", 3840, 1600, ffmpeg.Res2160p},
		{"zero", 0, 0, ffmpeg.Res576p},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ffmpeg.ResolutionOf(tc.width, tc.height))
		})
	}
}

func TestFloorCeilingBPP_MatchTheTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		res            ffmpeg.Resolution
		floor, ceiling int
		bpp            float64
	}{
		{ffmpeg.Res576p, 800_000, 2_500_000, 0.065},
		{ffmpeg.Res720p, 1_500_000, 4_000_000, 0.065},
		{ffmpeg.Res1080p, 2_500_000, 8_000_000, 0.065},
		{ffmpeg.Res1440p, 4_000_000, 12_000_000, 0.060},
		{ffmpeg.Res2160p, 8_000_000, 20_000_000, 0.055},
		{ffmpeg.Resolution("unknown"), 800_000, 2_500_000, 0.065},
	}

	for _, tc := range cases {
		t.Run(string(tc.res), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.floor, ffmpeg.Floor(tc.res))
			require.Equal(t, tc.ceiling, ffmpeg.Ceiling(tc.res))
			require.InDelta(t, tc.bpp, ffmpeg.BPP(tc.res), 1e-9)
		})
	}
}

func TestClamp_AppliesTheThreeClampsInOrder(t *testing.T) {
	t.Parallel()

	hd := ffmpeg.BitrateInput{Width: 1920, Height: 1080}

	cases := []struct {
		name   string
		target int
		in     ffmpeg.BitrateInput
		want   int
	}{
		{
			name:   "untouched between floor and ceiling",
			target: 4_000_000,
			in:     hd,
			want:   4_000_000,
		},
		{
			name:   "source clamp at 85%",
			target: 4_000_000,
			in:     ffmpeg.BitrateInput{Width: 1920, Height: 1080, SourceBitrate: 4_000_000},
			want:   3_400_000,
		},
		{
			name:   "unresolved source bitrate skips the 85% clamp",
			target: 7_000_000,
			in:     hd,
			want:   7_000_000,
		},
		{
			name:   "floor wins over the source clamp",
			target: 4_000_000,
			in:     ffmpeg.BitrateInput{Width: 1920, Height: 1080, SourceBitrate: 1_000_000},
			want:   2_500_000,
		},
		{
			name:   "ceiling caps the result",
			target: 30_000_000,
			in:     hd,
			want:   8_000_000,
		},
		{
			name:   "floor lifts a tiny target",
			target: 100_000,
			in:     ffmpeg.BitrateInput{Width: 3840, Height: 2160},
			want:   8_000_000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ffmpeg.Clamp(tc.target, tc.in))
		})
	}
}

func TestTargetFromSamples_AppliesTheHardwareCorrection(t *testing.T) {
	t.Parallel()

	in := ffmpeg.BitrateInput{Width: 1920, Height: 1080}

	// Median of the three is 4 Mbps. Derived from the constant rather than
	// written out, so retuning it after a VMAF pass (8.1, 27) does not fail a
	// test that is about the multiplication, not the value.
	want := int(4_000_000 * ffmpeg.HardwareCorrection)
	require.Equal(t, want, ffmpeg.TargetFromSamples([]int{3_000_000, 4_000_000, 9_000_000}, in))
	require.Equal(t, 0, ffmpeg.TargetFromSamples(nil, in))
}

func TestMedian_OddAndEven(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, ffmpeg.Median(nil))
	require.Equal(t, 7, ffmpeg.Median([]int{7}))
	require.Equal(t, 4, ffmpeg.Median([]int{9, 4, 1}))
	require.Equal(t, 5, ffmpeg.Median([]int{8, 2, 4, 6}))
}

func TestFallbackBitrate_MatchesTheWorkedExamples(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    ffmpeg.BitrateInput
		want  int
		delta float64
	}{
		{
			name:  "720p at 23.976",
			in:    ffmpeg.BitrateInput{Width: 1280, Height: 720, FPS: 23.976},
			want:  1_400_000,
			delta: 50_000,
		},
		{
			name:  "1080p at 23.976",
			in:    ffmpeg.BitrateInput{Width: 1920, Height: 1080, FPS: 23.976},
			want:  3_200_000,
			delta: 50_000,
		},
		{
			name:  "2160p at 23.976",
			in:    ffmpeg.BitrateInput{Width: 3840, Height: 2160, FPS: 23.976},
			want:  10_900_000,
			delta: 50_000,
		},
		{
			name:  "1440p uses its own BPP",
			in:    ffmpeg.BitrateInput{Width: 2560, Height: 1440, FPS: 24},
			want:  5_308_416,
			delta: 1,
		},
		{
			name:  "60 fps scales by fps/24 on top of the linear term",
			in:    ffmpeg.BitrateInput{Width: 1920, Height: 1080, FPS: 60},
			want:  12_939_264,
			delta: 1,
		},
		{
			name:  "50 fps scales by fps/24, uncapped at 2.08 becomes 1.6",
			in:    ffmpeg.BitrateInput{Width: 1920, Height: 1080, FPS: 50},
			want:  10_782_720,
			delta: 1,
		},
		{
			name:  "30 fps is not high frame rate",
			in:    ffmpeg.BitrateInput{Width: 1920, Height: 1080, FPS: 30},
			want:  4_043_520,
			delta: 1,
		},
		{
			name:  "HDR adds 25%",
			in:    ffmpeg.BitrateInput{Width: 1920, Height: 1080, FPS: 24, HDR: true},
			want:  4_043_520,
			delta: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.InDelta(t, tc.want, ffmpeg.FallbackBitrate(tc.in), tc.delta)
		})
	}
}

func TestTargetFromFallback_ClampsTheFormula(t *testing.T) {
	t.Parallel()

	// The raw formula gives 12.9 Mbps for 1080p60, well past the 8 Mbps ceiling.
	require.Equal(t, 8_000_000, ffmpeg.TargetFromFallback(ffmpeg.BitrateInput{
		Width: 1920, Height: 1080, FPS: 60,
	}))

	require.Equal(t, 800_000, ffmpeg.TargetFromFallback(ffmpeg.BitrateInput{
		Width: 720, Height: 576, FPS: 25,
	}))
}
