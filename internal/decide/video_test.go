package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

const mkvPath = "/media/movies/Example (2019)/Example (2019).mkv"

// planOf runs the engine over one video stream plus a copyable stereo AAC
// track, which keeps every case in the table about the video decision.
func planOf(t *testing.T, v ffprobe.Stream) decide.Analysis {
	t.Helper()

	a, err := decide.New().Plan(mkv(v, audio("aac", 2)), decide.Options{Path: mkvPath})
	require.NoError(t, err)

	return a
}

func TestEngine_VideoCopyTest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stream   ffprobe.Stream
		decision domain.Decision
		reason   string
		kind     domain.Kind
	}{
		{
			name:     "h264 high",
			stream:   video("h264", "High"),
			decision: domain.DecisionCopy,
			reason:   "h264 High L4.0 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 constrained baseline is its own profile string",
			stream:   video("h264", "Constrained Baseline", withLevel(31)),
			decision: domain.DecisionCopy,
			reason:   "h264 Constrained Baseline L3.1 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 baseline",
			stream:   video("h264", "Baseline", withLevel(30)),
			decision: domain.DecisionCopy,
			reason:   "h264 Baseline L3.0 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 main",
			stream:   video("h264", "Main"),
			decision: domain.DecisionCopy,
			reason:   "h264 Main L4.0 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 high 10 is 10-bit and off the list",
			stream:   video("h264", "High 10", withPixFmt("yuv420p10le")),
			decision: domain.DecisionEncode,
			reason:   `profile "High 10" is not on the copy list for h264`,
			kind:     domain.KindFull,
		},
		{
			name:     "h264 high 4:2:2 fails profile and chroma",
			stream:   video("h264", "High 4:2:2", withPixFmt("yuv422p10le")),
			decision: domain.DecisionEncode,
			reason:   `profile "High 4:2:2" is not on the copy list for h264, chroma 4:2:2 is not 4:2:0`,
			kind:     domain.KindFull,
		},
		{
			name:     "unknown profile is default deny",
			stream:   video("h264", ""),
			decision: domain.DecisionEncode,
			reason:   "profile is unknown",
			kind:     domain.KindFull,
		},
		{
			name:     "hevc main",
			stream:   video("hevc", "Main"),
			decision: domain.DecisionCopy,
			reason:   "hevc Main L4.0 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "hevc main 10 in yuv420p10le",
			stream:   video("hevc", "Main 10", withPixFmt("yuv420p10le")),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L4.0 10-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "hevc main 10 in p010le",
			stream:   video("hevc", "Main 10", withPixFmt("p010le")),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L4.0 10-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "hevc rext is off the list",
			stream:   video("hevc", "Rext", withPixFmt("yuv444p10le")),
			decision: domain.DecisionEncode,
			reason:   `profile "Rext" is not on the copy list for hevc, chroma 4:4:4 is not 4:2:0`,
			kind:     domain.KindFull,
		},
		{
			name:     "hevc has no level ceiling",
			stream:   video("hevc", "Main 10", withLevel(153), withPixFmt("yuv420p10le"), withSize(3840, 2160)),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L5.1 10-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 at the level ceiling",
			stream:   video("h264", "High", withLevel(42)),
			decision: domain.DecisionCopy,
			reason:   "h264 High L4.2 8-bit 4:2:0 progressive",
			kind:     domain.KindSkip,
		},
		{
			name:     "h264 without a level is default deny",
			stream:   video("h264", "High", withLevel(0)),
			decision: domain.DecisionEncode,
			reason:   "level is unknown",
			kind:     domain.KindFull,
		},
		{
			name:     "monochrome is not 4:2:0",
			stream:   video("h264", "High", withPixFmt("gray")),
			decision: domain.DecisionEncode,
			reason:   "chroma monochrome is not 4:2:0",
			kind:     domain.KindFull,
		},
		{
			name:     "unknown pixel format is not 4:2:0",
			stream:   video("h264", "High", withPixFmt("")),
			decision: domain.DecisionEncode,
			reason:   "chroma unknown is not 4:2:0",
			kind:     domain.KindFull,
		},
		{
			name:     "mpeg2 dvd rip",
			stream:   video("mpeg2video", "Main", withSize(720, 576), withFieldOrder("progressive")),
			decision: domain.DecisionEncode,
			reason:   "codec mpeg2video is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "vc1 blu-ray",
			stream:   video("vc1", "Advanced", withFieldOrder("progressive")),
			decision: domain.DecisionEncode,
			reason:   "codec vc1 is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "av1 is re-encoded rather than added to the copy list",
			stream:   video("av1", "Main", withPixFmt("yuv420p10le")),
			decision: domain.DecisionEncode,
			reason:   "codec av1 is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "vp9",
			stream:   video("vp9", "Profile 0"),
			decision: domain.DecisionEncode,
			reason:   "codec vp9 is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "xvid",
			stream:   video("mpeg4", "Simple Profile"),
			decision: domain.DecisionEncode,
			reason:   "codec mpeg4 is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "wmv",
			stream:   video("wmv3", "Main"),
			decision: domain.DecisionEncode,
			reason:   "codec wmv3 is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "a codec ffprobe could not name",
			stream:   video("", ""),
			decision: domain.DecisionEncode,
			reason:   "codec unknown is not on the copy list",
			kind:     domain.KindFull,
		},
		{
			name:     "hdr hevc copies untouched",
			stream:   video("hevc", "Main 10", withPixFmt("yuv420p10le"), withHDR()),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L4.0 10-bit 4:2:0 progressive HDR",
			kind:     domain.KindSkip,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := planOf(t, tc.stream)
			v, ok := a.Plan.VideoStream()
			require.True(t, ok)
			require.Equal(t, tc.decision, v.Decision)
			require.Equal(t, tc.reason, v.Reason)
			require.Equal(t, tc.kind, a.Plan.Kind)
			require.False(t, a.Plan.LevelRewrite)
			require.False(t, a.Plan.Deinterlace)
		})
	}
}

func TestEngine_LevelRewriteCarveOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stream  ffprobe.Stream
		rewrite bool
		reason  string
		kind    domain.Kind
	}{
		{
			name:    "1080p level 5.1 with 3 refs",
			stream:  video("h264", "High", withLevel(51)),
			rewrite: true,
			reason:  "level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)",
			kind:    domain.KindRemux,
		},
		{
			name:    "level 5.0 rewrites too",
			stream:  video("h264", "High", withLevel(50), withRefs(4)),
			rewrite: true,
			reason:  "level 5.0 -> 4.2 flag rewrite (content fits 4.2, refs=4)",
			kind:    domain.KindRemux,
		},
		{
			name:    "1088 tall still fits",
			stream:  video("h264", "High", withLevel(51), withSize(1920, 1088)),
			rewrite: true,
			reason:  "level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)",
			kind:    domain.KindRemux,
		},
		{
			name:    "60 fps still fits",
			stream:  video("h264", "High", withLevel(51), withFPS("60/1")),
			rewrite: true,
			reason:  "level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)",
			kind:    domain.KindRemux,
		},
		{
			name:    "refs above 4 needs the higher level",
			stream:  video("h264", "High", withLevel(51), withRefs(5)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "refs unknown is not a claim that it fits",
			stream:  video("h264", "High", withLevel(51), withRefs(0)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "4k is genuinely above 4.2",
			stream:  video("h264", "High", withLevel(51), withSize(3840, 2160)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "too tall",
			stream:  video("h264", "High", withLevel(51), withSize(1920, 1200)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "too wide",
			stream:  video("h264", "High", withLevel(51), withSize(2048, 1080)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "above 60 fps",
			stream:  video("h264", "High", withLevel(51), withFPS("120/1")),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "no frame rate at all",
			stream:  video("h264", "High", withLevel(51), withFPS("0/0")),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "no dimensions at all",
			stream:  video("h264", "High", withLevel(51), withSize(0, 0)),
			rewrite: false,
			reason:  "level 5.1 is above 4.2",
			kind:    domain.KindFull,
		},
		{
			name:    "the rewrite is for level-only failures",
			stream:  video("h264", "High 10", withLevel(51), withPixFmt("yuv420p10le")),
			rewrite: false,
			reason:  `profile "High 10" is not on the copy list for h264, level 5.1 is above 4.2`,
			kind:    domain.KindFull,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := planOf(t, tc.stream)
			v, ok := a.Plan.VideoStream()
			require.True(t, ok)
			require.Equal(t, tc.rewrite, a.Plan.LevelRewrite)
			require.Equal(t, tc.reason, v.Reason)
			require.Equal(t, tc.kind, a.Plan.Kind)

			if tc.rewrite {
				require.Equal(t, domain.DecisionCopy, v.Decision, "a level rewrite is still a copy")
			} else {
				require.Equal(t, domain.DecisionEncode, v.Decision)
			}
		})
	}
}

func TestEngine_LevelRewriteNeverProducesFull(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(video("h264", "High", withLevel(51)), audio("dts", 6)),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)
	require.True(t, a.Plan.LevelRewrite)
	require.Equal(t, domain.KindAudioOnly, a.Plan.Kind)
}

func TestEngine_FieldOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stream      ffprobe.Stream
		idet        domain.Scan
		decision    domain.Decision
		deinterlace bool
		needsIdet   bool
	}{
		{
			name:     "h264 with no field_order is progressive",
			stream:   video("h264", "High"),
			decision: domain.DecisionCopy,
		},
		{
			name:     "h264 with unknown field_order is progressive",
			stream:   video("h264", "High", withFieldOrder("unknown")),
			decision: domain.DecisionCopy,
		},
		{
			name:     "h264 declared progressive",
			stream:   video("h264", "High", withFieldOrder("progressive")),
			decision: domain.DecisionCopy,
		},
		{
			name:        "top field first",
			stream:      video("h264", "High", withFieldOrder("tt")),
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:        "bottom field first",
			stream:      video("h264", "High", withFieldOrder("bb")),
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:        "tb",
			stream:      video("h264", "High", withFieldOrder("tb")),
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:        "bt",
			stream:      video("h264", "High", withFieldOrder("bt")),
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:      "mpeg2 saying nothing earns an idet sample",
			stream:    video("mpeg2video", "Main", withSize(720, 576)),
			decision:  domain.DecisionEncode,
			needsIdet: true,
		},
		{
			name:      "vc1 saying nothing earns an idet sample",
			stream:    video("vc1", "Advanced", withFieldOrder("unknown")),
			decision:  domain.DecisionEncode,
			needsIdet: true,
		},
		{
			name:     "mpeg2 that declared itself needs no sample",
			stream:   video("mpeg2video", "Main", withFieldOrder("progressive")),
			decision: domain.DecisionEncode,
		},
		{
			name:        "mpeg2 that declared itself interlaced needs no sample",
			stream:      video("mpeg2video", "Main", withFieldOrder("tt")),
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:     "av1 is not a legacy scan codec",
			stream:   video("av1", "Main"),
			decision: domain.DecisionEncode,
		},
		{
			name:        "an idet sample reporting interlaced settles it",
			stream:      video("mpeg2video", "Main"),
			idet:        domain.ScanInterlaced,
			decision:    domain.DecisionEncode,
			deinterlace: true,
		},
		{
			name:     "an idet sample reporting progressive settles it",
			stream:   video("mpeg2video", "Main"),
			idet:     domain.ScanProgressive,
			decision: domain.DecisionEncode,
		},
		{
			name:        "an idet answer beats an interlaced field_order",
			stream:      video("mpeg2video", "Main", withFieldOrder("tt")),
			idet:        domain.ScanProgressive,
			decision:    domain.DecisionEncode,
			deinterlace: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := decide.New().Plan(
				mkv(tc.stream, audio("aac", 2)),
				decide.Options{Path: mkvPath, IdetScan: tc.idet},
			)
			require.NoError(t, err)

			v, ok := a.Plan.VideoStream()
			require.True(t, ok)
			require.Equal(t, tc.decision, v.Decision)
			require.Equal(t, tc.deinterlace, a.Plan.Deinterlace)
			require.Equal(t, tc.needsIdet, a.NeedsIdetSample)
		})
	}
}

func TestEngine_InterlacedReasonNamesTheScan(t *testing.T) {
	t.Parallel()

	a := planOf(t, video("h264", "High", withFieldOrder("tt")))
	v, _ := a.Plan.VideoStream()
	require.Equal(t, "interlaced", v.Reason)
	require.Equal(t, domain.KindFull, a.Plan.Kind)
}

func TestEngine_DolbyVision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stream   ffprobe.Stream
		decision domain.Decision
		reason   string
		kind     domain.Kind
		profile  int
	}{
		{
			name:     "profile 5 is never re-encoded",
			stream:   video("hevc", "", withPixFmt("yuv420p10le"), withHDR(), withDolbyVision(5)),
			decision: domain.DecisionCopy,
			reason:   "Dolby Vision profile 5 is never re-encoded (profile is unknown)",
			kind:     domain.KindSkip,
			profile:  5,
		},
		{
			name:     "profile 5 that also fails chroma is still copied",
			stream:   video("hevc", "Main 10", withPixFmt("yuv422p10le"), withHDR(), withDolbyVision(5)),
			decision: domain.DecisionCopy,
			reason:   "Dolby Vision profile 5 is never re-encoded (chroma 4:2:2 is not 4:2:0)",
			kind:     domain.KindSkip,
			profile:  5,
		},
		{
			name:     "profile 5 that passes the copy test copies normally",
			stream:   video("hevc", "Main 10", withPixFmt("yuv420p10le"), withHDR(), withDolbyVision(5)),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L4.0 10-bit 4:2:0 progressive HDR, Dolby Vision profile 5",
			kind:     domain.KindSkip,
			profile:  5,
		},
		{
			name:     "profile 8 copies when it passes",
			stream:   video("hevc", "Main 10", withPixFmt("yuv420p10le"), withHDR(), withDolbyVision(8)),
			decision: domain.DecisionCopy,
			reason:   "hevc Main 10 L4.0 10-bit 4:2:0 progressive HDR, Dolby Vision profile 8",
			kind:     domain.KindSkip,
			profile:  8,
		},
		{
			name:     "profile 7 degrades to HDR10 on an encode",
			stream:   video("hevc", "Rext", withPixFmt("yuv422p10le"), withHDR(), withDolbyVision(7)),
			decision: domain.DecisionEncode,
			reason:   `profile "Rext" is not on the copy list for hevc, chroma 4:2:2 is not 4:2:0, Dolby Vision profile 7`,
			kind:     domain.KindFull,
			profile:  7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := planOf(t, tc.stream)
			v, ok := a.Plan.VideoStream()
			require.True(t, ok)
			require.Equal(t, tc.decision, v.Decision)
			require.Equal(t, tc.reason, v.Reason)
			require.Equal(t, tc.kind, a.Plan.Kind)
			require.True(t, a.Plan.DolbyVision)
			require.Equal(t, tc.profile, a.Plan.DolbyVisionProfile)
			require.True(t, a.Plan.HDR)
		})
	}
}

func TestEngine_DolbyVisionProfile5DowngradesToAudioOnly(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(
			video("hevc", "", withPixFmt("yuv420p10le"), withHDR(), withDolbyVision(5)),
			audio("truehd", 8),
		),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)
	require.Equal(t, domain.KindAudioOnly, a.Plan.Kind)

	v, _ := a.Plan.VideoStream()
	require.Equal(t, domain.DecisionCopy, v.Decision)
}

func TestEngine_AttachedPictureIsNeverTheVideoStream(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(
			video("mjpeg", "Baseline", withAttachedPic(), withPixFmt("yuvj420p"), withSize(600, 900)),
			video("h264", "High"),
			audio("aac", 2),
		),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)

	cover := a.Plan.Streams[0]
	require.Equal(t, domain.DecisionDrop, cover.Decision)
	require.Equal(t, "attached picture (cover art)", cover.Reason)
	require.Nil(t, cover.OutputIndex)

	primary, ok := a.Plan.VideoStream()
	require.True(t, ok)
	require.Equal(t, 1, primary.SourceIndex)
	require.Equal(t, domain.DecisionCopy, primary.Decision)
	require.Equal(t, 0, *primary.OutputIndex)

	require.Equal(t, domain.KindSkip, a.Plan.Kind,
		"cover art alone is not a reason to rewrite a compatible file")
}

func TestEngine_SecondaryVideoStreamDrops(t *testing.T) {
	t.Parallel()

	a, err := decide.New().Plan(
		mkv(video("h264", "High"), video("h264", "High"), audio("aac", 2)),
		decide.Options{Path: mkvPath},
	)
	require.NoError(t, err)
	require.Equal(t, domain.DecisionDrop, a.Plan.Streams[1].Decision)
	require.Equal(t, "secondary video stream", a.Plan.Streams[1].Reason)
}

func TestEngine_NoVideoStream(t *testing.T) {
	t.Parallel()

	tests := map[string]*ffprobe.Result{
		"audio only":            mkv(audio("aac", 2)),
		"cover art only":        mkv(video("mjpeg", "Baseline", withAttachedPic()), audio("aac", 2)),
		"nothing but subtitles": mkv(subtitle("subrip")),
	}

	for name, probe := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
			require.ErrorIs(t, err, decide.ErrNoVideoStream)
		})
	}
}

func TestEngine_NilProbe(t *testing.T) {
	t.Parallel()

	_, err := decide.New().Plan(nil, decide.Options{Path: mkvPath})
	require.ErrorIs(t, err, decide.ErrNoProbe)
}
