package decide

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

type audioVerdict struct {
	decision domain.Decision
	reason   string
	target   audioTarget
}

func planAudio(s ffprobe.Stream, container domain.Container) audioVerdict {
	if slices.Contains(audioCopyList(s.Channels), s.CodecName) {
		return audioVerdict{
			decision: domain.DecisionCopy,
			reason:   fmt.Sprintf("%s, %s", codecName(s), layoutName(s)),
		}
	}

	target := audioEncodeTarget(container, s.Channels)
	reason := fmt.Sprintf("%s not in copy list for %s channels", codecName(s), channelBucket(s.Channels))

	// plan.md 3, rule 9: the channel count only ever changes here, on a
	// conversion that was required anyway.
	if target.Channels < s.Channels {
		reason = fmt.Sprintf("%s, downmixed %s to %s", reason,
			channelNotation(s.Channels), channelNotation(target.Channels))
	}

	return audioVerdict{decision: domain.DecisionEncode, reason: reason, target: target}
}

func channelBucket(channels int) string {
	if channels <= 2 {
		return "1-2"
	}

	return "3+"
}

// layoutName is ffprobe's channel_layout with the variant suffix removed, so
// "5.1(side)" reads as "5.1".
func layoutName(s ffprobe.Stream) string {
	if s.ChannelLayout == "" {
		return channelNotation(s.Channels)
	}

	name, _, _ := strings.Cut(s.ChannelLayout, "(")

	return name
}

func channelNotation(channels int) string {
	switch channels {
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 3:
		return "2.1"
	case 4:
		return "4.0"
	case 5:
		return "4.1"
	case 6:
		return "5.1"
	case 7:
		return "6.1"
	case 8:
		return "7.1"
	default:
		return fmt.Sprintf("%dch", channels)
	}
}
