package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

const mp4Path = "/media/movies/Example (2019)/Example (2019).mp4"

func TestEngine_AudioCopyList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stream   ffprobe.Stream
		decision domain.Decision
		reason   string
		codec    string
		bitrate  int
		channels int
	}{
		{
			name:     "aac stereo",
			stream:   audio("aac", 2),
			decision: domain.DecisionCopy,
			reason:   "aac, stereo",
		},
		{
			name:     "ac3 stereo",
			stream:   audio("ac3", 2),
			decision: domain.DecisionCopy,
			reason:   "ac3, stereo",
		},
		{
			name:     "eac3 stereo",
			stream:   audio("eac3", 2),
			decision: domain.DecisionCopy,
			reason:   "eac3, stereo",
		},
		{
			name:     "mp3 stereo",
			stream:   audio("mp3", 2),
			decision: domain.DecisionCopy,
			reason:   "mp3, stereo",
		},
		{
			name:     "mp3 mono",
			stream:   audio("mp3", 1),
			decision: domain.DecisionCopy,
			reason:   "mp3, mono",
		},
		{
			name:     "aac 5.1",
			stream:   audio("aac", 6),
			decision: domain.DecisionCopy,
			reason:   "aac, 5.1",
		},
		{
			name:     "ac3 5.1",
			stream:   audio("ac3", 6),
			decision: domain.DecisionCopy,
			reason:   "ac3, 5.1",
		},
		{
			name:     "eac3 7.1 passes through untouched",
			stream:   audio("eac3", 8),
			decision: domain.DecisionCopy,
			reason:   "eac3, 7.1",
		},
		{
			name:     "aac 7.1 passes through untouched",
			stream:   audio("aac", 8),
			decision: domain.DecisionCopy,
			reason:   "aac, 7.1",
		},
		{
			name:     "mp3 is not on the 3+ channel list",
			stream:   audio("mp3", 6),
			decision: domain.DecisionEncode,
			reason:   "mp3 not in copy list for 3+ channels",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 6,
		},
		{
			name:     "dts 5.1",
			stream:   audio("dts", 6),
			decision: domain.DecisionEncode,
			reason:   "dts not in copy list for 3+ channels",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 6,
		},
		{
			name:     "dts-hd ma 7.1 downmixes",
			stream:   audio("dts", 8, withProfile("DTS-HD MA")),
			decision: domain.DecisionEncode,
			reason:   "dts not in copy list for 3+ channels, downmixed 7.1 to 5.1",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 6,
		},
		{
			name:     "truehd 7.1 downmixes",
			stream:   audio("truehd", 8),
			decision: domain.DecisionEncode,
			reason:   "truehd not in copy list for 3+ channels, downmixed 7.1 to 5.1",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 6,
		},
		{
			name:     "flac stereo",
			stream:   audio("flac", 2),
			decision: domain.DecisionEncode,
			reason:   "flac not in copy list for 1-2 channels",
			codec:    "aac",
			bitrate:  160_000,
			channels: 2,
		},
		{
			name:     "pcm mono",
			stream:   audio("pcm_s16le", 1),
			decision: domain.DecisionEncode,
			reason:   "pcm_s16le not in copy list for 1-2 channels",
			codec:    "aac",
			bitrate:  96_000,
			channels: 1,
		},
		{
			name:     "opus stereo",
			stream:   audio("opus", 2),
			decision: domain.DecisionEncode,
			reason:   "opus not in copy list for 1-2 channels",
			codec:    "aac",
			bitrate:  160_000,
			channels: 2,
		},
		{
			name:     "vorbis 5.1",
			stream:   audio("vorbis", 6),
			decision: domain.DecisionEncode,
			reason:   "vorbis not in copy list for 3+ channels",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 6,
		},
		{
			name:     "a 3 channel track stays 3 channel",
			stream:   audio("dts", 3),
			decision: domain.DecisionEncode,
			reason:   "dts not in copy list for 3+ channels",
			codec:    "ac3",
			bitrate:  640_000,
			channels: 3,
		},
		{
			name:     "a channel count ffprobe did not report",
			stream:   audio("dts", 0),
			decision: domain.DecisionEncode,
			reason:   "dts not in copy list for 1-2 channels",
			codec:    "aac",
			bitrate:  96_000,
			channels: 1,
		},
		{
			name:     "a codec ffprobe could not name",
			stream:   audio("", 2),
			decision: domain.DecisionEncode,
			reason:   "unknown not in copy list for 1-2 channels",
			codec:    "aac",
			bitrate:  160_000,
			channels: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := decide.New().Plan(mkv(video("h264", "High"), tc.stream), decide.Options{Path: mkvPath})
			require.NoError(t, err)

			got := a.Plan.Streams[1]
			require.Equal(t, domain.StreamPlan{
				Type:           domain.StreamAudio,
				SourceIndex:    0,
				OutputIndex:    intPtr(0),
				Decision:       tc.decision,
				Reason:         tc.reason,
				Language:       "eng",
				TargetCodec:    tc.codec,
				TargetBitrate:  tc.bitrate,
				TargetChannels: tc.channels,
				Default:        true,
			}, got)
		})
	}
}

func TestEngine_AudioTargetsInMP4(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stream   ffprobe.Stream
		codec    string
		bitrate  int
		channels int
	}{
		{"5.1 becomes 64k per channel aac", audio("dts", 6), "aac", 384_000, 6},
		{"7.1 downmixes and stays aac", audio("truehd", 8), "aac", 384_000, 6},
		{"3 channels", audio("dts", 3), "aac", 192_000, 3},
		{"stereo is unchanged by the container", audio("flac", 2), "aac", 160_000, 2},
		{"mono is unchanged by the container", audio("flac", 1), "aac", 96_000, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := decide.New().Plan(mp4(video("h264", "High"), tc.stream), decide.Options{Path: mp4Path})
			require.NoError(t, err)

			got := a.Plan.Streams[1]
			require.Equal(t, domain.DecisionEncode, got.Decision)
			require.Equal(t, tc.codec, got.TargetCodec)
			require.Equal(t, tc.bitrate, got.TargetBitrate)
			require.Equal(t, tc.channels, got.TargetChannels)
		})
	}
}

func TestEngine_AudioKeepsEveryTrackAndItsMetadata(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(
			video("h264", "High"),
			audio("dts", 6, withLang("eng"), withTitle("Surround 5.1")),
			audio("aac", 2, withLang("ger")),
			commentaryTrack(),
		),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)

	audioPlans := streamsOfType(a.Plan, domain.StreamAudio)
	require.Len(t, audioPlans, 3)
	require.Equal(t, "Surround 5.1", audioPlans[0].Title)
	require.Equal(t, "ger", audioPlans[1].Language)
	require.True(t, audioPlans[2].Comment)
	require.True(t, audioPlans[2].VisualImpaired)
	require.Equal(t, []int{0, 1, 2}, outputIndices(audioPlans))
}

func commentaryTrack() ffprobe.Stream {
	s := audio("ac3", 2, withLang("eng"), withTitle("Director commentary"))
	s.Disposition.Default = 0
	s.Disposition.Comment = 1
	s.Disposition.VisualImpaired = 1

	return s
}

func TestEngine_NeverProducesZeroAudioStreams(t *testing.T) {
	t.Parallel()

	_, err := decide.New().Plan(
		mkv(video("mpeg2video", "Main"), subtitle("subrip")),
		decide.Options{Path: mkvPath},
	)
	require.ErrorIs(t, err, decide.ErrNoAudioStreams)
}

func TestEngine_SilentFileThatNeedsNoWriteIsNotAnError(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(mkv(video("h264", "High")), decide.Options{Path: mkvPath})
	require.NoError(t, err)
	require.Equal(t, domain.KindSkip, a.Plan.Kind, "nothing is written, so no file gains zero audio")
}

func intPtr(i int) *int { return &i }

func streamsOfType(p domain.Plan, t domain.StreamType) []domain.StreamPlan {
	var out []domain.StreamPlan

	for _, s := range p.Streams {
		if s.Type == t {
			out = append(out, s)
		}
	}

	return out
}

func outputIndices(plans []domain.StreamPlan) []int {
	out := make([]int, 0, len(plans))

	for _, p := range plans {
		if p.OutputIndex != nil {
			out = append(out, *p.OutputIndex)
		}
	}

	return out
}
