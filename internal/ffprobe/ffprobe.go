// Package ffprobe wraps the ffprobe binary and models the JSON it prints. It
// holds no policy: it reports what the file says, and internal/decide decides
// what that means.
package ffprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/prober_mock.go -pkg mock . Prober

// ErrProbeFailed is returned when the ffprobe process itself fails.
var ErrProbeFailed = errors.New("ffprobe: probe failed")

// ErrUnreadable is returned when ffprobe succeeded but printed something that
// is not a probe result.
var ErrUnreadable = errors.New("ffprobe: unreadable output")

// Prober reads one file. One method, per plan.md 2.2.
type Prober interface {
	Probe(ctx context.Context, path string) (*Result, error)
}

// CLI is the ffprobe binary. Its path is bootstrap configuration (plan.md 21),
// never hard-coded.
type CLI struct {
	binary string
}

var _ Prober = (*CLI)(nil)

// New returns a Prober that shells out to the ffprobe at binary.
func New(binary string) *CLI {
	return &CLI{binary: binary}
}

// Args are the ffprobe arguments every probe uses, exported so the argv is
// visible in tests and logs rather than buried in the exec call.
func Args(path string) []string {
	return []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_chapters",
		path,
	}
}

// Probe runs ffprobe and parses its JSON.
func (c *CLI) Probe(ctx context.Context, path string) (*Result, error) {
	cmd := exec.CommandContext(ctx, c.binary, Args(path)...) //nolint:gosec // the ffprobe path is operator-supplied bootstrap config, plan.md 21
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w: %s", ErrProbeFailed, path, err, tail(stderr.String()))
	}

	res, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return res, nil
}

// Parse turns ffprobe's JSON into a Result, keeping the raw bytes so the
// caller can persist exactly what was read.
func Parse(raw []byte) (*Result, error) {
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}

	if len(res.Streams) == 0 && res.Format.FormatName == "" {
		return nil, fmt.Errorf("%w: no format and no streams", ErrUnreadable)
	}

	res.Raw = append([]byte(nil), raw...)

	return &res, nil
}

const stderrTail = 512

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= stderrTail {
		return s
	}

	return s[len(s)-stderrTail:]
}
