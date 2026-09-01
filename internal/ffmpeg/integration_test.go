package ffmpeg_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// TestRunner_RealRemux is the only test here that needs ffmpeg. It skips
// cleanly when the binary is absent, which is the normal case in CI.
func TestRunner_RealRemux(t *testing.T) {
	t.Parallel()

	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv")

	gen := exec.CommandContext(t.Context(), bin,
		"-nostdin", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest", src)
	require.NoError(t, gen.Run())

	out := filepath.Join(dir, "out.mkv")

	cmd, err := ffmpeg.Build(ffmpeg.Request{
		Source: ffmpeg.Source{Path: src, VideoCodec: "h264"},
		Output: out,
		Tags:   tags(),
		Plan: domain.Plan{
			Kind:            domain.KindRemux,
			OutputContainer: domain.ContainerMatroska,
			Streams: []domain.StreamPlan{
				videoCopy(),
				{Type: domain.StreamAudio, SourceIndex: 0, Decision: domain.DecisionCopy, Language: "eng", Default: true},
			},
		},
	})
	require.NoError(t, err)

	var seen int

	res, err := ffmpeg.NewRunner(bin, ffmpeg.DefaultGrace, 2*time.Second).
		Run(t.Context(), cmd.Args, func(ffmpeg.Progress) { seen++ })
	require.NoError(t, err)
	require.Positive(t, seen)
	require.Positive(t, res.FinalOutTime)

	info, err := os.Stat(out)
	require.NoError(t, err)
	require.Positive(t, info.Size())
}
