package ffmpeg

import (
	"strconv"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// IdetFrames is the interlacing sample length of plan.md 6.2, around 20 seconds,
// enough for the filter's counters to separate combing from noise.
const IdetFrames = 500

// IdetArgs is the sample of plan.md 6.2, run by the worker rather than the
// decision engine, which will not shell out.
func IdetArgs(src string) []string {
	return []string{
		"-hide_banner", "-nostdin",
		"-i", src,
		"-frames:v", strconv.Itoa(IdetFrames),
		"-vf", "idet",
		"-an", "-sn",
		"-f", "null", "-",
	}
}

// idetCounts is one of the filter's summary lines.
type idetCounts struct {
	tff         int
	bff         int
	progressive int
}

func (c idetCounts) total() int { return c.tff + c.bff + c.progressive }

// ParseIdet reads the idet filter's stderr counters; anything it cannot read is
// progressive (plan.md 6.2), since a wrong interlaced verdict combs clean frames.
func ParseIdet(stderr string) domain.Scan {
	var multi, single idetCounts

	for _, line := range strings.Split(stderr, "\n") {
		switch {
		case strings.Contains(line, "Multi frame detection:"):
			multi = idetLine(line, "Multi frame detection:")
		case strings.Contains(line, "Single frame detection:"):
			single = idetLine(line, "Single frame detection:")
		}
	}

	counts := multi
	if counts.total() == 0 {
		counts = single
	}

	if counts.total() == 0 || counts.tff+counts.bff <= counts.progressive {
		return domain.ScanProgressive
	}

	return domain.ScanInterlaced
}

func idetLine(line, label string) idetCounts {
	_, rest, ok := strings.Cut(line, label)
	if !ok {
		return idetCounts{}
	}

	fields := strings.Fields(rest)
	out := idetCounts{}

	for i := 0; i+1 < len(fields); i++ {
		value, err := strconv.Atoi(fields[i+1])
		if err != nil {
			continue
		}

		switch fields[i] {
		case "TFF:":
			out.tff = value
		case "BFF:":
			out.bff = value
		case "Progressive:":
			out.progressive = value
		}
	}

	return out
}
