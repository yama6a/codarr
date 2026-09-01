package ffmpeg_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
)

// shellRunner drives a shell instead of ffmpeg, so the process handling can be
// tested without one installed.
func shellRunner(t *testing.T, grace, duration time.Duration) *ffmpeg.Runner {
	t.Helper()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell available")
	}

	return ffmpeg.NewRunner(sh, grace, duration)
}

func TestRunner_ReportsProgressAndTheFinalOutTime(t *testing.T) {
	t.Parallel()

	script := `printf 'frame=10\nout_time_us=5000000\nprogress=continue\n'
printf 'frame=20\nout_time_us=10000000\nprogress=end\n'
printf 'a warning\nanother warning\n' >&2`

	var got []ffmpeg.Progress

	res, err := shellRunner(t, ffmpeg.DefaultGrace, 20*time.Second).
		Run(t.Context(), []string{"-c", script}, func(p ffmpeg.Progress) { got = append(got, p) })
	require.NoError(t, err)

	require.Equal(t, 10*time.Second, res.FinalOutTime)
	require.Equal(t, "a warning\nanother warning", res.StderrTail)
	require.Equal(t, []ffmpeg.Progress{
		{Frame: 10, OutTime: 5 * time.Second, Percent: 25},
		{Frame: 20, OutTime: 10 * time.Second, Percent: 50},
	}, got)
	require.Equal(t, []string{"-c", script}, res.Argv[1:])
}

func TestRunner_NonZeroExitCarriesTheStderrTail(t *testing.T) {
	t.Parallel()

	res, err := shellRunner(t, ffmpeg.DefaultGrace, time.Second).
		Run(t.Context(), []string{"-c", `printf 'Invalid data found\n' >&2; exit 3`}, nil)
	require.ErrorIs(t, err, ffmpeg.ErrFfmpegFailed)
	require.Contains(t, err.Error(), "Invalid data found")
	require.Equal(t, "Invalid data found", res.StderrTail)
}

func TestRunner_MissingBinary(t *testing.T) {
	t.Parallel()

	_, err := ffmpeg.NewRunner("/nonexistent/ffmpeg", ffmpeg.DefaultGrace, time.Second).
		Run(t.Context(), nil, nil)
	require.ErrorIs(t, err, ffmpeg.ErrFfmpegStart)
}

func TestRunner_CancellationTerminates(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := shellRunner(t, 100*time.Millisecond, time.Second).
			Run(ctx, []string{"-c", `printf 'started\n' >&2; sleep 30`}, nil)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled ffmpeg did not exit")
	}
}

func TestRunner_CancellationEscalatesToKill(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := shellRunner(t, 100*time.Millisecond, time.Second).
			Run(ctx, []string{"-c", `trap '' TERM; printf 'started\n' >&2; sleep 30`}, nil)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("a process ignoring SIGTERM was never killed")
	}
}
