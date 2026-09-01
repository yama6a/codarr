package promote

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// durationTolerance is the 1% of plan.md 15.3.
const durationTolerance = 0.01

// rewrittenLevel is the level a level-rewrite plan must produce (plan.md 6.2).
const rewrittenLevel = "42"

// Output is the subset of an ffprobe result verification acts on.
type Output struct {
	DurationSeconds float64
	Streams         []OutputStream
}

// OutputStream is one stream of the probed output. DolbyVision is true when the
// stream carries a DOVI configuration record.
type OutputStream struct {
	Type        domain.StreamType
	Codec       string
	Profile     string
	Level       string
	Width       int
	Height      int
	Language    string
	DolbyVision bool
}

// Verify is plan.md 15.3. It runs before anything is destroyed, and on failure
// the staging file is left in place for inspection. Every failure names the
// value that was wrong (19.1), never just "verification failed".
func (p *Promoter) Verify(ctx context.Context, req Request) ([]string, error) {
	out, err := p.prober.Probe(ctx, req.Staging.Path)
	if err != nil {
		return nil, wrap(domain.FailVerification, err, "ffprobe could not read the output %s", req.Staging.Path)
	}

	info, err := p.fs.Stat(req.Staging.Path)
	if err != nil {
		return nil, wrap(domain.FailVerification, err, "the output %s could not be stat'd", req.Staging.Path)
	}

	checks := []func() ([]string, error){
		func() ([]string, error) { return verifyDuration(req, out) },
		func() ([]string, error) { return nil, verifyStreamShape(req, out) },
		func() ([]string, error) { return nil, verifyAudio(req, out) },
		func() ([]string, error) { return nil, verifyVideoCopy(req, out) },
		func() ([]string, error) { return verifyDolbyVision(req, out) },
		func() ([]string, error) { return nil, verifySize(req, info.Size) },
	}

	var warnings []string

	for _, check := range checks {
		w, err := check()
		warnings = append(warnings, w...)

		if err != nil {
			return warnings, err
		}
	}

	return warnings, nil
}

// verifyDuration carries the legacy fallback of plan.md 15.3: VOB, AVI and
// friends routinely lie in their container headers, so ffmpeg's own final
// out_time is the ground truth for what it actually wrote.
func verifyDuration(req Request, out Output) ([]string, error) {
	src := req.Source.DurationSeconds

	if src > 0 && withinTolerance(out.DurationSeconds, src) {
		return nil, nil
	}

	if !req.Source.LegacyContainer {
		if src <= 0 {
			return nil, fail(domain.FailVerification,
				"the source duration is unknown, so the output duration of %.0fs cannot be verified", out.DurationSeconds)
		}

		return nil, fail(domain.FailVerification,
			"output duration %.0fs differs from source %.0fs by more than 1%%", out.DurationSeconds, src)
	}

	if req.FinalOutTimeSeconds <= 0 {
		return nil, fail(domain.FailVerification,
			"output duration %.0fs differs from source %.0fs by more than 1%% and ffmpeg reported no final out_time to check against",
			out.DurationSeconds, src)
	}

	if !withinTolerance(out.DurationSeconds, req.FinalOutTimeSeconds) {
		return nil, fail(domain.FailVerification,
			"output duration %.0fs differs from ffmpeg's own out_time %.0fs by more than 1%%",
			out.DurationSeconds, req.FinalOutTimeSeconds)
	}

	return []string{fmtf(
		"the legacy source container reported a duration of %.0fs but ffmpeg wrote %.0fs; trusting ffmpeg's out_time",
		src, req.FinalOutTimeSeconds,
	)}, nil
}

func verifyStreamShape(req Request, out Output) error {
	expected := expectedStreams(req.Plan)

	if len(out.Streams) != len(expected) {
		return fail(domain.FailVerification,
			"the output has %d streams, the plan expected %d", len(out.Streams), len(expected))
	}

	for i, want := range expected {
		if got := out.Streams[i].Type; got != want.Type {
			return fail(domain.FailVerification,
				"output stream %d is %s, the plan expected %s at that position", i, got, want.Type)
		}
	}

	return nil
}

func verifyAudio(req Request, out Output) error {
	present := map[string]bool{}
	count := 0

	for _, s := range out.Streams {
		if s.Type != domain.StreamAudio {
			continue
		}

		count++
		present[strings.ToLower(s.Language)] = true
	}

	// The checklist in plan.md 26 makes this absolute: never produce a file with
	// zero audio streams.
	if count == 0 {
		return fail(domain.FailVerification, "the output has no audio streams at all")
	}

	for _, want := range expectedAudioLanguages(req.Plan) {
		if !present[want] {
			return fail(domain.FailVerification,
				"the output has no audio stream tagged language %q, which the plan kept", want)
		}
	}

	return nil
}

// verifyVideoCopy catches a copy that silently re-encoded (plan.md 15.3).
func verifyVideoCopy(req Request, out Output) error {
	planned, ok := req.Plan.VideoStream()
	if !ok || planned.Decision != domain.DecisionCopy || req.Source.Video == nil {
		return nil
	}

	got, ok := firstVideo(out)
	if !ok {
		return fail(domain.FailVerification, "the plan copies the video stream but the output has no video stream")
	}

	src := req.Source.Video

	if src.Codec != "" && got.Codec != src.Codec {
		return fail(domain.FailVerification,
			"the output video codec is %q and the source was %q, but the plan said copy", got.Codec, src.Codec)
	}

	if src.Profile != "" && got.Profile != src.Profile {
		return fail(domain.FailVerification,
			"the output video profile is %q and the source was %q, but the plan said copy", got.Profile, src.Profile)
	}

	if src.Width != 0 && src.Height != 0 && (got.Width != src.Width || got.Height != src.Height) {
		return fail(domain.FailVerification,
			"the output resolution is %dx%d and the source was %dx%d, but the plan said copy",
			got.Width, got.Height, src.Width, src.Height)
	}

	return verifyLevel(req.Plan, src.Level, got.Level)
}

// verifyLevel: the level is exempt from the copy check exactly when the plan
// recorded a level rewrite, and then it has to be 4.2 (plan.md 6.2, 15.3).
func verifyLevel(plan domain.Plan, source, output string) error {
	if plan.LevelRewrite {
		if normaliseLevel(output) != rewrittenLevel {
			return fail(domain.FailVerification,
				"the plan rewrote the H.264 level to 4.2 but the output reports level %q", output)
		}

		return nil
	}

	if source == "" || normaliseLevel(output) == normaliseLevel(source) {
		return nil
	}

	return fail(domain.FailVerification,
		"the output video level is %q and the source was %q, but the plan said copy", output, source)
}

// verifyDolbyVision: profile 5 has no HDR10 base layer, so losing the record
// renders green and purple. Profiles 7 and 8 degrade to HDR10 (plan.md 9).
func verifyDolbyVision(req Request, out Output) ([]string, error) {
	if !req.Plan.DolbyVision {
		return nil, nil
	}

	if got, ok := firstVideo(out); ok && got.DolbyVision {
		return nil, nil
	}

	if req.Plan.DolbyVisionProfile == 5 {
		return nil, fail(domain.FailVerification,
			"the source is Dolby Vision profile 5 but the output carries no DOVI configuration record; without it the stream renders with wrong colour")
	}

	return []string{fmtf(
		"the source is Dolby Vision profile %d but the output carries no DOVI configuration record; playback degrades to HDR10",
		req.Plan.DolbyVisionProfile,
	)}, nil
}

// verifySize applies to full plans only. An audio_only plan legitimately grows
// a file when a 1.5 Mbps DTS track becomes 640k AC3 (plan.md 15.3).
func verifySize(req Request, outputSize int64) error {
	if req.Plan.Kind != domain.KindFull || outputSize <= req.Source.SizeBytes {
		return nil
	}

	return fail(domain.FailVerification,
		"the output is %s (%d bytes), larger than the %s (%d bytes) source, and the plan is a full transcode",
		human(outputSize), outputSize, human(req.Source.SizeBytes), req.Source.SizeBytes)
}

func expectedStreams(plan domain.Plan) []domain.StreamPlan {
	out := make([]domain.StreamPlan, 0, len(plan.Streams))

	for _, s := range plan.Streams {
		if s.OutputIndex == nil || s.Decision == domain.DecisionDrop {
			continue
		}

		out = append(out, s)
	}

	sort.SliceStable(out, func(i, j int) bool { return *out[i].OutputIndex < *out[j].OutputIndex })

	return out
}

func expectedAudioLanguages(plan domain.Plan) []string {
	var out []string

	for _, s := range expectedStreams(plan) {
		if s.Type != domain.StreamAudio || s.Language == "" {
			continue
		}

		lang := strings.ToLower(s.Language)
		if !contains(out, lang) {
			out = append(out, lang)
		}
	}

	return out
}

func firstVideo(out Output) (OutputStream, bool) {
	for _, s := range out.Streams {
		if s.Type == domain.StreamVideo {
			return s, true
		}
	}

	return OutputStream{}, false
}

func withinTolerance(got, want float64) bool {
	return math.Abs(got-want)/want <= durationTolerance
}

func normaliseLevel(l string) string {
	return strings.TrimSpace(strings.ReplaceAll(l, ".", ""))
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}

	return false
}

func human(bytes int64) string {
	const unit = 1024

	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	value, exp := float64(bytes)/unit, 0

	for value >= unit && exp < 3 {
		value /= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", value, "KMGT"[exp])
}
