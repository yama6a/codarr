package decide

import (
	"slices"
	"strings"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Options are the per-file inputs that are not in the probe.
type Options struct {
	// Path is the source file; the output container keys on its extension
	// (plan.md 6.1), not the demuxer name. Empty falls back to the probe.
	Path string

	// IdetScan is the answer to an earlier Analysis.NeedsIdetSample. Empty
	// means no sample was taken.
	IdetScan domain.Scan
}

// Analysis is a plan plus the one thing the engine cannot settle from an
// ffprobe result alone.
type Analysis struct {
	Plan domain.Plan

	// NeedsIdetSample asks the caller to run an idet sample and re-plan with
	// Options.IdetScan (plan.md 6.2). The plan alongside is the progressive read.
	NeedsIdetSample bool
}

// Engine turns a probe into a plan, with no state and no dependencies, so the
// same probe always produces the same plan.
type Engine struct{}

// New returns the decision engine.
func New() Engine { return Engine{} }

// Plan applies the whole of plan.md section 6 to one probed file.
func (e Engine) Plan(probe *ffprobe.Result, opts Options) (Analysis, error) {
	if probe == nil {
		return Analysis{}, ErrNoProbe
	}

	video, ok := probe.PrimaryVideo()
	if !ok {
		return Analysis{}, ErrNoVideoStream
	}

	out := ContainerFor(sourcePath(probe, opts))
	scan, idetPending := resolveScan(video, opts)
	verdict := planVideo(video, scan, idetPending)

	p := domain.Plan{
		SourceContainer: normaliseFormatName(probe.Format.FormatName),
		OutputContainer: out,
		LevelRewrite:    verdict.levelRewrite,
		Deinterlace:     verdict.deinterlace,
		HDR:             verdict.hdr,
		DolbyVision:     verdict.dolbyVision,
		PolicyHash:      PolicyHash(),
	}

	if verdict.dolbyVision {
		p.DolbyVisionProfile = verdict.dvProfile
	}

	p.Streams = append(p.Streams, videoPlans(probe, video, verdict)...)
	p.Streams = append(p.Streams, audioPlans(probe, out)...)
	p.Streams = append(p.Streams, subtitlePlans(probe, out)...)
	assignOutputIndices(p.Streams)

	p.Kind = deriveKind(p)

	if p.Kind != domain.KindSkip && !hasOutputAudio(p) {
		return Analysis{}, ErrNoAudioStreams
	}

	p.Reasons = reasonLines(probe, p)

	return Analysis{Plan: p, NeedsIdetSample: verdict.needsIdet}, nil
}

func sourcePath(probe *ffprobe.Result, opts Options) string {
	if opts.Path != "" {
		return opts.Path
	}

	return probe.Format.Filename
}

// plan.md 6.2: only an explicit interlaced field_order counts, and a legacy
// encode-path codec that says nothing earns an idet sample rather than a guess.
func resolveScan(video ffprobe.Stream, opts Options) (domain.Scan, bool) {
	if opts.IdetScan == domain.ScanInterlaced {
		return domain.ScanInterlaced, false
	}

	if opts.IdetScan != "" {
		return domain.ScanProgressive, false
	}

	if video.Interlaced() {
		return domain.ScanInterlaced, false
	}

	pending := !video.FieldOrderKnown() && slices.Contains(legacyScanCodecs, video.CodecName)

	return domain.ScanProgressive, pending
}

func videoPlans(probe *ffprobe.Result, primary ffprobe.Stream, v videoVerdict) []domain.StreamPlan {
	streams := probe.StreamsOfType(ffprobe.TypeVideo)
	plans := make([]domain.StreamPlan, 0, len(streams))

	for i, s := range streams {
		p := basePlan(domain.StreamVideo, i, s)

		switch {
		case s.Index == primary.Index:
			p.Decision = v.decision
			p.Reason = v.reason

			if v.decision == domain.DecisionEncode {
				p.TargetCodec = videoEncodeCodec
			}
		case s.IsAttachedPic():
			p.Decision = domain.DecisionDrop
			p.Reason = "attached picture (cover art)"
		default:
			p.Decision = domain.DecisionDrop
			p.Reason = "secondary video stream"
		}

		plans = append(plans, p)
	}

	return plans
}

func audioPlans(probe *ffprobe.Result, container domain.Container) []domain.StreamPlan {
	streams := probe.StreamsOfType(ffprobe.TypeAudio)
	plans := make([]domain.StreamPlan, 0, len(streams))

	for i, s := range streams {
		v := planAudio(s, container)
		p := basePlan(domain.StreamAudio, i, s)
		p.Decision = v.decision
		p.Reason = v.reason

		if v.decision == domain.DecisionEncode {
			p.TargetCodec = v.target.Codec
			p.TargetBitrate = v.target.Bitrate
			p.TargetChannels = v.target.Channels
		}

		plans = append(plans, p)
	}

	return plans
}

func subtitlePlans(probe *ffprobe.Result, container domain.Container) []domain.StreamPlan {
	streams := probe.StreamsOfType(ffprobe.TypeSubtitle)
	plans := make([]domain.StreamPlan, 0, len(streams))

	for i, s := range streams {
		v := planSubtitle(s, container)
		p := basePlan(domain.StreamSubtitle, i, s)
		p.Decision = v.decision
		p.Reason = v.reason

		if v.decision == domain.DecisionConvert {
			p.TargetCodec = v.target
		}

		plans = append(plans, p)
	}

	return plans
}

// ffmpeg does not reliably propagate dispositions, so plan.md 6.3 sets them
// explicitly downstream, which means recording them here.
func basePlan(t domain.StreamType, ordinal int, s ffprobe.Stream) domain.StreamPlan {
	return domain.StreamPlan{
		Type:           t,
		SourceIndex:    ordinal,
		Language:       s.Language(),
		Title:          s.Title(),
		Default:        s.Disposition.Default == 1,
		Forced:         s.Disposition.Forced == 1,
		Comment:        s.Disposition.Comment == 1,
		VisualImpaired: s.Disposition.VisualImpaired == 1,
	}
}

// Per-type numbering of the kept streams: the mapping plan.md 14.2 says every
// codec, disposition and bitstream-filter option is addressed by.
func assignOutputIndices(plans []domain.StreamPlan) {
	next := map[domain.StreamType]int{}

	for i := range plans {
		if plans[i].Decision == domain.DecisionDrop {
			continue
		}

		idx := next[plans[i].Type]
		plans[i].OutputIndex = &idx
		next[plans[i].Type] = idx + 1
	}
}

func deriveKind(p domain.Plan) domain.Kind {
	video, ok := p.VideoStream()
	if ok && video.Decision == domain.DecisionEncode {
		return domain.KindFull
	}

	for _, s := range p.Streams {
		if s.Type == domain.StreamVideo || s.Decision == domain.DecisionCopy {
			continue
		}

		return domain.KindAudioOnly
	}

	// A level rewrite is a copy with a bitstream filter attached, so the file
	// is still rebuilt (plan.md 6.2) - remux, never full.
	if p.LevelRewrite || p.SourceContainer != string(p.OutputContainer) {
		return domain.KindRemux
	}

	return domain.KindSkip
}

func hasOutputAudio(p domain.Plan) bool {
	for _, s := range p.Streams {
		if s.Type == domain.StreamAudio && s.OutputIndex != nil {
			return true
		}
	}

	return false
}

// ffprobe reports a demuxer list, so "matroska,webm" has to reduce to one
// family name before it compares against the output container.
func normaliseFormatName(formatName string) string {
	if formatName == "" {
		return "unknown"
	}

	names := strings.Split(formatName, ",")

	for _, n := range names {
		if n == string(domain.ContainerMatroska) || n == string(domain.ContainerMP4) {
			return n
		}
	}

	return names[0]
}
