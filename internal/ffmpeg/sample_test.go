package ffmpeg_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/ffmpeg/mock"
	"github.com/yama6a/codarr/internal/pkg/fsx"
)

type fakeSampleFS struct {
	mu      sync.Mutex
	sizes   map[string]int64
	statErr error
	removed []string
}

func (f *fakeSampleFS) Stat(path string) (fsx.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.statErr != nil {
		return fsx.FileInfo{}, f.statErr
	}

	return fsx.FileInfo{Size: f.sizes[path]}, nil
}

func (f *fakeSampleFS) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.removed = append(f.removed, path)

	return nil
}

func TestSampleSegments_MiddleNinetyPercent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		duration float64
		want     []ffmpeg.Segment
	}{
		{
			name:     "two hour film",
			duration: 7200,
			want: []ffmpeg.Segment{
				{Start: 1656, Duration: 60},
				{Start: 3600, Duration: 60},
				{Start: 5544, Duration: 60},
			},
		},
		{
			name:     "short file pulls the last window back inside",
			duration: 300,
			want: []ffmpeg.Segment{
				{Start: 69, Duration: 60},
				{Start: 150, Duration: 60},
				{Start: 231, Duration: 60},
			},
		},
		{
			name:     "very short file collapses to overlapping windows",
			duration: 61,
			want: []ffmpeg.Segment{
				{Start: 1, Duration: 60},
			},
		},
		{
			name:     "file shorter than one window becomes one whole-file sample",
			duration: 40,
			want:     []ffmpeg.Segment{{Start: 0, Duration: 40}},
		},
		{
			name:     "unknown duration yields nothing",
			duration: 0,
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ffmpeg.SampleSegments(tc.duration))
		})
	}
}

func TestSampleArgs_FixedQualityEncode(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-nostdin",
		"-ss", "1656.000",
		"-t", "60.000",
		"-i", "/library/film.mkv",
		"-an", "-sn",
		"-c:v", "libx265",
		"-crf", "21",
		"-preset", "veryfast",
		"-x265-params", "log-level=none",
		"/tmp/probe_0.mkv",
	}, ffmpeg.SampleArgs("/library/film.mkv", ffmpeg.Segment{Start: 1656, Duration: 60}, "/tmp/probe_0.mkv"))
}

func TestSampleBitrate_BitsOverSeconds(t *testing.T) {
	t.Parallel()

	require.Equal(t, 4_000_000, ffmpeg.SampleBitrate(30_000_000, 60))
	require.Equal(t, 0, ffmpeg.SampleBitrate(30_000_000, 0))
}

func TestSampleProbe_MedianOfThreeConcurrentSamples(t *testing.T) {
	t.Parallel()

	fs := &fakeSampleFS{sizes: map[string]int64{
		"/tmp/probe_0.mkv": 22_500_000,
		"/tmp/probe_1.mkv": 30_000_000,
		"/tmp/probe_2.mkv": 45_000_000,
	}}

	var (
		mu   sync.Mutex
		seen [][]string
	)

	enc := &mock.EncoderMock{
		RunFunc: func(_ context.Context, args []string, progress func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
			require.Nil(t, progress)

			mu.Lock()
			defer mu.Unlock()

			seen = append(seen, args)

			return ffmpeg.RunResult{}, nil
		},
	}

	base, err := ffmpeg.NewSampleProbe(enc, fs, "/tmp").Base(t.Context(), "/library/film.mkv", 7200)
	require.NoError(t, err)
	require.Equal(t, 4_000_000, base)
	require.Len(t, seen, 3)
	require.ElementsMatch(t,
		[]string{"/tmp/probe_0.mkv", "/tmp/probe_1.mkv", "/tmp/probe_2.mkv"},
		fs.removed)
}

func TestSampleProbe_Failures(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	t.Run("unknown duration", func(t *testing.T) {
		t.Parallel()

		_, err := ffmpeg.NewSampleProbe(&mock.EncoderMock{}, &fakeSampleFS{}, "/tmp").
			Base(t.Context(), "/library/film.mkv", 0)
		require.ErrorIs(t, err, ffmpeg.ErrSampleProbe)
	})

	t.Run("encode fails", func(t *testing.T) {
		t.Parallel()

		enc := &mock.EncoderMock{
			RunFunc: func(context.Context, []string, func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
				return ffmpeg.RunResult{}, boom
			},
		}

		_, err := ffmpeg.NewSampleProbe(enc, &fakeSampleFS{}, "/tmp").
			Base(t.Context(), "/library/film.mkv", 7200)
		require.ErrorIs(t, err, ffmpeg.ErrSampleProbe)
		require.ErrorIs(t, err, boom)
	})

	t.Run("stat fails", func(t *testing.T) {
		t.Parallel()

		enc := &mock.EncoderMock{
			RunFunc: func(context.Context, []string, func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
				return ffmpeg.RunResult{}, nil
			},
		}

		_, err := ffmpeg.NewSampleProbe(enc, &fakeSampleFS{statErr: boom}, "/tmp").
			Base(t.Context(), "/library/film.mkv", 7200)
		require.ErrorIs(t, err, ffmpeg.ErrSampleProbe)
		require.ErrorIs(t, err, boom)
	})
}
