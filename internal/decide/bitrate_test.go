package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestResolveVideoBitrate_WalksTheChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		probe  *ffprobe.Result
		want   int
		source domain.BitrateSource
	}{
		{
			name:   "the stream says so",
			probe:  mkv(video("h264", "High", withBitRate("8000000"), withBPSTag("9000000")), audio("aac", 2)),
			want:   8_000_000,
			source: domain.BitrateFromStream,
		},
		{
			name:   "the mkvmerge BPS tag",
			probe:  mkv(video("h264", "High", withBPSTag("8420000")), audio("aac", 2)),
			want:   8_420_000,
			source: domain.BitrateFromBPSTag,
		},
		{
			name:   "computed from size, duration and the known audio",
			probe:  mkv(video("h264", "High"), audio("aac", 2, withBitRate("192000"))),
			want:   9_608_000,
			source: domain.BitrateFromComputed,
		},
		{
			name:   "computed ignores audio it cannot measure",
			probe:  mkv(video("h264", "High"), audio("dts", 6)),
			want:   9_800_000,
			source: domain.BitrateFromComputed,
		},
		{
			name:   "computed reads the audio BPS tag too",
			probe:  mkv(video("h264", "High"), audio("dts", 6, withBPSTag("1509000"))),
			want:   8_291_000,
			source: domain.BitrateFromComputed,
		},
		{
			name:   "the format bitrate is the last resort",
			probe:  noDuration(mkv(video("h264", "High"), audio("aac", 2))),
			want:   10_000_000,
			source: domain.BitrateFromFormat,
		},
		{
			name:   "nothing to go on",
			probe:  bare(mkv(video("h264", "High"), audio("aac", 2))),
			source: domain.BitrateUnresolved,
		},
		{
			name:   "audio larger than the file allows for",
			probe:  audioHeavy(),
			source: domain.BitrateUnresolved,
		},
		{
			name:   "no video stream",
			probe:  mkv(audio("aac", 2)),
			source: domain.BitrateUnresolved,
		},
		{
			name:   "no probe",
			source: domain.BitrateUnresolved,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, src := decide.ResolveVideoBitrate(tc.probe)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.source, src)
		})
	}
}

func noDuration(r *ffprobe.Result) *ffprobe.Result {
	r.Format.Duration = ""

	return r
}

func bare(r *ffprobe.Result) *ffprobe.Result {
	r.Format.Duration = ""
	r.Format.Size = ""
	r.Format.BitRate = ""

	return r
}

// audioHeavy is a file whose audio alone accounts for more than its size, which
// is what a truncated or mis-tagged file looks like.
func audioHeavy() *ffprobe.Result {
	r := mkv(video("h264", "High"), audio("truehd", 8, withBitRate("40000000")))
	r.Format.BitRate = ""

	return r
}
