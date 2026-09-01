package decide_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func fixture(t *testing.T, name string) *ffprobe.Result {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	res, err := ffprobe.Parse(raw)
	require.NoError(t, err)

	return res
}

// TestEngine_PlansRealProbeJSON runs the whole path, from the bytes ffprobe
// prints to the plan, rather than from a hand-built Result.
func TestEngine_PlansRealProbeJSON(t *testing.T) {
	t.Parallel()

	probe := fixture(t, "audio_only_mkv.json")

	a, err := decide.New().Plan(probe, decide.Options{Path: probe.Format.Filename})
	require.NoError(t, err)

	require.Equal(t, domain.KindAudioOnly, a.Plan.Kind)
	require.Equal(t, domain.ContainerMatroska, a.Plan.OutputContainer)
	require.False(t, a.Plan.LevelRewrite)
	require.False(t, a.Plan.Deinterlace)
	require.False(t, a.Plan.HDR)
	require.False(t, a.Plan.DolbyVision)
	require.False(t, a.NeedsIdetSample)
	require.Equal(t, []string{
		"video: COPY - h264 High L4.0 8-bit 4:2:0 progressive",
		"audio 0 (eng, 5.1): ENCODE - dts not in copy list for 3+ channels",
		"audio 1 (ger, 2.0): COPY - aac, stereo",
		"subtitle 0 (eng, hdmv_pgs_subtitle): DROP - image-based, forced",
		"subtitle 1 (eng, subrip): COPY",
		"subtitle 2 (swe, ass): CONVERT - ass to srt",
		"container: matroska -> matroska",
		"plan: AUDIO_ONLY - video copied, 1 audio stream re-encoded, 1 subtitle stream converted, 1 subtitle stream dropped",
	}, a.Plan.Reasons)

	rec := decide.NewTransform(probe, a.Plan, 240)
	require.Equal(t, domain.BeforeAfterInt{Before: 1, After: 0}, rec.Attachments)
	require.Equal(t, domain.BeforeAfterInt{Before: 2, After: 2}, rec.Chapters)
	require.Equal(t, int64(9871234567), rec.Size.BeforeBytes)
	require.Equal(t, intPtr(8420), rec.Video.Before.BitrateKbps)
}

// TestEngine_SkipsItsOwnUnmodifiedOutput is the conjunction of plan.md 12 over
// a file Codarr wrote.
func TestEngine_SkipsItsOwnUnmodifiedOutput(t *testing.T) {
	t.Parallel()

	probe := fixture(t, "tagged_mp4.json")

	check := decide.New().CheckSkip(probe, "xxh3-128:abc", "xxh3-128:abc")
	require.Equal(t, decide.PolicyHash(), mustTag(t, probe, decide.TagPolicy),
		"the fixture is tagged with the policy this build applies")
	require.True(t, check.Skip)
	require.Equal(t, domain.ProvenanceCodarrOutput, check.Provenance)

	a, err := decide.New().Plan(probe, decide.Options{Path: probe.Format.Filename})
	require.NoError(t, err)
	require.Equal(t, domain.KindSkip, a.Plan.Kind, "the plan agrees with the tag")

	// The realistic third-party rewrite: same tags, different bytes.
	rewritten := decide.New().CheckSkip(probe, "xxh3-128:def", "xxh3-128:abc")
	require.False(t, rewritten.Skip)
	require.True(t, rewritten.Tagged)
	require.True(t, rewritten.PolicyMatches)
	require.False(t, rewritten.FingerprintMatches)
	require.Equal(t, domain.ProvenanceModified, rewritten.Provenance)
}

func mustTag(t *testing.T, probe *ffprobe.Result, name string) string {
	t.Helper()

	v, ok := probe.Format.Tag(name)
	require.True(t, ok)

	return v
}
