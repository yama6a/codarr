package hardware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/runner_mock.go -pkg mock . Runner
//go:generate go run -mod=mod github.com/matryer/moq -out mock/store_mock.go -pkg mock . Store
//go:generate go run -mod=mod github.com/matryer/moq -out mock/fs_mock.go -pkg mock . FS

// ErrNoFfmpeg is returned when the binary cannot be run at all, which is a
// different problem from a codec that does not work.
var ErrNoFfmpeg = errors.New("hardware: ffmpeg is not runnable")

// Runner executes one ffmpeg invocation to completion and returns everything it
// printed. The probe reads both streams: the version lands on stdout and a
// codec failure lands on stderr.
type Runner interface {
	Run(ctx context.Context, args []string) (string, error)
}

// Store is the capability cache. plan.md 10.1 keys it on the ffmpeg version so
// an image upgrade re-probes instead of trusting the old answer.
type Store interface {
	ReplaceHWCapabilities(ctx context.Context, caps []domain.HWCapability) error
	ListHWCapabilities(ctx context.Context) ([]domain.HWCapability, error)
}

// FS is the one filesystem call the VP9 decode probe needs: its synthesised
// sample has to be cleaned up. fsx.FS satisfies it.
type FS interface {
	Remove(path string) error
}

// Prober runs the probe matrix of plan.md 10.1 and caches the answer.
type Prober struct {
	runner  Runner
	store   Store
	fs      FS
	clock   clock.Clock
	device  string
	tempDir string
	logger  *slog.Logger
}

// New returns a Prober. device is settings.qsv_device and tempDir is where the
// VP9 sample is written.
func New(runner Runner, st Store, fs FS, clk clock.Clock, device, tempDir string, logger *slog.Logger) *Prober {
	return &Prober{
		runner:  runner,
		store:   st,
		fs:      fs,
		clock:   clk,
		device:  device,
		tempDir: tempDir,
		logger:  logger.With(slog.String("component", "hardware")),
	}
}

// Capabilities returns the cached probe, running a fresh one when the cache is
// empty or was produced by a different ffmpeg build.
func (p *Prober) Capabilities(ctx context.Context) (Capabilities, error) {
	version, err := p.Version(ctx)
	if err != nil {
		return Capabilities{}, err
	}

	cached, err := p.store.ListHWCapabilities(ctx)
	if err != nil {
		return Capabilities{}, fmt.Errorf("read cached capabilities: %w", err)
	}

	if caps, ok := usable(cached, version, p.device); ok {
		return caps, nil
	}

	p.logger.Info("hardware capability cache is stale or empty, probing",
		slog.String("ffmpeg_version", version))

	return p.Probe(ctx)
}

// Probe runs the whole matrix and replaces the cache. This is the manual
// re-probe behind POST /api/hardware/probe.
func (p *Prober) Probe(ctx context.Context) (Capabilities, error) {
	version, err := p.Version(ctx)
	if err != nil {
		return Capabilities{}, err
	}

	now := p.clock.Now()
	entries := make([]domain.HWCapability, 0, len(Backends())*len(Profiles())+len(Backends()))

	for _, b := range Backends() {
		for _, prof := range Profiles() {
			entries = append(entries, p.encodeEntry(ctx, b, prof, version, now))
		}
	}

	entries = append(entries, p.decodeEntries(ctx, version, now)...)

	if err := p.store.ReplaceHWCapabilities(ctx, entries); err != nil {
		return Capabilities{}, fmt.Errorf("cache capabilities: %w", err)
	}

	caps := Capabilities{
		Device:        p.device,
		FfmpegVersion: version,
		ProbedAt:      now,
		Entries:       entries,
	}

	p.logger.Info("hardware probe complete",
		slog.String("ffmpeg_version", version),
		slog.String("encoder", string(caps.Select(false).Encoder)))

	return caps, nil
}

// Version is the ffmpeg build string the cache is keyed on.
func (p *Prober) Version(ctx context.Context) (string, error) {
	out, err := p.runner.Run(ctx, VersionArgs())
	if err != nil {
		return "", fmt.Errorf("%w: %w: %s", ErrNoFfmpeg, err, firstLine(out))
	}

	v := ParseVersion(out)
	if v == "" {
		return "", fmt.Errorf("%w: unrecognised -version output: %s", ErrNoFfmpeg, firstLine(out))
	}

	return v, nil
}

func (p *Prober) encodeEntry(ctx context.Context, b Backend, prof Profile, version string, now time.Time) domain.HWCapability {
	e := domain.HWCapability{
		Backend:       string(b),
		Codec:         CodecHEVC,
		Profile:       string(prof),
		Direction:     string(DirectionEncode),
		FfmpegVersion: version,
		ProbedAt:      now,
	}

	out, err := p.runner.Run(ctx, EncodeArgs(b, prof, p.device))
	if err != nil {
		e.Error = failureText(out, err)

		return e
	}

	e.Works = true

	return e
}

// decodeEntries covers the VP9 check of plan.md 10.1. The sample is synthesised
// once and decoded on each backend; lavfi cannot be fed to a hardware decoder.
func (p *Prober) decodeEntries(ctx context.Context, version string, now time.Time) []domain.HWCapability {
	entries := make([]domain.HWCapability, 0, len(Backends()))
	sample := filepath.Join(p.tempDir, ".codarr-vp9-probe.webm")

	sampleOut, sampleErr := p.runner.Run(ctx, VP9SampleArgs(sample))
	if sampleErr == nil {
		defer func() {
			if err := p.fs.Remove(sample); err != nil {
				p.logger.Warn("could not remove the VP9 probe sample",
					slog.String("path", sample), slog.String("error", err.Error()))
			}
		}()
	}

	for _, b := range Backends() {
		e := domain.HWCapability{
			Backend:       string(b),
			Codec:         CodecVP9,
			Direction:     string(DirectionDecode),
			FfmpegVersion: version,
			ProbedAt:      now,
		}

		switch {
		case sampleErr != nil:
			// Inconclusive rather than negative, but the schema has one flag.
			// The text is what the UI shows, so it says which it is.
			e.Error = "inconclusive: could not synthesise a VP9 sample to decode: " +
				failureText(sampleOut, sampleErr)
		default:
			out, err := p.runner.Run(ctx, VP9DecodeArgs(b, p.device, sample))
			if err != nil {
				e.Error = failureText(out, err)
			} else {
				e.Works = true
			}
		}

		entries = append(entries, e)
	}

	return entries
}

// usable reports whether the cached rows were all produced by this ffmpeg
// build. A mixed set means a probe was interrupted, so it is not trusted.
func usable(cached []domain.HWCapability, version, device string) (Capabilities, bool) {
	if len(cached) == 0 {
		return Capabilities{}, false
	}

	probedAt := cached[0].ProbedAt

	for _, e := range cached {
		if e.FfmpegVersion != version {
			return Capabilities{}, false
		}

		if e.ProbedAt.After(probedAt) {
			probedAt = e.ProbedAt
		}
	}

	return Capabilities{
		Device:        device,
		FfmpegVersion: version,
		ProbedAt:      probedAt,
		Entries:       cached,
	}, true
}

// ParseVersion reads the build out of `ffmpeg -version`, whose first line is
// "ffmpeg version 7.1.4-Jellyfin Copyright (c) ...".
func ParseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "version" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}

	return ""
}

// maxErrorText caps what is stored per failed entry. ffmpeg at -loglevel error
// is terse, but a driver can be chatty.
const maxErrorText = 512

func failureText(out string, err error) string {
	text := strings.TrimSpace(out)
	if text == "" {
		text = err.Error()
	}

	if len(text) > maxErrorText {
		text = text[len(text)-maxErrorText:]
	}

	return collapse(text)
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return line
}
