package ffmpeg

import (
	"sort"
)

// HardwareCorrection scales an x265 measurement up to what the UHD 630
// fixed-function encoder needs for the same quality (8.1). Policy constant,
// tunable after the VMAF spot-check in 27.
const HardwareCorrection = 1.35

// Resolution is the tier the BPP, floor and ceiling tables are keyed on.
type Resolution string

const (
	Res576p  Resolution = "576p"
	Res720p  Resolution = "720p"
	Res1080p Resolution = "1080p"
	Res1440p Resolution = "1440p"
	Res2160p Resolution = "2160p"
)

// ResolutionOf tiers on width. Letterboxing shrinks height, not width, so a
// 1280x536 scope transfer is 720p and not 576p.
func ResolutionOf(width, _ int) Resolution {
	switch {
	case width >= 3840:
		return Res2160p
	case width >= 2560:
		return Res1440p
	case width >= 1920:
		return Res1080p
	case width >= 1280:
		return Res720p
	default:
		return Res576p
	}
}

// Floor is the lower clamp from 8.3, in bits per second.
func Floor(r Resolution) int {
	switch r {
	case Res576p:
		return 800_000
	case Res720p:
		return 1_500_000
	case Res1080p:
		return 2_500_000
	case Res1440p:
		return 4_000_000
	case Res2160p:
		return 8_000_000
	default:
		return 800_000
	}
}

// Ceiling is the upper clamp from 8.3, in bits per second.
func Ceiling(r Resolution) int {
	switch r {
	case Res576p:
		return 2_500_000
	case Res720p:
		return 4_000_000
	case Res1080p:
		return 8_000_000
	case Res1440p:
		return 12_000_000
	case Res2160p:
		return 20_000_000
	default:
		return 2_500_000
	}
}

// BPP is the bits-per-pixel constant for the fallback formula (8.2).
func BPP(r Resolution) float64 {
	switch r {
	case Res1440p:
		return 0.060
	case Res2160p:
		return 0.055
	case Res576p, Res720p, Res1080p:
		return 0.065
	default:
		return 0.065
	}
}

// BitrateInput is the probe-derived video facts the target depends on.
type BitrateInput struct {
	Width  int
	Height int
	FPS    float64

	// HDR covers HDR and any 10-bit source; 8.2 adds 25% for either.
	HDR bool

	// SourceBitrate is the value resolved per 8.4, zero when unresolved.
	SourceBitrate int
}

// TargetFromSamples turns the sample probe's measurements into the encode
// target: median, hardware correction, then the clamps of 8.1 in order.
func TargetFromSamples(sampleBps []int, in BitrateInput) int {
	base := Median(sampleBps)
	if base == 0 {
		return 0
	}

	return Clamp(int(float64(base)*HardwareCorrection), in)
}

// TargetFromFallback is the 8.2 formula plus the clamps. The hardware
// correction does not apply: the BPP table is already an HEVC target rather
// than an x265 measurement.
func TargetFromFallback(in BitrateInput) int {
	return Clamp(FallbackBitrate(in), in)
}

// FallbackBitrate is the raw 8.2 formula, before clamping.
func FallbackBitrate(in BitrateInput) int {
	res := ResolutionOf(in.Width, in.Height)
	bps := float64(in.Width) * float64(in.Height) * in.FPS * BPP(res)

	// 8.2 scales for high frame rate material on top of the linear fps term.
	if in.FPS > 30 {
		scale := in.FPS / 24
		if scale > 1.6 {
			scale = 1.6
		}

		bps *= scale
	}

	if in.HDR {
		bps *= 1.25
	}

	return int(bps)
}

// Clamp applies the three clamps of 8.1 in the order they are written there:
// the source ceiling first, then the resolution floor, then the resolution
// ceiling. The order matters, because the floor is allowed to win.
func Clamp(target int, in BitrateInput) int {
	if in.SourceBitrate > 0 {
		target = min(target, in.SourceBitrate*85/100)
	}

	res := ResolutionOf(in.Width, in.Height)
	target = max(target, Floor(res))

	return min(target, Ceiling(res))
}

// Median is the middle of the sample bitrates. An even count averages the two
// middle values; the probe always takes three, so that only happens when a
// caller passes something else.
func Median(v []int) int {
	if len(v) == 0 {
		return 0
	}

	s := make([]int, len(v))
	copy(s, v)
	sort.Ints(s)

	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}

	return (s[mid-1] + s[mid]) / 2
}
