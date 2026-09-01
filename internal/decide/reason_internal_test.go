package decide

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestChannelNotation(t *testing.T) {
	t.Parallel()

	tests := map[int]string{
		1: "1.0", 2: "2.0", 3: "2.1", 4: "4.0", 5: "4.1",
		6: "5.1", 7: "6.1", 8: "7.1", 10: "10ch", 0: "0ch",
	}

	for channels, want := range tests {
		require.Equal(t, want, channelNotation(channels))
	}
}

func TestLayoutName_FallsBackToTheChannelCount(t *testing.T) {
	t.Parallel()

	require.Equal(t, "5.1", layoutName(ffprobe.Stream{Channels: 6, ChannelLayout: "5.1(side)"}))
	require.Equal(t, "7.1", layoutName(ffprobe.Stream{Channels: 8, ChannelLayout: "7.1"}))
	require.Equal(t, "2.1", layoutName(ffprobe.Stream{Channels: 3}))
}

func TestLabel_UnknownStreamType(t *testing.T) {
	t.Parallel()

	probe := &ffprobe.Result{}
	require.Equal(t, "data", label(probe, domain.StreamPlan{Type: domain.StreamType("data")}))
}

func TestSourceStream_OutOfRangeIsEmpty(t *testing.T) {
	t.Parallel()

	probe := &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: ffprobe.TypeAudio, CodecName: "aac"}}}
	require.Equal(t, "aac", sourceStream(probe, ffprobe.TypeAudio, 0).CodecName)
	require.Empty(t, sourceStream(probe, ffprobe.TypeAudio, 1).CodecName)
	require.Empty(t, sourceStream(probe, ffprobe.TypeAudio, -1).CodecName)
}
