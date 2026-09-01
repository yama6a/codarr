package ffmpeg

// The rate-control and sample-probe constants of plan.md 8.1 to 8.3, named so
// GET /api/policy renders them rather than restating the literals.
const (
	// The 8.3 rate control triple, held as exact integer ratios so the emitted
	// argv numbers do not depend on float rounding.
	maxrateNum, maxrateDen = 8, 5
	bufsizeNum, bufsizeDen = 2, 1

	MaxrateFactor = float64(maxrateNum) / float64(maxrateDen)
	BufsizeFactor = float64(bufsizeNum) / float64(bufsizeDen)

	// HDRUplift is the 8.2 allowance for HDR material.
	HDRUplift = 1.25

	// FPSScaleCap bounds the 8.2 high-frame-rate scaling.
	FPSScaleCap = 1.6

	// SourceClamp caps the target at a fraction of the resolved source bitrate
	// (8.1). Skipped entirely when the source bitrate is unresolved.
	SourceClamp = 0.85

	// SkipHeadPct and SkipTailPct are the head and tail of the file the sample
	// windows avoid (8.1).
	SkipHeadPct = 0.05
	SkipTailPct = 0.05

	// SampleSegmentCount is how many windows 8.1 asks for.
	SampleSegmentCount = 3

	// SampleCRF and SamplePreset are the fixed-quality settings the sample
	// encode measures at.
	SampleCRF    = 21
	SamplePreset = "veryfast"

	// SampleEncoder is the software encoder the sample probe measures with;
	// HardwareCorrection exists because the real encode does not use it.
	SampleEncoder = "libx265"

	// LevelRewriteBSF is the bitstream filter that rewrites an H.264 level flag
	// to 4.2 while still copying the stream (6.2).
	LevelRewriteBSF = levelRewriteBSF
)

// MP4Movflags are the flags every MP4 output is muxed with: faststart moves the
// moov atom to the front, use_metadata_tags carries the loop-prevention tag.
func MP4Movflags() []string { return []string{"+faststart", "+use_metadata_tags"} }

// HEVCTagMP4 is the sample entry tag for HEVC in MP4. plan.md 14.1 mandates
// hvc1, because ffmpeg's default hev1 is refused by Apple-derived players.
const HEVCTagMP4 = "hvc1"

// HEVCTagMP4DolbyVision is the tag a Dolby Vision stream must carry in MP4
// instead, because hvc1 silently drops the dvcC record; see VERIFY.md.
const HEVCTagMP4DolbyVision = "dvh1"

// DolbyVisionStrictness is what the mov muxer demands before it will write the
// configuration record at all.
func DolbyVisionStrictness() []string { return []string{"-strict", "unofficial"} }
