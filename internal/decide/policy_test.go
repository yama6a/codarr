package decide

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// currentPolicyHash pins the policy hash: changing it makes every processed file
// eligible again (plan.md 12). testdata/tagged_mp4.json carries the same value.
const currentPolicyHash = "914f0f87"

func TestPolicyHash_IsStable(t *testing.T) {
	t.Parallel()

	seen := map[string]struct{}{}
	for range 3 {
		seen[PolicyHash()] = struct{}{}
	}

	require.Len(t, seen, 1)
	require.Equal(t, hashPolicy(currentPolicy()), PolicyHash())
	require.Len(t, PolicyHash(), policyHashLength)
	require.Equal(t, currentPolicyHash, PolicyHash())
}

func TestPolicyHash_IsIndependentOfSetOrder(t *testing.T) {
	t.Parallel()

	p := currentPolicy()
	p.VideoCopyCodecs = []string{"hevc", "h264"}
	p.H264CopyProfiles = []string{"High", "Main", "Baseline", "Constrained Baseline"}
	p.SubtitleImageCodecs = []string{"dvb_subtitle", "dvd_subtitle", "hdmv_pgs_subtitle"}

	require.Equal(t, PolicyHash(), hashPolicy(p))
}

func TestPolicyHash_ChangesWithEveryConstant(t *testing.T) {
	t.Parallel()

	mutations := map[string]func(*policySnapshot){
		"video codec added":     func(p *policySnapshot) { p.VideoCopyCodecs = append(p.VideoCopyCodecs, "av1") },
		"video codec removed":   func(p *policySnapshot) { p.VideoCopyCodecs = []string{"h264"} },
		"h264 profile added":    func(p *policySnapshot) { p.H264CopyProfiles = append(p.H264CopyProfiles, "High 10") },
		"hevc profile removed":  func(p *policySnapshot) { p.HevcCopyProfiles = []string{"Main"} },
		"chroma":                func(p *policySnapshot) { p.CopyChroma = "4:2:2" },
		"level ceiling":         func(p *policySnapshot) { p.H264MaxLevel = 5.1 },
		"rewrite target":        func(p *policySnapshot) { p.LevelRewriteTarget = 4.1 },
		"rewrite width":         func(p *policySnapshot) { p.LevelRewriteMaxWidth = 3840 },
		"rewrite height":        func(p *policySnapshot) { p.LevelRewriteMaxHeight = 2160 },
		"rewrite fps":           func(p *policySnapshot) { p.LevelRewriteMaxFPS = 120 },
		"rewrite refs":          func(p *policySnapshot) { p.LevelRewriteMaxRefs = 5 },
		"encode codec":          func(p *policySnapshot) { p.VideoEncodeCodec = "av1" },
		"encode profile sdr":    func(p *policySnapshot) { p.VideoEncodeProfileSDR = "main10" },
		"encode profile hdr":    func(p *policySnapshot) { p.VideoEncodeProfileHDR = "main12" },
		"legacy scan codecs":    func(p *policySnapshot) { p.LegacyScanCodecs = []string{"mpeg2video"} },
		"stereo copy list":      func(p *policySnapshot) { p.AudioCopyUpTo2Channels = []string{"aac"} },
		"surround copy list":    func(p *policySnapshot) { p.AudioCopy3PlusChannels = append(p.AudioCopy3PlusChannels, "dts") },
		"audio target bitrate":  func(p *policySnapshot) { p.AudioTargets[0].Bitrate = 128_000 },
		"audio target codec":    func(p *policySnapshot) { p.AudioTargets[0].Codec = "opus" },
		"audio target channels": func(p *policySnapshot) { p.AudioTargets[4].Channels = 8 },
		"subtitle text list":    func(p *policySnapshot) { p.SubtitleTextCodecs = []string{"subrip"} },
		"subtitle image list":   func(p *policySnapshot) { p.SubtitleImageCodecs = []string{} },
		"subtitle broadcast":    func(p *policySnapshot) { p.SubtitleBroadcastCodecs = []string{"dvb_teletext"} },
		"subtitle target mkv":   func(p *policySnapshot) { p.SubtitleTargetMKV = "ass" },
		"subtitle target mp4":   func(p *policySnapshot) { p.SubtitleTargetMP4 = "subrip" },
		"container mapping": func(p *policySnapshot) {
			p.ContainerByExt = map[string]domain.Container{".mkv": domain.ContainerMatroska}
		},
		"container mapping flip": func(p *policySnapshot) {
			p.ContainerByExt = map[string]domain.Container{".mkv": domain.ContainerMP4, ".mp4": domain.ContainerMP4, ".m4v": domain.ContainerMP4}
		},
		"container default": func(p *policySnapshot) { p.ContainerDefault = domain.ContainerMP4 },
	}

	seen := map[string]string{PolicyHash(): "unmodified"}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := clonePolicy(currentPolicy())
			mutate(&p)

			got := hashPolicy(p)
			require.NotEqual(t, PolicyHash(), got, "changing %s must change the hash", name)
		})

		p := clonePolicy(currentPolicy())
		mutate(&p)
		got := hashPolicy(p)

		if other, ok := seen[got]; ok {
			t.Fatalf("%s hashes the same as %s", name, other)
		}

		seen[got] = name
	}
}

func clonePolicy(p policySnapshot) policySnapshot {
	p.VideoCopyCodecs = append([]string(nil), p.VideoCopyCodecs...)
	p.H264CopyProfiles = append([]string(nil), p.H264CopyProfiles...)
	p.HevcCopyProfiles = append([]string(nil), p.HevcCopyProfiles...)
	p.LegacyScanCodecs = append([]string(nil), p.LegacyScanCodecs...)
	p.AudioCopyUpTo2Channels = append([]string(nil), p.AudioCopyUpTo2Channels...)
	p.AudioCopy3PlusChannels = append([]string(nil), p.AudioCopy3PlusChannels...)
	p.AudioTargets = append([]audioTarget(nil), p.AudioTargets...)
	p.SubtitleTextCodecs = append([]string(nil), p.SubtitleTextCodecs...)
	p.SubtitleImageCodecs = append([]string(nil), p.SubtitleImageCodecs...)
	p.SubtitleBroadcastCodecs = append([]string(nil), p.SubtitleBroadcastCodecs...)

	return p
}

func TestAudioEncodeTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container domain.Container
		channels  int
		want      audioTarget
	}{
		{"mkv mono", domain.ContainerMatroska, 1, audioTarget{"aac", 96_000, 1}},
		{"mkv zero channels", domain.ContainerMatroska, 0, audioTarget{"aac", 96_000, 1}},
		{"mkv stereo", domain.ContainerMatroska, 2, audioTarget{"aac", 160_000, 2}},
		{"mkv 3.0", domain.ContainerMatroska, 3, audioTarget{"ac3", 640_000, 3}},
		{"mkv 5.1", domain.ContainerMatroska, 6, audioTarget{"ac3", 640_000, 6}},
		{"mkv 7.1", domain.ContainerMatroska, 8, audioTarget{"ac3", 640_000, 6}},
		{"mkv 9.1", domain.ContainerMatroska, 10, audioTarget{"ac3", 640_000, 6}},
		{"mp4 mono", domain.ContainerMP4, 1, audioTarget{"aac", 96_000, 1}},
		{"mp4 stereo", domain.ContainerMP4, 2, audioTarget{"aac", 160_000, 2}},
		{"mp4 5.1", domain.ContainerMP4, 6, audioTarget{"aac", 384_000, 6}},
		{"mp4 7.1", domain.ContainerMP4, 8, audioTarget{"aac", 384_000, 6}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, audioEncodeTarget(tc.container, tc.channels))
		})
	}
}

func TestAudioCopyList_SplitsAtThreeChannels(t *testing.T) {
	t.Parallel()

	require.Equal(t, audioCopyUpTo2Channels, audioCopyList(1))
	require.Equal(t, audioCopyUpTo2Channels, audioCopyList(2))
	require.Equal(t, audioCopy3PlusChannels, audioCopyList(3))
	require.Equal(t, audioCopy3PlusChannels, audioCopyList(8))
}
