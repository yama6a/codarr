package ffmpeg_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Captured from `ffmpeg -vf idet -frames:v 500 -f null -` on a PAL DVD rip and
// on a progressive telecine, which are the two verdicts 6.2 turns on.
const (
	interlacedIdet = `[Parsed_idet_0 @ 0x55d1c8] Repeated Fields: Neither:   498 Top:     1 Bottom:     1
[Parsed_idet_0 @ 0x55d1c8] Single frame detection: TFF:   421 BFF:    12 Progressive:    45 Undetermined:    22
[Parsed_idet_0 @ 0x55d1c8] Multi frame detection: TFF:   455 BFF:     8 Progressive:    31 Undetermined:     6
frame=  500 fps=213 q=-0.0 Lsize=N/A time=00:00:20.83 bitrate=N/A speed=8.87x`

	progressiveIdet = `[Parsed_idet_0 @ 0x55d1c8] Repeated Fields: Neither:   350 Top:    75 Bottom:    75
[Parsed_idet_0 @ 0x55d1c8] Single frame detection: TFF:     3 BFF:     1 Progressive:   470 Undetermined:    26
[Parsed_idet_0 @ 0x55d1c8] Multi frame detection: TFF:     0 BFF:     0 Progressive:   494 Undetermined:     6
frame=  500 fps=220 q=-0.0 Lsize=N/A time=00:00:20.83 bitrate=N/A speed=9.17x`
)

func TestParseIdet_ReadsBothVerdicts(t *testing.T) {
	t.Parallel()

	require.Equal(t, domain.ScanInterlaced, ffmpeg.ParseIdet(interlacedIdet))
	require.Equal(t, domain.ScanProgressive, ffmpeg.ParseIdet(progressiveIdet))
}

func TestParseIdet_PrefersTheMultiFrameCountersOverTheSingleFrameOnes(t *testing.T) {
	t.Parallel()

	// Single-frame detection is the noisier of the two and disagrees here; the
	// multi-frame counters win, as the filter's own documentation recommends.
	mixed := `[Parsed_idet_0 @ 0x1] Single frame detection: TFF:   300 BFF:   100 Progressive:    80 Undetermined:    20
[Parsed_idet_0 @ 0x1] Multi frame detection: TFF:     4 BFF:     2 Progressive:   480 Undetermined:    14`

	require.Equal(t, domain.ScanProgressive, ffmpeg.ParseIdet(mixed))
}

func TestParseIdet_FallsBackToSingleFrameWhenMultiFrameIsAbsent(t *testing.T) {
	t.Parallel()

	only := `[Parsed_idet_0 @ 0x1] Single frame detection: TFF:   421 BFF:    12 Progressive:    45 Undetermined:    22`

	require.Equal(t, domain.ScanInterlaced, ffmpeg.ParseIdet(only))
}

// plan.md 6.2: an unknown scan type is progressive. Treating unreadable output
// as interlaced would deinterlace progressive content.
func TestParseIdet_UnreadableOutputIsProgressive(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"",
		"ffmpeg version 7.1.4\nUnknown filter 'idet'",
		"[Parsed_idet_0 @ 0x1] Multi frame detection: TFF: n/a BFF: n/a Progressive: n/a Undetermined: n/a",
	} {
		require.Equal(t, domain.ScanProgressive, ffmpeg.ParseIdet(in), in)
	}
}

func TestIdetArgs_DecodesAShortSampleAndWritesNothing(t *testing.T) {
	t.Parallel()

	args := ffmpeg.IdetArgs("/media/movies/Example.vob")
	joined := strings.Join(args, " ")

	require.Contains(t, joined, "-nostdin")
	require.Contains(t, joined, "-i /media/movies/Example.vob")
	require.Contains(t, joined, "-vf idet")
	require.Contains(t, joined, "-frames:v 500")
	require.Contains(t, joined, "-f null -")
	require.Equal(t, 500, ffmpeg.IdetFrames)
}
