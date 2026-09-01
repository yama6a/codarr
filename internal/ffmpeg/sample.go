package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/yama6a/codarr/internal/pkg/fsx"
)

var ErrSampleProbe = errors.New("ffmpeg: sample probe failed")

// SampleSeconds is the length of each sample window (8.1).
const SampleSeconds = 60.0

// Segment is one sample window.
type Segment struct {
	Start    float64
	Duration float64
}

// SampleSegments picks the three windows of 8.1: skip the first and last 5%,
// then take 60 seconds at 20%, 50% and 80% of what is left. Windows are pulled
// back so they fit inside the file, and a file shorter than one window becomes
// a single whole-file sample.
func SampleSegments(durationSec float64) []Segment {
	if durationSec <= 0 {
		return nil
	}

	if durationSec <= SampleSeconds {
		return []Segment{{Start: 0, Duration: durationSec}}
	}

	segs := make([]Segment, 0, 3)

	for _, at := range []float64{0.2, 0.5, 0.8} {
		start := durationSec*0.05 + durationSec*0.90*at
		if start+SampleSeconds > durationSec {
			start = durationSec - SampleSeconds
		}

		if len(segs) > 0 && segs[len(segs)-1].Start == start {
			continue
		}

		segs = append(segs, Segment{Start: start, Duration: SampleSeconds})
	}

	return segs
}

// SampleArgs is the fixed-quality sample encode from 8.1. It writes a real file
// because -f null - does not reliably report output size.
func SampleArgs(src string, seg Segment, out string) []string {
	return []string{
		"-nostdin",
		"-ss", formatSeconds(seg.Start),
		"-t", formatSeconds(seg.Duration),
		"-i", src,
		"-an", "-sn",
		"-c:v", SampleEncoder,
		"-crf", strconv.Itoa(SampleCRF),
		"-preset", SamplePreset,
		"-x265-params", "log-level=none",
		out,
	}
}

func formatSeconds(s float64) string {
	return strconv.FormatFloat(s, 'f', 3, 64)
}

// SampleBitrate is what a measured sample file works out to, in bits per second.
func SampleBitrate(sizeBytes int64, durationSec float64) int {
	if durationSec <= 0 {
		return 0
	}

	return int(float64(sizeBytes) * 8 / durationSec)
}

// SampleFS is the two filesystem calls the sample probe makes. fsx.FS
// satisfies it.
type SampleFS interface {
	Stat(path string) (fsx.FileInfo, error)
	Remove(path string) error
}

// SampleProbe measures what libx265 at CRF 21 costs for one file's content.
type SampleProbe struct {
	enc Encoder
	fs  SampleFS
	dir string
}

// NewSampleProbe returns a SampleProbe writing its temporary samples into dir.
func NewSampleProbe(enc Encoder, fs SampleFS, dir string) *SampleProbe {
	return &SampleProbe{enc: enc, fs: fs, dir: dir}
}

// Base runs the samples and returns the median of their bitrates, which is the
// base the hardware correction and clamps of 8.1 are applied to. The samples run
// concurrently: they are independent, and this is not a transcode, so the
// one-job-at-a-time rule does not apply.
func (p *SampleProbe) Base(ctx context.Context, src string, durationSec float64) (int, error) {
	segs := SampleSegments(durationSec)
	if len(segs) == 0 {
		return 0, fmt.Errorf("%w: source duration unknown", ErrSampleProbe)
	}

	var (
		wg    sync.WaitGroup
		rates = make([]int, len(segs))
		errs  = make([]error, len(segs))
	)

	for i, seg := range segs {
		wg.Add(1)

		go func() {
			defer wg.Done()

			rates[i], errs[i] = p.measure(ctx, src, seg, i)
		}()
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return 0, err
	}

	return Median(rates), nil
}

func (p *SampleProbe) measure(ctx context.Context, src string, seg Segment, n int) (int, error) {
	out := filepath.Join(p.dir, "probe_"+strconv.Itoa(n)+".mkv")

	defer func() { _ = p.fs.Remove(out) }()

	if _, err := p.enc.Run(ctx, SampleArgs(src, seg, out), nil); err != nil {
		return 0, fmt.Errorf("%w: segment %d: %w", ErrSampleProbe, n, err)
	}

	info, err := p.fs.Stat(out)
	if err != nil {
		return 0, fmt.Errorf("%w: stat segment %d: %w", ErrSampleProbe, n, err)
	}

	return SampleBitrate(info.Size, seg.Duration), nil
}
