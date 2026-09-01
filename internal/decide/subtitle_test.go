package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestEngine_SubtitlePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stream   ffprobe.Stream
		mp4      bool
		decision domain.Decision
		reason   string
		target   string
	}{
		{
			name:     "subrip into mkv copies",
			stream:   subtitle("subrip"),
			decision: domain.DecisionCopy,
		},
		{
			name:     "ass converts to srt",
			stream:   subtitle("ass"),
			decision: domain.DecisionConvert,
			reason:   "ass to srt",
			target:   "srt",
		},
		{
			name:     "ssa converts to srt",
			stream:   subtitle("ssa"),
			decision: domain.DecisionConvert,
			reason:   "ssa to srt",
			target:   "srt",
		},
		{
			name:     "webvtt converts to srt",
			stream:   subtitle("webvtt"),
			decision: domain.DecisionConvert,
			reason:   "webvtt to srt",
			target:   "srt",
		},
		{
			name:     "mov_text converts to srt in an mkv",
			stream:   subtitle("mov_text"),
			decision: domain.DecisionConvert,
			reason:   "mov_text to srt",
			target:   "srt",
		},
		{
			name:     "mov_text into mp4 copies",
			stream:   subtitle("mov_text"),
			mp4:      true,
			decision: domain.DecisionCopy,
		},
		{
			name:     "subrip converts into an mp4",
			stream:   subtitle("subrip"),
			mp4:      true,
			decision: domain.DecisionConvert,
			reason:   "subrip to mov_text",
			target:   "mov_text",
		},
		{
			name:     "ass converts to mov_text in an mp4",
			stream:   subtitle("ass"),
			mp4:      true,
			decision: domain.DecisionConvert,
			reason:   "ass to mov_text",
			target:   "mov_text",
		},
		{
			name:     "pgs drops",
			stream:   subtitle("hdmv_pgs_subtitle"),
			decision: domain.DecisionDrop,
			reason:   "image-based",
		},
		{
			name:     "forced pgs drops with the rest",
			stream:   subtitle("hdmv_pgs_subtitle", withForced()),
			decision: domain.DecisionDrop,
			reason:   "image-based, forced",
		},
		{
			name:     "vobsub drops",
			stream:   subtitle("dvd_subtitle"),
			decision: domain.DecisionDrop,
			reason:   "image-based",
		},
		{
			name:     "dvb subtitles drop",
			stream:   subtitle("dvb_subtitle"),
			decision: domain.DecisionDrop,
			reason:   "image-based",
		},
		{
			name:     "vobsub inside an mp4 drops too",
			stream:   subtitle("dvd_subtitle"),
			mp4:      true,
			decision: domain.DecisionDrop,
			reason:   "image-based",
		},
		{
			name:     "teletext drops",
			stream:   subtitle("dvb_teletext"),
			decision: domain.DecisionDrop,
			reason:   "broadcast caption format",
		},
		{
			name:     "eia 608 drops",
			stream:   subtitle("eia_608"),
			decision: domain.DecisionDrop,
			reason:   "broadcast caption format",
		},
		{
			name:     "an unrecognised format is default deny",
			stream:   subtitle("arib_caption"),
			decision: domain.DecisionDrop,
			reason:   "unsupported subtitle format arib_caption",
		},
		{
			name:     "a format ffprobe could not name",
			stream:   subtitle(""),
			decision: domain.DecisionDrop,
			reason:   "unsupported subtitle format unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := mkv(video("h264", "High"), audio("aac", 2), tc.stream)
			path := mkvPath

			if tc.mp4 {
				probe = mp4(video("h264", "High"), audio("aac", 2), tc.stream)
				path = mp4Path
			}

			a, err := decide.New().Plan(probe, decide.Options{Path: path})
			require.NoError(t, err)

			got := a.Plan.Streams[2]
			require.Equal(t, tc.decision, got.Decision)
			require.Equal(t, tc.reason, got.Reason)
			require.Equal(t, tc.target, got.TargetCodec)

			if tc.decision == domain.DecisionDrop {
				require.Nil(t, got.OutputIndex)
			} else {
				require.Equal(t, 0, *got.OutputIndex)
			}
		})
	}
}

func TestEngine_SubtitleOutputIndicesShiftPastDrops(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(
			video("h264", "High"),
			audio("aac", 2),
			subtitle("hdmv_pgs_subtitle", withForced()),
			subtitle("subrip"),
			subtitle("ass", withLang("swe")),
			subtitle("dvb_teletext"),
			subtitle("subrip", withLang("fin")),
		),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)

	subs := streamsOfType(a.Plan, domain.StreamSubtitle)
	require.Len(t, subs, 5)
	require.Nil(t, subs[0].OutputIndex)
	require.Equal(t, 0, *subs[1].OutputIndex)
	require.Equal(t, 1, *subs[2].OutputIndex)
	require.Nil(t, subs[3].OutputIndex)
	require.Equal(t, 2, *subs[4].OutputIndex)
	require.Equal(t, []int{0, 1, 2}, outputIndices(subs))
}

func TestEngine_SubtitleDispositionsAndLanguagesSurvive(t *testing.T) {
	t.Parallel()

	forced := subtitle("subrip", withLang("swe"), withForced())
	forced.Disposition.Default = 1

	a, err := decide.New().Plan(
		mkv(video("h264", "High"), audio("aac", 2), forced),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)

	got := a.Plan.Streams[2]
	require.Equal(t, domain.StreamPlan{
		Type:        domain.StreamSubtitle,
		SourceIndex: 0,
		OutputIndex: intPtr(0),
		Decision:    domain.DecisionCopy,
		Language:    "swe",
		Default:     true,
		Forced:      true,
	}, got)
}

func TestEngine_UntaggedSubtitleLanguageIsUnd(t *testing.T) {
	t.Parallel()

	s := subtitle("subrip")
	s.Tags = nil

	a, err := decide.New().Plan(
		mkv(video("h264", "High"), audio("aac", 2), s),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)
	require.Equal(t, "und", a.Plan.Streams[2].Language)
}
