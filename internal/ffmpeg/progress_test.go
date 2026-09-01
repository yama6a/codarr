package ffmpeg_test

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/clock"
)

func TestParseProgress_CapturedStream(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/progress.txt")
	require.NoError(t, err)

	defer func() { require.NoError(t, f.Close()) }()

	var got []ffmpeg.Progress

	final, err := ffmpeg.ParseProgress(f, 10*time.Minute, func(p ffmpeg.Progress) {
		got = append(got, p)
	})
	require.NoError(t, err)

	require.Equal(t, []ffmpeg.Progress{
		{Frame: 48},
		{
			Frame: 1440, FPS: 189.32, Speed: 7.89,
			OutTime: 60060 * time.Millisecond, TotalSize: 32468992, Percent: 10.01,
		},
		{
			Frame: 2880, FPS: 201.44, Speed: 8.4,
			OutTime: 120120 * time.Millisecond, TotalSize: 62914560, Percent: 20.02,
		},
	}, got)

	require.Equal(t, got[len(got)-1], final)
}

func TestParseProgress_IgnoresJunkAndKeepsLastGoodValue(t *testing.T) {
	t.Parallel()

	in := strings.Join([]string{
		"this line has no separator",
		"out_time_us=1000000",
		"frame=10",
		"progress=continue",
		"out_time_us=not-a-number",
		"frame=oops",
		"fps=oops",
		"speed=oops",
		"total_size=oops",
		"progress=end",
		"",
	}, "\n")

	final, err := ffmpeg.ParseProgress(strings.NewReader(in), 0, nil)
	require.NoError(t, err)
	require.Equal(t, ffmpeg.Progress{Frame: 10, OutTime: time.Second}, final)
}

func TestParseProgress_PercentCapsAtOneHundred(t *testing.T) {
	t.Parallel()

	final, err := ffmpeg.ParseProgress(
		strings.NewReader("out_time_us=20000000\nprogress=end\n"), 10*time.Second, nil)
	require.NoError(t, err)
	require.InDelta(t, 100.0, final.Percent, 1e-9)
}

func TestParseProgress_ReadErrorSurfaces(t *testing.T) {
	t.Parallel()

	want := errors.New("pipe broke")

	_, err := ffmpeg.ParseProgress(io.MultiReader(
		strings.NewReader("out_time_us=1\nprogress=continue\n"),
		errReader{want},
	), time.Second, nil)
	require.ErrorIs(t, err, want)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestThrottle_EmitsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	var got []int64

	th := ffmpeg.NewThrottle(fake, ffmpeg.FlushInterval, func(p ffmpeg.Progress) {
		got = append(got, p.Frame)
	})

	th.Update(ffmpeg.Progress{Frame: 1})

	fake.Advance(time.Second)
	th.Update(ffmpeg.Progress{Frame: 2})

	fake.Advance(3 * time.Second)
	th.Update(ffmpeg.Progress{Frame: 3})

	fake.Advance(time.Second)
	th.Update(ffmpeg.Progress{Frame: 4})

	fake.Advance(time.Second)
	th.Update(ffmpeg.Progress{Frame: 5})

	require.Equal(t, []int64{1, 4}, got)

	th.Flush()
	require.Equal(t, []int64{1, 4, 5}, got)

	th.Flush()
	require.Equal(t, []int64{1, 4, 5}, got)
}

func TestStderrRing_KeepsTheLastLines(t *testing.T) {
	t.Parallel()

	r := ffmpeg.NewStderrRing(3)

	_, err := io.WriteString(r, "one\r\ntwo\nthree\n")
	require.NoError(t, err)
	require.Equal(t, []string{"one", "two", "three"}, r.Lines())

	_, err = io.WriteString(r, "four\nfive\n")
	require.NoError(t, err)
	require.Equal(t, []string{"three", "four", "five"}, r.Lines())
	require.Equal(t, "three\nfour\nfive", r.Tail())

	_, err = io.WriteString(r, "partial")
	require.NoError(t, err)
	require.Equal(t, []string{"three", "four", "five", "partial"}, r.Lines())
}

func TestStderrRing_ZeroSizedDropsEverything(t *testing.T) {
	t.Parallel()

	r := ffmpeg.NewStderrRing(0)

	_, err := io.WriteString(r, "one\ntwo\n")
	require.NoError(t, err)
	require.Empty(t, r.Lines())
}
