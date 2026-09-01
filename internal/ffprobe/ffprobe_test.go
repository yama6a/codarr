package ffprobe_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffprobe"
)

// fakeBinary writes a shell script standing in for ffprobe. It records its argv
// and prints body, so the exec path is covered without a real ffprobe.
func fakeBinary(t *testing.T, body string, exitCode int) (path, argvFile string) {
	t.Helper()

	dir := t.TempDir()
	path = filepath.Join(dir, "ffprobe")
	argvFile = filepath.Join(dir, "argv")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n" + body + "\nexit " + itoa(exitCode) + "\n"

	//nolint:gosec // a stand-in binary has to be executable
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))

	return path, argvFile
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	return string(rune('0' + i))
}

func TestCLI_ProbeParsesOutput(t *testing.T) {
	t.Parallel()

	fixture, err := filepath.Abs(filepath.Join("testdata", "matroska_h264_dts.json"))
	require.NoError(t, err)

	bin, argvFile := fakeBinary(t, "cat "+fixture, 0)

	res, err := ffprobe.New(bin).Probe(t.Context(), "/media/movies/x.mkv")
	require.NoError(t, err)
	require.Equal(t, "matroska,webm", res.Format.FormatName)
	require.Len(t, res.Streams, 7)

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	require.Equal(t,
		"-v\nquiet\n-print_format\njson\n-show_format\n-show_streams\n-show_chapters\n/media/movies/x.mkv\n",
		string(argv))
}

func TestArgs_IsTheDocumentedInvocation(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters",
		"/x.mkv",
	}, ffprobe.Args("/x.mkv"))
}

func TestCLI_ProbeFailsOnNonZeroExit(t *testing.T) {
	t.Parallel()

	bin, _ := fakeBinary(t, "echo 'x.mkv: Invalid data found when processing input' >&2", 1)

	_, err := ffprobe.New(bin).Probe(t.Context(), "/media/movies/x.mkv")
	require.ErrorIs(t, err, ffprobe.ErrProbeFailed)
	require.Contains(t, err.Error(), "Invalid data found")
	require.Contains(t, err.Error(), "/media/movies/x.mkv")
}

func TestCLI_ProbeFailsOnUnreadableOutput(t *testing.T) {
	t.Parallel()

	bin, _ := fakeBinary(t, "echo '<html>proxy error</html>'", 0)

	_, err := ffprobe.New(bin).Probe(t.Context(), "/media/movies/x.mkv")
	require.ErrorIs(t, err, ffprobe.ErrUnreadable)
	require.Contains(t, err.Error(), "/media/movies/x.mkv")
}

func TestCLI_ProbeFailsWhenBinaryIsMissing(t *testing.T) {
	t.Parallel()

	_, err := ffprobe.New(filepath.Join(t.TempDir(), "absent")).Probe(t.Context(), "/x.mkv")
	require.ErrorIs(t, err, ffprobe.ErrProbeFailed)
}

func TestCLI_ProbeHonoursContext(t *testing.T) {
	t.Parallel()

	bin, _ := fakeBinary(t, "sleep 30", 0)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ffprobe.New(bin).Probe(ctx, "/x.mkv")
	require.ErrorIs(t, err, ffprobe.ErrProbeFailed)
}

func TestCLI_LongStderrIsTailed(t *testing.T) {
	t.Parallel()

	bin, _ := fakeBinary(t, "i=0; while [ $i -lt 200 ]; do echo 'noise line that repeats' >&2; i=$((i+1)); done; echo LASTLINE >&2", 1)

	_, err := ffprobe.New(bin).Probe(t.Context(), "/x.mkv")
	require.ErrorIs(t, err, ffprobe.ErrProbeFailed)
	require.Contains(t, err.Error(), "LASTLINE")
	require.Less(t, len(err.Error()), 700)
}
