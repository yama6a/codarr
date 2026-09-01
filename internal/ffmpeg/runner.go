package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/encoder_mock.go -pkg mock . Encoder

var (
	ErrFfmpegFailed = errors.New("ffmpeg: non-zero exit")
	ErrFfmpegStart  = errors.New("ffmpeg: could not start")
)

// DefaultGrace is how long a cancelled ffmpeg gets between SIGTERM and SIGKILL
// (19).
const DefaultGrace = 10 * time.Second

// RunResult is what one completed invocation reports back.
type RunResult struct {
	Argv []string

	// FinalOutTime is ffmpeg's own last out_time. Verification uses it for
	// legacy containers whose headers lie about duration (15.3).
	FinalOutTime time.Duration

	StderrTail string
}

// Encoder runs one ffmpeg invocation to completion, reporting progress as it
// goes.
type Encoder interface {
	Run(ctx context.Context, args []string, progress func(Progress)) (RunResult, error)
}

// Runner is the real Encoder. The binary path is injected (21).
type Runner struct {
	bin      string
	grace    time.Duration
	duration time.Duration
}

var _ Encoder = (*Runner)(nil)

// NewRunner returns a Runner for the given ffmpeg binary. duration is the
// probed source duration, used only to turn out_time into a percentage.
func NewRunner(bin string, grace, duration time.Duration) *Runner {
	return &Runner{bin: bin, grace: grace, duration: duration}
}

// Run executes ffmpeg and blocks until it exits. Cancelling ctx sends SIGTERM
// and, after the grace period, SIGKILL.
func (r *Runner) Run(ctx context.Context, args []string, progress func(Progress)) (RunResult, error) {
	//nolint:gosec // G204: the binary comes from injected configuration (21) and the args from Build.
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = r.grace

	stderr := NewStderrRing(StderrTailLines)
	cmd.Stderr = stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("%w: %w", ErrFfmpegStart, err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("%w: %w", ErrFfmpegStart, err)
	}

	var (
		wg       sync.WaitGroup
		final    Progress
		parseErr error
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		final, parseErr = ParseProgress(stdout, r.duration, progress)
	}()

	wg.Wait()

	// cmd.Wait comes before the tail is read: with a non-*os.File stderr, the
	// copy into the ring is only guaranteed complete once Wait returns.
	waitErr := cmd.Wait()

	res := RunResult{
		Argv:         append([]string{r.bin}, args...),
		FinalOutTime: final.OutTime,
		StderrTail:   stderr.Tail(),
	}

	if waitErr != nil {
		return res, fmt.Errorf("%w: %w: %s", ErrFfmpegFailed, waitErr, res.StderrTail)
	}

	return res, parseErr
}
