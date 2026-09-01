package decide

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

type failureKind int

const (
	failCodec failureKind = iota
	failProfile
	failLevel
	failChroma
	failScan
)

type copyFailure struct {
	kind failureKind
	text string
}

type videoVerdict struct {
	decision     domain.Decision
	reason       string
	levelRewrite bool
	deinterlace  bool
	needsIdet    bool
	hdr          bool
	dolbyVision  bool
	dvProfile    int
}

func planVideo(s ffprobe.Stream, scan domain.Scan, idetPending bool) videoVerdict {
	v := videoVerdict{hdr: s.IsHDR()}
	v.dvProfile, v.dolbyVision = s.DolbyVisionProfile()

	fails := copyFailures(s, scan)
	if len(fails) == 0 {
		v.decision = domain.DecisionCopy
		v.reason = describeVideo(s, scan)
		v.reason = appendDV(v.reason, v)

		return v
	}

	if v.dolbyVision && v.dvProfile == dolbyVisionNoEncodeProfile {
		// plan.md 9: profile 5 has no HDR10 base layer, so stripping the RPU
		// produces green and purple output. Copy it whatever else is wrong.
		v.decision = domain.DecisionCopy
		v.reason = fmt.Sprintf("Dolby Vision profile 5 is never re-encoded (%s)", joinFailures(fails))

		return v
	}

	if len(fails) == 1 && fails[0].kind == failLevel && levelRewriteApplies(s) {
		v.decision = domain.DecisionCopy
		v.levelRewrite = true
		target := strconv.FormatFloat(levelRewriteTarget, 'f', 1, 64)
		v.reason = fmt.Sprintf("level %s -> %s flag rewrite (content fits %s, refs=%d)",
			s.LevelString(), target, target, s.Refs)
		v.reason = appendDV(v.reason, v)

		return v
	}

	v.decision = domain.DecisionEncode
	v.deinterlace = scan == domain.ScanInterlaced
	v.needsIdet = idetPending
	v.reason = joinFailures(fails)
	v.reason = appendDV(v.reason, v)

	return v
}

// dolbyVisionNoEncodeProfile is the profile with no HDR10 base layer. 7 and 8
// degrade gracefully to HDR10 and are re-encoded like anything else.
const dolbyVisionNoEncodeProfile = 5

func appendDV(reason string, v videoVerdict) string {
	if !v.dolbyVision {
		return reason
	}

	return fmt.Sprintf("%s, Dolby Vision profile %d", reason, v.dvProfile)
}

func copyFailures(s ffprobe.Stream, scan domain.Scan) []copyFailure {
	var fails []copyFailure

	if !slices.Contains(videoCopyCodecs, s.CodecName) {
		fails = append(fails, copyFailure{failCodec, fmt.Sprintf("codec %s is not on the copy list", codecName(s))})

		// Profile and level strings mean nothing off the copy list, and every
		// such stream is re-encoded anyway.
		return append(fails, scanFailure(scan)...)
	}

	if f, ok := profileFailure(s); ok {
		fails = append(fails, f)
	}

	if f, ok := levelFailure(s); ok {
		fails = append(fails, f)
	}

	if c := s.Chroma(); c != copyChroma {
		fails = append(fails, copyFailure{failChroma, fmt.Sprintf("chroma %s is not %s", c, copyChroma)})
	}

	return append(fails, scanFailure(scan)...)
}

func scanFailure(scan domain.Scan) []copyFailure {
	if scan != domain.ScanInterlaced {
		return nil
	}

	return []copyFailure{{failScan, "interlaced"}}
}

func profileFailure(s ffprobe.Stream) (copyFailure, bool) {
	allowed := hevcCopyProfiles
	if s.CodecName == "h264" {
		allowed = h264CopyProfiles
	}

	if s.Profile == "" {
		return copyFailure{failProfile, "profile is unknown"}, true
	}

	if !slices.Contains(allowed, s.Profile) {
		return copyFailure{failProfile, fmt.Sprintf("profile %q is not on the copy list for %s", s.Profile, s.CodecName)}, true
	}

	return copyFailure{}, false
}

// levelFailure applies the ceiling to h264 only; see h264MaxLevel.
func levelFailure(s ffprobe.Stream) (copyFailure, bool) {
	if s.CodecName != "h264" {
		return copyFailure{}, false
	}

	level, ok := s.LevelValue()
	if !ok {
		return copyFailure{failLevel, "level is unknown"}, true
	}

	if level > h264MaxLevel {
		return copyFailure{failLevel, fmt.Sprintf("level %s is above %s", s.LevelString(), strconv.FormatFloat(h264MaxLevel, 'f', 1, 64))}, true
	}

	return copyFailure{}, false
}

func levelRewriteApplies(s ffprobe.Stream) bool {
	if _, ok := s.LevelValue(); !ok {
		return false
	}

	return s.Width > 0 && s.Width <= levelRewriteMaxWidth &&
		s.Height > 0 && s.Height <= levelRewriteMaxHeight &&
		s.FrameRate() > 0 && s.FrameRate() <= levelRewriteMaxFPS &&
		s.Refs >= 1 && s.Refs <= levelRewriteMaxRefs
}

func describeVideo(s ffprobe.Stream, scan domain.Scan) string {
	parts := []string{codecName(s)}

	if s.Profile != "" {
		parts = append(parts, s.Profile)
	}

	if lvl := s.LevelString(); lvl != "" {
		parts = append(parts, "L"+lvl)
	}

	if d := s.BitDepth(); d > 0 {
		parts = append(parts, fmt.Sprintf("%d-bit", d))
	}

	if c := s.Chroma(); c != ffprobe.ChromaUnknown {
		parts = append(parts, string(c))
	}

	parts = append(parts, string(scan))

	if s.IsHDR() {
		parts = append(parts, "HDR")
	}

	return strings.Join(parts, " ")
}

func codecName(s ffprobe.Stream) string {
	if s.CodecName == "" {
		return "unknown"
	}

	return s.CodecName
}

func joinFailures(fails []copyFailure) string {
	texts := make([]string, 0, len(fails))
	for _, f := range fails {
		texts = append(texts, f.text)
	}

	return strings.Join(texts, ", ")
}

// ForceVideoEncode returns p with its primary video stream re-encoded to the
// policy's target codec, and reports whether that is allowed. It exists for the
// space reclaim sweep of plan.md 11, which is the one caller that re-encodes
// video the copy test of 6.2 passed; reason is what the sweep wants recorded
// against the stream.
//
// It reports false when there is no video stream to force, and when plan.md 9
// forbids re-encoding this one: Dolby Vision profile 5 has no HDR10 base layer,
// so no saving justifies it.
func ForceVideoEncode(p domain.Plan, reason string) (domain.Plan, bool) {
	if p.DolbyVision && p.DolbyVisionProfile == dolbyVisionNoEncodeProfile {
		return domain.Plan{}, false
	}

	streams := slices.Clone(p.Streams)
	forced := false

	for i := range streams {
		if streams[i].Type != domain.StreamVideo || streams[i].Decision == domain.DecisionDrop {
			continue
		}

		streams[i].Decision = domain.DecisionEncode
		streams[i].TargetCodec = videoEncodeCodec
		streams[i].Reason = reason
		forced = true

		break
	}

	if !forced {
		return domain.Plan{}, false
	}

	p.Streams = streams
	p.Kind = domain.KindFull

	// The stream is being re-encoded, so there is no level flag left to rewrite.
	p.LevelRewrite = false
	// The reason block is rewritten in place rather than appended to: leaving
	// the original "video: COPY" line next to a new "video: ENCODE" one puts two
	// contradictory statements in front of the user (plan.md 7).
	p.Reasons = rewriteReasons(p.Reasons,
		"video: "+strings.ToUpper(string(domain.DecisionEncode))+" - "+reason,
		"plan: "+strings.ToUpper(string(domain.KindFull))+" - "+summarise(p))

	return p, true
}

// rewriteReasons replaces the primary video line and the plan line of a reason
// block, which are the only two ForceVideoEncode changes.
func rewriteReasons(reasons []string, videoLine, planLine string) []string {
	out := slices.Clone(reasons)
	video, plan := -1, -1

	for i, line := range out {
		switch {
		case video < 0 && strings.HasPrefix(line, "video: "):
			video = i
		case strings.HasPrefix(line, "plan: "):
			plan = i
		}
	}

	if video < 0 {
		out = append(out, videoLine)
	} else {
		out[video] = videoLine
	}

	if plan < 0 {
		return append(out, planLine)
	}

	out[plan] = planLine

	return out
}
