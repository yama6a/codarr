package decide

import (
	"fmt"
	"slices"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

type subtitleVerdict struct {
	decision domain.Decision
	reason   string
	target   string
}

func planSubtitle(s ffprobe.Stream, container domain.Container) subtitleVerdict {
	switch {
	case slices.Contains(subtitleImageCodecs, s.CodecName):
		// plan.md 6.4: forced image subtitles drop with the rest, because
		// keeping them reintroduces the burn-in transcode this exists to remove.
		return subtitleVerdict{decision: domain.DecisionDrop, reason: dropReason("image-based", s)}

	case slices.Contains(subtitleBroadcastCodecs, s.CodecName):
		return subtitleVerdict{decision: domain.DecisionDrop, reason: dropReason("broadcast caption format", s)}

	case !slices.Contains(subtitleTextCodecs, s.CodecName):
		return subtitleVerdict{decision: domain.DecisionDrop, reason: dropReason(
			fmt.Sprintf("unsupported subtitle format %s", codecName(s)), s)}
	}

	target := SubtitleTargetForContainer(container)
	if s.CodecName == target {
		return subtitleVerdict{decision: domain.DecisionCopy, target: target}
	}

	return subtitleVerdict{
		decision: domain.DecisionConvert,
		reason:   fmt.Sprintf("%s to %s", s.CodecName, SubtitleEncoder(target)),
		target:   target,
	}
}

// dropReason marks forced tracks explicitly: they are the ones worth finding
// again in the transform record (plan.md 6.4).
func dropReason(reason string, s ffprobe.Stream) string {
	if s.Disposition.Forced == 1 {
		return reason + ", forced"
	}

	return reason
}
