package ffmpeg

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
)

// FlushInterval is how often progress reaches the database. Writing every
// ffmpeg line would hammer SQLite for no benefit; with a 10-second UI poll this
// is more resolution than anyone sees (14.3).
const FlushInterval = 5 * time.Second

// StderrTailLines is how much ffmpeg stderr is kept for a failure message.
const StderrTailLines = 200

// Progress is one -progress block from a running ffmpeg.
type Progress struct {
	Frame     int64
	FPS       float64
	Speed     float64
	OutTime   time.Duration
	TotalSize int64
	Percent   float64
}

// ParseProgress reads an ffmpeg -progress pipe:1 stream, calling emit once per
// completed block, and returns the last block seen. Its OutTime is what
// verification uses for legacy containers whose headers lie about duration
// (15.3).
func ParseProgress(r io.Reader, duration time.Duration, emit func(Progress)) (Progress, error) {
	var (
		last    Progress
		current Progress
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}

		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		if key == "progress" {
			current.Percent = percentOf(current.OutTime, duration)
			last = current

			if emit != nil {
				emit(current)
			}

			continue
		}

		applyProgressField(&current, key, value)
	}

	if err := sc.Err(); err != nil {
		return last, fmt.Errorf("ffmpeg: reading progress stream: %w", err)
	}

	return last, nil
}

func applyProgressField(p *Progress, key, value string) {
	switch key {
	case "frame":
		p.Frame = parseInt(value, p.Frame)
	case "fps":
		p.FPS = parseFloat(value, p.FPS)
	case "speed":
		p.Speed = parseFloat(strings.TrimSuffix(value, "x"), p.Speed)
	case "total_size":
		p.TotalSize = parseInt(value, p.TotalSize)
	case "out_time_us":
		p.OutTime = time.Duration(parseInt(value, int64(p.OutTime/time.Microsecond))) * time.Microsecond
	}
}

func parseInt(s string, fallback int64) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fallback
	}

	return v
}

func parseFloat(s string, fallback float64) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}

	return v
}

func percentOf(out, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}

	pct := float64(out) / float64(total) * 100

	return min(pct, 100)
}

// Throttle holds the live progress value in memory and releases it to the
// caller's sink at most once per interval (14.3). It does not know about the
// store; the sink is whatever the job worker passes in.
type Throttle struct {
	mu       sync.Mutex
	clock    clock.Clock
	interval time.Duration
	sink     func(Progress)
	last     time.Time
	latest   Progress
	pending  bool
}

// NewThrottle returns a Throttle that emits to sink at most once per interval.
func NewThrottle(c clock.Clock, interval time.Duration, sink func(Progress)) *Throttle {
	return &Throttle{clock: c, interval: interval, sink: sink}
}

// Update records the live value and emits it if the interval has elapsed. The
// first update always emits, so the UI sees a job move as soon as it starts.
func (t *Throttle) Update(p Progress) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.latest = p
	t.pending = true

	now := t.clock.Now()
	if !t.last.IsZero() && now.Sub(t.last) < t.interval {
		return
	}

	t.last = now
	t.pending = false
	t.sink(p)
}

// Flush emits whatever is held, regardless of the interval. The worker calls it
// once the run ends so the final value is never lost to the throttle.
func (t *Throttle) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.pending {
		return
	}

	t.pending = false
	t.last = t.clock.Now()
	t.sink(t.latest)
}

// StderrRing keeps the last StderrTailLines lines of ffmpeg stderr, which is
// what a failed job persists (14.3, 19.1).
type StderrRing struct {
	mu    sync.Mutex
	buf   []string
	next  int
	full  bool
	tail  strings.Builder
	limit int
}

var _ io.Writer = (*StderrRing)(nil)

// NewStderrRing returns a ring holding at most n lines.
func NewStderrRing(n int) *StderrRing {
	return &StderrRing{buf: make([]string, n), limit: n}
}

// Write splits incoming bytes on newlines and keeps the trailing lines. A
// partial final line is held until its newline arrives.
func (r *StderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, b := range p {
		if b != '\n' {
			r.tail.WriteByte(b)

			continue
		}

		r.push(strings.TrimRight(r.tail.String(), "\r"))
		r.tail.Reset()
	}

	return len(p), nil
}

func (r *StderrRing) push(line string) {
	if r.limit == 0 {
		return
	}

	r.buf[r.next] = line
	r.next = (r.next + 1) % r.limit

	if r.next == 0 {
		r.full = true
	}
}

// Lines returns the retained lines oldest first, including any unterminated
// final line.
func (r *StderrRing) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []string

	if r.full {
		out = append(out, r.buf[r.next:]...)
	}

	out = append(out, r.buf[:r.next]...)

	if r.tail.Len() > 0 {
		out = append(out, r.tail.String())
	}

	return out
}

// Tail is the retained lines as one newline-joined string.
func (r *StderrRing) Tail() string {
	return strings.Join(r.Lines(), "\n")
}
