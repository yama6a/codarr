package decide

import (
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// containerAllowance is the 2% of plan.md 8.4 rung 3: muxing overhead that is
// in the file size but not in any stream.
const containerAllowance = 0.98

// ResolveVideoBitrate walks the fallback chain of plan.md 8.4, first match
// wins; ffprobe's per-stream bit_rate is usually absent for Matroska.
func ResolveVideoBitrate(probe *ffprobe.Result) (int, domain.BitrateSource) {
	if probe == nil {
		return 0, domain.BitrateUnresolved
	}

	video, ok := probe.PrimaryVideo()
	if !ok {
		return 0, domain.BitrateUnresolved
	}

	if bps, ok := video.BitRateBPS(); ok {
		return bps, domain.BitrateFromStream
	}

	if bps, ok := video.BPSTagBPS(); ok {
		return bps, domain.BitrateFromBPSTag
	}

	if bps, ok := computedVideoBitrate(probe); ok {
		return bps, domain.BitrateFromComputed
	}

	if bps := probe.Format.BitRateBPS(); bps > 0 {
		return bps, domain.BitrateFromFormat
	}

	return 0, domain.BitrateUnresolved
}

func computedVideoBitrate(probe *ffprobe.Result) (int, bool) {
	duration := probe.Duration()
	size := probe.Format.SizeBytes()

	if duration <= 0 || size <= 0 {
		return 0, false
	}

	total := float64(size) * 8 / duration

	var audio float64

	for _, s := range probe.StreamsOfType(ffprobe.TypeAudio) {
		if bps, ok := s.BitRateBPS(); ok {
			audio += float64(bps)

			continue
		}

		if bps, ok := s.BPSTagBPS(); ok {
			audio += float64(bps)
		}
	}

	v := total*containerAllowance - audio
	if v <= 0 {
		return 0, false
	}

	return int(v), true
}

func streamBitrateKbps(s ffprobe.Stream) *int {
	bps, ok := s.BitRateBPS()
	if !ok {
		bps, ok = s.BPSTagBPS()
	}

	if !ok {
		return nil
	}

	kbps := bps / 1000

	return &kbps
}
