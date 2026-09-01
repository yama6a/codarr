package decide

import (
	"fmt"
	"strings"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// reasonLines renders the block plan.md 7 specifies, one line per stream plus
// the container and the plan itself. It is stored and shown in the UI.
func reasonLines(probe *ffprobe.Result, p domain.Plan) []string {
	lines := make([]string, 0, len(p.Streams)+2)

	for _, s := range p.Streams {
		lines = append(lines, streamLine(probe, s))
	}

	lines = append(lines,
		fmt.Sprintf("container: %s -> %s", p.SourceContainer, p.OutputContainer),
		fmt.Sprintf("plan: %s - %s", strings.ToUpper(string(p.Kind)), summarise(p)),
	)

	return lines
}

func streamLine(probe *ffprobe.Result, s domain.StreamPlan) string {
	line := label(probe, s) + ": " + strings.ToUpper(string(s.Decision))
	if s.Reason == "" {
		return line
	}

	return line + " - " + s.Reason
}

func label(probe *ffprobe.Result, s domain.StreamPlan) string {
	switch s.Type {
	case domain.StreamVideo:
		// The primary video stream is unnumbered; a dropped one is not.
		if s.OutputIndex != nil {
			return "video"
		}

		return fmt.Sprintf("video %d", s.SourceIndex)

	case domain.StreamAudio:
		src := sourceStream(probe, ffprobe.TypeAudio, s.SourceIndex)

		return fmt.Sprintf("audio %d (%s, %s)", s.SourceIndex, s.Language, channelNotation(src.Channels))

	case domain.StreamSubtitle:
		src := sourceStream(probe, ffprobe.TypeSubtitle, s.SourceIndex)

		return fmt.Sprintf("subtitle %d (%s, %s)", s.SourceIndex, s.Language, codecName(src))

	default:
		return string(s.Type)
	}
}

func sourceStream(probe *ffprobe.Result, codecType string, ordinal int) ffprobe.Stream {
	streams := probe.StreamsOfType(codecType)
	if ordinal < 0 || ordinal >= len(streams) {
		return ffprobe.Stream{}
	}

	return streams[ordinal]
}

func summarise(p domain.Plan) string {
	if p.Kind == domain.KindSkip {
		return "every stream is already compatible"
	}

	parts := []string{videoSummary(p)}

	if n := count(p, domain.StreamAudio, domain.DecisionEncode); n > 0 {
		parts = append(parts, fmt.Sprintf("%d audio %s re-encoded", n, plural(n, "stream")))
	}

	if n := count(p, domain.StreamSubtitle, domain.DecisionConvert); n > 0 {
		parts = append(parts, fmt.Sprintf("%d subtitle %s converted", n, plural(n, "stream")))
	}

	if n := count(p, domain.StreamSubtitle, domain.DecisionDrop); n > 0 {
		parts = append(parts, fmt.Sprintf("%d subtitle %s dropped", n, plural(n, "stream")))
	}

	if p.SourceContainer != string(p.OutputContainer) {
		parts = append(parts, fmt.Sprintf("container %s -> %s", p.SourceContainer, p.OutputContainer))
	}

	return strings.Join(parts, ", ")
}

func videoSummary(p domain.Plan) string {
	v, ok := p.VideoStream()
	if !ok || v.Decision != domain.DecisionEncode {
		if p.LevelRewrite {
			return "video copied with a level flag rewrite"
		}

		return "video copied"
	}

	return fmt.Sprintf("video re-encoded to HEVC %s", strings.ToUpper(VideoEncodeProfile(p.HDR)))
}

func count(p domain.Plan, t domain.StreamType, d domain.Decision) int {
	n := 0

	for _, s := range p.Streams {
		if s.Type == t && s.Decision == d {
			n++
		}
	}

	return n
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}

	return word + "s"
}
