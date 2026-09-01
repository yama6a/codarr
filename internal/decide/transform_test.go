package decide_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

const ntscFilm = 24000.0 / 1001.0

func withChapters(r *ffprobe.Result, n int) *ffprobe.Result {
	for i := range n {
		r.Chapters = append(r.Chapters, ffprobe.Chapter{ID: int64(i)})
	}

	return r
}

// exampleFile is the file plan.md 17.2 renders a transform record for.
func exampleFile() *ffprobe.Result {
	r := mkv(
		video("h264", "High", withBPSTag("8420000")),
		audio("dts", 6, withProfile("DTS-HD MA"), withLang("eng"), withTitle("Surround 5.1"), withBPSTag("1509000")),
		audio("aac", 2, withLang("ger"), withBitRate("192000")),
		subtitle("hdmv_pgs_subtitle", withLang("eng"), withForced()),
		subtitle("subrip", withLang("eng")),
		subtitle("ass", withLang("swe")),
		attachment("a.ttf"), attachment("b.ttf"), attachment("c.ttf"),
	)
	r.Format.Size = "9871234567"

	return withChapters(r, 12)
}

func TestNewTransform_IsThePredictionAtEnqueue(t *testing.T) {
	t.Parallel()

	probe := exampleFile()

	a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
	require.NoError(t, err)

	got := decide.NewTransform(probe, a.Plan, 240)

	require.Equal(t, domain.TransformRecord{
		Container: domain.BeforeAfterString{Before: "matroska", After: "matroska"},
		Video: domain.VideoTransform{
			Action: domain.DecisionCopy,
			Reason: "h264 High L4.0 8-bit 4:2:0 progressive",
			Before: &domain.VideoState{
				Codec: "h264", Profile: "High", Level: "4.0",
				Width: 1920, Height: 1080, FPS: ntscFilm,
				BitrateKbps: intPtr(8420), PixFmt: "yuv420p",
				HDR: false, Scan: domain.ScanProgressive,
			},
			After: &domain.VideoState{
				Codec: "h264", Profile: "High", Level: "4.0",
				Width: 1920, Height: 1080, FPS: ntscFilm,
				BitrateKbps: intPtr(8420), PixFmt: "yuv420p",
				HDR: false, Scan: domain.ScanProgressive,
			},
		},
		Audio: []domain.AudioTransform{
			{
				SourceIndex: 0, OutputIndex: intPtr(0), Language: "eng",
				Title:  strPtr("Surround 5.1"),
				Action: domain.DecisionEncode,
				Reason: "dts not in copy list for 3+ channels",
				Before: &domain.AudioState{
					Codec: "dts", Profile: "DTS-HD MA", Channels: 6,
					Layout: "5.1", BitrateKbps: intPtr(1509),
				},
				After: &domain.AudioState{Codec: "ac3", Channels: 6, Layout: "5.1", BitrateKbps: intPtr(640)},
			},
			{
				SourceIndex: 1, OutputIndex: intPtr(1), Language: "ger",
				Action: domain.DecisionCopy,
				Reason: "aac, stereo",
				Before: &domain.AudioState{Codec: "aac", Channels: 2, Layout: "stereo", BitrateKbps: intPtr(192)},
				After:  &domain.AudioState{Codec: "aac", Channels: 2, Layout: "stereo", BitrateKbps: intPtr(192)},
			},
		},
		Subtitles: []domain.SubtitleTransform{
			{
				SourceIndex: 0, Language: "eng",
				Action: domain.DecisionDrop,
				Reason: "image-based, forced",
				Before: &domain.SubtitleState{Codec: "hdmv_pgs_subtitle", Forced: true},
			},
			{
				SourceIndex: 1, OutputIndex: intPtr(0), Language: "eng",
				Action: domain.DecisionCopy,
				Before: &domain.SubtitleState{Codec: "subrip"},
				After:  &domain.SubtitleState{Codec: "subrip"},
			},
			{
				SourceIndex: 2, OutputIndex: intPtr(1), Language: "swe",
				Action: domain.DecisionConvert,
				Reason: "ass to srt",
				Before: &domain.SubtitleState{Codec: "ass"},
				After:  &domain.SubtitleState{Codec: "subrip"},
			},
		},
		Attachments: domain.BeforeAfterInt{Before: 3, After: 0},
		Chapters:    domain.BeforeAfterInt{Before: 12, After: 12},
		Size:        domain.SizeTransform{BeforeBytes: 9871234567},
		Duration:    domain.DurationTransform{Estimated: 240},
	}, got)
}

func TestNewTransform_LevelRewriteRecordsTheNewLevel(t *testing.T) {
	t.Parallel()

	probe := mkv(video("h264", "High", withLevel(51)), audio("aac", 2))

	a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
	require.NoError(t, err)

	got := decide.NewTransform(probe, a.Plan, 60)
	require.Equal(t, domain.DecisionCopy, got.Video.Action)
	require.Equal(t, "level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)", got.Video.Reason)
	require.Equal(t, "5.1", got.Video.Before.Level)
	require.Equal(t, "4.2", got.Video.After.Level)
}

func TestNewTransform_FullJobLeavesTheBitrateUnknown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stream  ffprobe.Stream
		profile string
		pixFmt  string
		hdr     bool
	}{
		{"sdr", video("av1", "Main", withBitRate("6000000")), "Main", "yuv420p", false},
		{"hdr", video("av1", "Main", withPixFmt("yuv420p10le"), withHDR(), withBitRate("6000000")), "Main 10", "yuv420p10le", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := mkv(tc.stream, audio("aac", 2))

			a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
			require.NoError(t, err)

			got := decide.NewTransform(probe, a.Plan, 3600)
			require.Equal(t, domain.DecisionEncode, got.Video.Action)
			require.Equal(t, intPtr(6000), got.Video.Before.BitrateKbps)
			require.Equal(t, &domain.VideoState{
				Codec: "hevc", Profile: tc.profile, Level: "",
				Width: 1920, Height: 1080, FPS: ntscFilm,
				BitrateKbps: nil, PixFmt: tc.pixFmt,
				HDR: tc.hdr, Scan: domain.ScanProgressive,
			}, got.Video.After)
		})
	}
}

func TestNewTransform_DeinterlacedSourceRecordsBothScans(t *testing.T) {
	t.Parallel()

	probe := mkv(video("mpeg2video", "Main", withFieldOrder("tt")), audio("ac3", 6))

	a, err := decide.New().Plan(probe, decide.Options{Path: "/media/x.vob"})
	require.NoError(t, err)

	got := decide.NewTransform(probe, a.Plan, 900)
	require.Equal(t, domain.ScanInterlaced, got.Video.Before.Scan)
	require.Equal(t, domain.ScanProgressive, got.Video.After.Scan)
}

func TestNewTransform_IdetInterlacedIsRecordedOnTheBeforeState(t *testing.T) {
	t.Parallel()

	probe := mkv(video("mpeg2video", "Main"), audio("ac3", 6))

	a, err := decide.New().Plan(probe, decide.Options{Path: "/media/x.vob", IdetScan: domain.ScanInterlaced})
	require.NoError(t, err)

	got := decide.NewTransform(probe, a.Plan, 900)
	require.Equal(t, domain.ScanInterlaced, got.Video.Before.Scan,
		"the probe could not see it, but the sample could")
}

func TestNewTransform_NoVideoPlan(t *testing.T) {
	t.Parallel()

	probe := mkv(audio("aac", 2))
	got := decide.NewTransform(probe, domain.Plan{}, 10)
	require.Equal(t, domain.VideoTransform{}, got.Video)
}

func TestMergeMeasured_ReplacesEveryPrediction(t *testing.T) {
	t.Parallel()

	probe := exampleFile()

	a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
	require.NoError(t, err)

	rec := decide.NewTransform(probe, a.Plan, 240)

	out := withChapters(mkv(
		video("h264", "High", withBPSTag("8420000")),
		audio("ac3", 6, withLang("eng"), withBitRate("640000")),
		audio("aac", 2, withLang("ger"), withBitRate("192000")),
		subtitle("subrip", withLang("eng")),
		subtitle("subrip", withLang("swe")),
	), 12)
	out.Format.Size = "8102938475"

	got := decide.MergeMeasured(rec, out, 218)

	require.Equal(t, 218, *got.Duration.Actual)
	require.Equal(t, 240, got.Duration.Estimated)
	require.Equal(t, int64(8102938475), got.Size.AfterBytes)
	require.Equal(t, int64(9871234567), got.Size.BeforeBytes)
	require.Equal(t, domain.BeforeAfterInt{Before: 3, After: 0}, got.Attachments)
	require.Equal(t, domain.BeforeAfterInt{Before: 12, After: 12}, got.Chapters)
	require.Equal(t, "matroska", got.Container.After)

	require.Equal(t, &domain.AudioState{
		Codec: "ac3", Channels: 6, Layout: "5.1", BitrateKbps: intPtr(640),
	}, got.Audio[0].After)
	require.Equal(t, &domain.SubtitleState{Codec: "subrip"}, got.Subtitles[1].After)
	require.Equal(t, &domain.SubtitleState{Codec: "subrip"}, got.Subtitles[2].After)
	require.Nil(t, got.Subtitles[0].After, "a dropped track has no after state")
	require.Equal(t, intPtr(8420), got.Video.After.BitrateKbps)
	require.Nil(t, got.OutputIdentity, "promotion writes that, not the merge")
}

func TestMergeMeasured_HandlesAThinnerOutput(t *testing.T) {
	t.Parallel()

	probe := exampleFile()

	a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
	require.NoError(t, err)

	rec := decide.NewTransform(probe, a.Plan, 240)

	out := mkv(audio("ac3", 6))
	out.Format.Size = "0"

	got := decide.MergeMeasured(rec, out, 100)
	require.Equal(t, int64(0), got.Size.AfterBytes, "a size ffprobe could not read leaves the prediction alone")
	require.Equal(t, &domain.SubtitleState{Codec: "subrip"}, got.Subtitles[1].After,
		"a stream the output does not have keeps its prediction; verification is what catches that")
	require.Equal(t, rec.Video.After, got.Video.After, "no video in the output leaves the prediction alone")
}

func TestMergeMeasured_WithoutAProbeStillRecordsTheDuration(t *testing.T) {
	t.Parallel()

	probe := exampleFile()

	a, err := decide.New().Plan(probe, decide.Options{Path: mkvPath})
	require.NoError(t, err)

	rec := decide.NewTransform(probe, a.Plan, 240)
	got := decide.MergeMeasured(rec, nil, 300)

	require.Equal(t, 300, *got.Duration.Actual)
	require.Equal(t, rec.Video.After, got.Video.After)
}

func strPtr(s string) *string { return &s }
