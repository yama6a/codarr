package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestContainerFor_PreservesMKVAndMP4(t *testing.T) {
	t.Parallel()

	tests := map[string]domain.Container{
		"/media/a.mkv":      domain.ContainerMatroska,
		"/media/a.MKV":      domain.ContainerMatroska,
		"/media/a.mp4":      domain.ContainerMP4,
		"/media/a.MP4":      domain.ContainerMP4,
		"/media/a.m4v":      domain.ContainerMP4,
		"/media/a.avi":      domain.ContainerMatroska,
		"/media/a.wmv":      domain.ContainerMatroska,
		"/media/a.ts":       domain.ContainerMatroska,
		"/media/a.m2ts":     domain.ContainerMatroska,
		"/media/a.vob":      domain.ContainerMatroska,
		"/media/a.mov":      domain.ContainerMatroska,
		"/media/a.flv":      domain.ContainerMatroska,
		"/media/a.ogm":      domain.ContainerMatroska,
		"/media/a.webm":     domain.ContainerMatroska,
		"/media/no-suffix":  domain.ContainerMatroska,
		"/media/a.mkv.part": domain.ContainerMatroska,
	}

	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, decide.ContainerFor(path))
		})
	}
}

func TestIsLegacyContainer(t *testing.T) {
	t.Parallel()

	require.False(t, decide.IsLegacyContainer("matroska"))
	require.False(t, decide.IsLegacyContainer("mp4"))
	require.True(t, decide.IsLegacyContainer("avi"))
	require.True(t, decide.IsLegacyContainer("mpeg"))
	require.True(t, decide.IsLegacyContainer("unknown"))
}

func TestHardwareDecodable(t *testing.T) {
	t.Parallel()

	for _, codec := range []string{"h264", "hevc", "mpeg2video", "vc1", "vp9"} {
		require.True(t, decide.HardwareDecodable(codec), codec)
	}

	for _, codec := range []string{"av1", "mpeg4", "wmv3", "vp8", ""} {
		require.False(t, decide.HardwareDecodable(codec), codec)
	}
}

func TestSubtitleEncoder_MapsSubripToSrt(t *testing.T) {
	t.Parallel()

	require.Equal(t, "srt", decide.SubtitleEncoder("subrip"))
	require.Equal(t, "mov_text", decide.SubtitleEncoder("mov_text"))
	require.Equal(t, "subrip", decide.SubtitleTargetForContainer(domain.ContainerMatroska))
	require.Equal(t, "mov_text", decide.SubtitleTargetForContainer(domain.ContainerMP4))
}

func TestVideoEncodeProfile_Main10ForHDR(t *testing.T) {
	t.Parallel()

	require.Equal(t, "main", decide.VideoEncodeProfile(false))
	require.Equal(t, "main10", decide.VideoEncodeProfile(true))
}

func TestEngine_KindDerivation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		probe  *ffprobe.Result
		path   string
		kind   domain.Kind
		source string
		output domain.Container
	}{
		{
			name:   "everything copies in an mkv",
			probe:  mkv(video("h264", "High"), audio("aac", 2), subtitle("subrip")),
			path:   mkvPath,
			kind:   domain.KindSkip,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "everything copies in an mp4",
			probe:  mp4(video("h264", "High"), audio("aac", 2), subtitle("mov_text")),
			path:   mp4Path,
			kind:   domain.KindSkip,
			source: "mp4",
			output: domain.ContainerMP4,
		},
		{
			name:   "everything copies in an avi",
			probe:  probeOf("avi", video("h264", "High"), audio("ac3", 2)),
			path:   "/media/x.avi",
			kind:   domain.KindRemux,
			source: "avi",
			output: domain.ContainerMatroska,
		},
		{
			name:   "a mov is a legacy container",
			probe:  mp4(video("h264", "High"), audio("aac", 2)),
			path:   "/media/x.mov",
			kind:   domain.KindRemux,
			source: "mp4",
			output: domain.ContainerMatroska,
		},
		{
			name:   "an mpeg-ts capture",
			probe:  probeOf("mpegts", video("h264", "High"), audio("ac3", 6)),
			path:   "/media/x.ts",
			kind:   domain.KindRemux,
			source: "mpegts",
			output: domain.ContainerMatroska,
		},
		{
			name:   "audio work alone",
			probe:  mkv(video("h264", "High"), audio("dts", 6)),
			path:   mkvPath,
			kind:   domain.KindAudioOnly,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "subtitle work alone",
			probe:  mkv(video("h264", "High"), audio("aac", 2), subtitle("ass")),
			path:   mkvPath,
			kind:   domain.KindAudioOnly,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "a dropped subtitle alone",
			probe:  mkv(video("h264", "High"), audio("aac", 2), subtitle("hdmv_pgs_subtitle")),
			path:   mkvPath,
			kind:   domain.KindAudioOnly,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "video work makes it full whatever else is true",
			probe:  mkv(video("av1", "Main"), audio("aac", 2)),
			path:   mkvPath,
			kind:   domain.KindFull,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "a legacy container that also needs an encode is full",
			probe:  probeOf("mpeg", video("mpeg2video", "Main", withFieldOrder("tt")), audio("ac3", 6)),
			path:   "/media/x.vob",
			kind:   domain.KindFull,
			source: "mpeg",
			output: domain.ContainerMatroska,
		},
		{
			name:   "an mp4 that needs subtitle conversion",
			probe:  mp4(video("h264", "High"), audio("aac", 2), subtitle("subrip")),
			path:   mp4Path,
			kind:   domain.KindAudioOnly,
			source: "mp4",
			output: domain.ContainerMP4,
		},
		{
			name:   "a webm keeps its extension only when nothing needs doing",
			probe:  probeOf("matroska,webm", video("vp9", "Profile 0"), audio("opus", 2)),
			path:   "/media/x.webm",
			kind:   domain.KindFull,
			source: "matroska",
			output: domain.ContainerMatroska,
		},
		{
			name:   "a format ffprobe could not name",
			probe:  probeOf("", video("h264", "High"), audio("aac", 2)),
			path:   "/media/x.bin",
			kind:   domain.KindRemux,
			source: "unknown",
			output: domain.ContainerMatroska,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := decide.New().Plan(tc.probe, decide.Options{Path: tc.path})
			require.NoError(t, err)
			require.Equal(t, tc.kind, a.Plan.Kind)
			require.Equal(t, tc.source, a.Plan.SourceContainer)
			require.Equal(t, tc.output, a.Plan.OutputContainer)
			require.Equal(t, tc.kind != domain.KindSkip, a.Plan.NeedsWrite())
		})
	}
}

func TestEngine_PathFallsBackToTheProbeFilename(t *testing.T) {
	t.Parallel()

	probe := mkv(video("h264", "High"), audio("aac", 2))
	probe.Format.Filename = "/media/movies/x.mp4"

	a, err := decide.New().Plan(probe, decide.Options{})
	require.NoError(t, err)
	require.Equal(t, domain.ContainerMP4, a.Plan.OutputContainer)
}

func TestEngine_PlanCarriesThePolicyHash(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(mkv(video("h264", "High"), audio("aac", 2)), decide.Options{Path: mkvPath})
	require.NoError(t, err)
	require.Equal(t, decide.PolicyHash(), a.Plan.PolicyHash)
}

// TestEngine_ReasonBlock pins the exact block plan.md 7 prints for its own
// example file.
func TestEngine_ReasonBlock(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(
			video("h264", "High"),
			audio("dts", 6, withLang("eng"), withTitle("Surround 5.1"), withProfile("DTS-HD MA")),
			audio("aac", 2, withLang("ger")),
			subtitle("subrip", withLang("eng")),
			subtitle("ass", withLang("eng")),
			subtitle("hdmv_pgs_subtitle", withLang("eng")),
		),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)

	require.Equal(t, []string{
		"video: COPY - h264 High L4.0 8-bit 4:2:0 progressive",
		"audio 0 (eng, 5.1): ENCODE - dts not in copy list for 3+ channels",
		"audio 1 (ger, 2.0): COPY - aac, stereo",
		"subtitle 0 (eng, subrip): COPY",
		"subtitle 1 (eng, ass): CONVERT - ass to srt",
		"subtitle 2 (eng, hdmv_pgs_subtitle): DROP - image-based",
		"container: matroska -> matroska",
		"plan: AUDIO_ONLY - video copied, 1 audio stream re-encoded, 1 subtitle stream converted, 1 subtitle stream dropped",
	}, a.Plan.Reasons)
}

func TestEngine_ReasonBlockVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		probe *ffprobe.Result
		path  string
		want  []string
	}{
		{
			name:  "skip",
			probe: mkv(video("h264", "High"), audio("aac", 2)),
			path:  mkvPath,
			want: []string{
				"video: COPY - h264 High L4.0 8-bit 4:2:0 progressive",
				"audio 0 (eng, 2.0): COPY - aac, stereo",
				"container: matroska -> matroska",
				"plan: SKIP - every stream is already compatible",
			},
		},
		{
			name:  "remux",
			probe: probeOf("avi", video("h264", "High"), audio("mp3", 2)),
			path:  "/media/x.avi",
			want: []string{
				"video: COPY - h264 High L4.0 8-bit 4:2:0 progressive",
				"audio 0 (eng, 2.0): COPY - mp3, stereo",
				"container: avi -> matroska",
				"plan: REMUX - video copied, container avi -> matroska",
			},
		},
		{
			name:  "level rewrite",
			probe: mkv(video("h264", "High", withLevel(51)), audio("aac", 2)),
			path:  mkvPath,
			want: []string{
				"video: COPY - level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)",
				"audio 0 (eng, 2.0): COPY - aac, stereo",
				"container: matroska -> matroska",
				"plan: REMUX - video copied with a level flag rewrite",
			},
		},
		{
			name:  "full with cover art",
			probe: mkv(video("mjpeg", "Baseline", withAttachedPic()), video("av1", "Main"), audio("opus", 2)),
			path:  mkvPath,
			want: []string{
				"video 0: DROP - attached picture (cover art)",
				"video: ENCODE - codec av1 is not on the copy list",
				"audio 0 (eng, 2.0): ENCODE - opus not in copy list for 1-2 channels",
				"container: matroska -> matroska",
				"plan: FULL - video re-encoded to HEVC MAIN, 1 audio stream re-encoded",
			},
		},
		{
			name:  "hdr full",
			probe: mkv(video("hevc", "Rext", withPixFmt("yuv422p10le"), withHDR()), audio("aac", 2)),
			path:  mkvPath,
			want: []string{
				`video: ENCODE - profile "Rext" is not on the copy list for hevc, chroma 4:2:2 is not 4:2:0`,
				"audio 0 (eng, 2.0): COPY - aac, stereo",
				"container: matroska -> matroska",
				"plan: FULL - video re-encoded to HEVC MAIN10",
			},
		},
		{
			name:  "two dropped subtitles",
			probe: mkv(video("h264", "High"), audio("aac", 2), subtitle("dvd_subtitle"), subtitle("eia_608")),
			path:  mkvPath,
			want: []string{
				"video: COPY - h264 High L4.0 8-bit 4:2:0 progressive",
				"audio 0 (eng, 2.0): COPY - aac, stereo",
				"subtitle 0 (eng, dvd_subtitle): DROP - image-based",
				"subtitle 1 (eng, eia_608): DROP - broadcast caption format",
				"container: matroska -> matroska",
				"plan: AUDIO_ONLY - video copied, 2 subtitle streams dropped",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := decide.New().Plan(tc.probe, decide.Options{Path: tc.path})
			require.NoError(t, err)
			require.Equal(t, tc.want, a.Plan.Reasons)
		})
	}
}

func TestEngine_AttachmentsAreNotStreamPlans(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(video("h264", "High"), audio("aac", 2), subtitle("ass"), attachment("OpenSans.ttf")),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)
	require.Len(t, a.Plan.Streams, 3)
	require.Equal(t, domain.KindAudioOnly, a.Plan.Kind)
}
