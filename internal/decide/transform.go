package decide

import (
	"strconv"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Predicted output pixel formats, as ffprobe will report them back.
const (
	encodedPixFmtSDR = "yuv420p"
	encodedPixFmtHDR = "yuv420p10le"
)

// ffprobe's spelling of the HEVC profiles the encode targets, which is not the
// spelling ffmpeg's -profile:v takes (see VideoEncodeProfile).
const (
	encodedProfileSDR = "Main"
	encodedProfileHDR = "Main 10"
)

// NewTransform builds the enqueue-time record of plan.md 17.2, where "before" is
// measured and "after" is the plan's prediction until MergeMeasured runs.
func NewTransform(probe *ffprobe.Result, p domain.Plan, estimatedSeconds int) domain.TransformRecord {
	rec := domain.TransformRecord{
		Container: domain.BeforeAfterString{Before: p.SourceContainer, After: string(p.OutputContainer)},
		Video:     videoTransform(probe, p),
		Attachments: domain.BeforeAfterInt{
			Before: len(probe.StreamsOfType(ffprobe.TypeAttachment)),
			After:  0,
		},
		Chapters: domain.BeforeAfterInt{Before: len(probe.Chapters), After: len(probe.Chapters)},
		Size:     domain.SizeTransform{BeforeBytes: probe.Format.SizeBytes()},
		Duration: domain.DurationTransform{Estimated: estimatedSeconds},
	}

	for _, s := range p.Streams {
		switch s.Type {
		case domain.StreamAudio:
			rec.Audio = append(rec.Audio, audioTransform(probe, s))
		case domain.StreamSubtitle:
			rec.Subtitles = append(rec.Subtitles, subtitleTransform(probe, s, p.OutputContainer))
		case domain.StreamVideo:
		}
	}

	return rec
}

func videoTransform(probe *ffprobe.Result, p domain.Plan) domain.VideoTransform {
	plan, ok := p.VideoStream()
	if !ok {
		return domain.VideoTransform{}
	}

	src := sourceStream(probe, ffprobe.TypeVideo, plan.SourceIndex)
	bps, _ := ResolveVideoBitrate(probe)

	// Deinterlace is set when the engine concluded interlaced, including from
	// an idet sample the probe itself could not see.
	scan := scanOf(src)
	if p.Deinterlace {
		scan = domain.ScanInterlaced
	}

	before := videoState(src, scan)

	if bps > 0 {
		kbps := bps / 1000
		before.BitrateKbps = &kbps
	}

	return domain.VideoTransform{
		Action: plan.Decision,
		Reason: plan.Reason,
		Before: &before,
		After:  predictedVideoState(before, p),
	}
}

func scanOf(s ffprobe.Stream) domain.Scan {
	if s.Interlaced() {
		return domain.ScanInterlaced
	}

	return domain.ScanProgressive
}

func videoState(s ffprobe.Stream, scan domain.Scan) domain.VideoState {
	return domain.VideoState{
		Codec:       s.CodecName,
		Profile:     s.Profile,
		Level:       s.LevelString(),
		Width:       s.Width,
		Height:      s.Height,
		FPS:         s.FrameRate(),
		BitrateKbps: streamBitrateKbps(s),
		PixFmt:      s.PixFmt,
		HDR:         s.IsHDR(),
		Scan:        scan,
	}
}

// predictedVideoState is the enqueue-time "after". A full job leaves the
// bitrate nil because the sample probe of plan.md 8.1 has not run yet.
func predictedVideoState(before domain.VideoState, p domain.Plan) *domain.VideoState {
	after := before
	if p.Deinterlace {
		after.Scan = domain.ScanProgressive
	}

	plan, _ := p.VideoStream()
	if plan.Decision != domain.DecisionEncode {
		if p.LevelRewrite {
			after.Level = strconv.FormatFloat(levelRewriteTarget, 'f', 1, 64)
		}

		return &after
	}

	after.Codec = videoEncodeCodec
	after.Level = ""
	after.BitrateKbps = nil

	if p.HDR {
		after.Profile = encodedProfileHDR
		after.PixFmt = encodedPixFmtHDR
	} else {
		after.Profile = encodedProfileSDR
		after.PixFmt = encodedPixFmtSDR
	}

	return &after
}

func audioTransform(probe *ffprobe.Result, plan domain.StreamPlan) domain.AudioTransform {
	src := sourceStream(probe, ffprobe.TypeAudio, plan.SourceIndex)
	before := domain.AudioState{
		Codec:       src.CodecName,
		Profile:     src.Profile,
		Channels:    src.Channels,
		Layout:      layoutName(src),
		BitrateKbps: streamBitrateKbps(src),
	}

	t := domain.AudioTransform{
		SourceIndex: plan.SourceIndex,
		OutputIndex: plan.OutputIndex,
		Language:    plan.Language,
		Title:       titlePtr(plan.Title),
		Action:      plan.Decision,
		Reason:      plan.Reason,
		Before:      &before,
	}

	switch plan.Decision {
	case domain.DecisionDrop:
	case domain.DecisionEncode, domain.DecisionConvert:
		kbps := plan.TargetBitrate / 1000
		t.After = &domain.AudioState{
			Codec:       plan.TargetCodec,
			Channels:    plan.TargetChannels,
			Layout:      channelNotation(plan.TargetChannels),
			BitrateKbps: &kbps,
		}
	case domain.DecisionCopy:
		after := before
		t.After = &after
	}

	return t
}

func subtitleTransform(probe *ffprobe.Result, plan domain.StreamPlan, container domain.Container) domain.SubtitleTransform {
	src := sourceStream(probe, ffprobe.TypeSubtitle, plan.SourceIndex)
	before := domain.SubtitleState{Codec: src.CodecName, Forced: plan.Forced}

	t := domain.SubtitleTransform{
		SourceIndex: plan.SourceIndex,
		OutputIndex: plan.OutputIndex,
		Language:    plan.Language,
		Action:      plan.Decision,
		Reason:      plan.Reason,
		Before:      &before,
	}

	switch plan.Decision {
	case domain.DecisionDrop:
	case domain.DecisionConvert:
		t.After = &domain.SubtitleState{Codec: SubtitleTargetForContainer(container), Forced: plan.Forced}
	case domain.DecisionCopy, domain.DecisionEncode:
		after := before
		t.After = &after
	}

	return t
}

// MergeMeasured replaces every prediction with what an ffprobe of the finished
// output measured (plan.md 17.2), never touching the "before" half.
func MergeMeasured(rec domain.TransformRecord, out *ffprobe.Result, actualSeconds int) domain.TransformRecord {
	rec.Duration.Actual = &actualSeconds

	if out == nil {
		return rec
	}

	rec.Container.After = normaliseFormatName(out.Format.FormatName)
	rec.Attachments.After = len(out.StreamsOfType(ffprobe.TypeAttachment))
	rec.Chapters.After = len(out.Chapters)

	if size := out.Format.SizeBytes(); size > 0 {
		rec.Size.AfterBytes = size
	}

	if video, ok := out.PrimaryVideo(); ok && rec.Video.Before != nil {
		measured := videoState(video, scanOf(video))

		if bps, src := ResolveVideoBitrate(out); src != domain.BitrateUnresolved {
			kbps := bps / 1000
			measured.BitrateKbps = &kbps
		}

		rec.Video.After = &measured
	}

	mergeAudio(rec.Audio, out)
	mergeSubtitles(rec.Subtitles, out)

	return rec
}

func mergeAudio(tracks []domain.AudioTransform, out *ffprobe.Result) {
	streams := out.StreamsOfType(ffprobe.TypeAudio)

	for i := range tracks {
		s, ok := atOutputIndex(streams, tracks[i].OutputIndex)
		if !ok {
			continue
		}

		tracks[i].After = &domain.AudioState{
			Codec:       s.CodecName,
			Profile:     s.Profile,
			Channels:    s.Channels,
			Layout:      layoutName(s),
			BitrateKbps: streamBitrateKbps(s),
		}
	}
}

func mergeSubtitles(tracks []domain.SubtitleTransform, out *ffprobe.Result) {
	streams := out.StreamsOfType(ffprobe.TypeSubtitle)

	for i := range tracks {
		s, ok := atOutputIndex(streams, tracks[i].OutputIndex)
		if !ok {
			continue
		}

		tracks[i].After = &domain.SubtitleState{Codec: s.CodecName, Forced: s.Disposition.Forced == 1}
	}
}

func atOutputIndex(streams []ffprobe.Stream, idx *int) (ffprobe.Stream, bool) {
	if idx == nil || *idx < 0 || *idx >= len(streams) {
		return ffprobe.Stream{}, false
	}

	return streams[*idx], true
}

func titlePtr(title string) *string {
	if title == "" {
		return nil
	}

	return &title
}
