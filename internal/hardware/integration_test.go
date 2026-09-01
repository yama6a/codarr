package hardware_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/hardware"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
)

// The only test here that can tell working from compiled-in (plan.md 10.1); it needs a
// binary and a render node, so it skips outside the verification pod.
func TestProber_ProbeAgainstRealFfmpeg(t *testing.T) {
	t.Parallel()

	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}

	st := &recordingStore{}

	caps, err := hardware.New(hardware.NewCLI(bin), st, fsx.OS(), clock.System(),
		device, t.TempDir(), discard()).Probe(t.Context())
	require.NoError(t, err)

	require.NotEmpty(t, caps.FfmpegVersion)
	require.Len(t, caps.Entries, 6)
	require.Equal(t, caps.Entries, st.caps)

	t.Logf("encoder %s, remediation: %s", caps.Select(false).Encoder, caps.Remediation())
}

type recordingStore struct {
	caps []domain.HWCapability
}

func (s *recordingStore) ReplaceHWCapabilities(_ context.Context, caps []domain.HWCapability) error {
	s.caps = caps

	return nil
}

func (s *recordingStore) ListHWCapabilities(context.Context) ([]domain.HWCapability, error) {
	return s.caps, nil
}
