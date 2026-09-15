package hardware

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"go.uber.org/zap"
)

//go:generate go tool moq -out mock/runner_mock.go -pkg mock . Runner
//go:generate go tool moq -out mock/store_mock.go -pkg mock . Store
//go:generate go tool moq -out mock/fs_mock.go -pkg mock . FS

// ErrNoFfmpeg is returned when the binary cannot be run at all, which is a
// different problem from a codec that does not work.
var ErrNoFfmpeg = errors.New("hardware: ffmpeg is not runnable")

// Runner executes one ffmpeg invocation and returns both streams, because the version
// lands on stdout and a codec failure on stderr.
type Runner interface {
	Run(ctx context.Context, args []string) (string, error)
}

// Store is the capability cache, keyed on the ffmpeg version so an image upgrade
// re-probes instead of trusting the old answer (plan.md 10.1).
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
	logger  *zap.Logger
}

// New returns a Prober. device is settings.qsv_device and tempDir is where the
// VP9 sample is written.
func New(runner Runner, st Store, fs FS, clk clock.Clock, device, tempDir string, logger *zap.Logger) *Prober {
	return &Prober{
		runner:  runner,
		store:   st,
		fs:      fs,
		clock:   clk,
		device:  device,
		tempDir: tempDir,
		logger:  logger.With(zap.String("component", "hardware")),
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

	p.logger.Info("hardware capability cache is stale or empty, probing", zap.String("ffmpeg_version", version))

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
	entries := make([]domain.HWCapability, 0, len(Backends())*(len(Profiles())+len(DecodeProbes())))

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
		zap.String("ffmpeg_version", version),
		zap.String("encoder", string(caps.Select(false).Encoder)))

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

func (p *Prober) decodeEntries(ctx context.Context, version string, now time.Time) []domain.HWCapability {
	entries := make([]domain.HWCapability, 0, len(Backends())*len(DecodeProbes()))

	for _, probe := range DecodeProbes() {
		entries = append(entries, p.decodeCodec(ctx, probe, version, now)...)
	}

	return entries
}

// One sample per codec is synthesised and decoded on each backend, because lavfi
// cannot be fed to a hardware decoder (plan.md 10.1).
func (p *Prober) decodeCodec(
	ctx context.Context, probe DecodeProbe, version string, now time.Time,
) []domain.HWCapability {
	entries := make([]domain.HWCapability, 0, len(Backends()))
	sample := filepath.Join(p.tempDir, ".codarr-"+probe.Codec+"-probe"+probe.Ext)

	sampleErr := p.synthesise(ctx, probe, sample)
	if sampleErr == nil {
		defer func() {
			if err := p.fs.Remove(sample); err != nil {
				p.logger.Warn("could not remove the decode probe sample", zap.String("path", sample), zap.Error(err))
			}
		}()
	}

	for _, b := range Backends() {
		e := domain.HWCapability{
			Backend:       string(b),
			Codec:         probe.Codec,
			Direction:     string(DirectionDecode),
			FfmpegVersion: version,
			ProbedAt:      now,
		}

		switch {
		case sampleErr != nil:
			// Inconclusive rather than negative, but the schema has one flag, so the text says which.
			e.Error = "inconclusive: could not synthesise " + strings.ToUpper(probe.Codec) +
				" sample to decode: " + sampleErr.Error()
		default:
			out, err := p.runner.Run(ctx, DecodeArgs(b, p.device, sample))
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

// synthesise tries the probe's encoders in order and returns the last failure
// when none of them is in this ffmpeg build.
func (p *Prober) synthesise(ctx context.Context, probe DecodeProbe, sample string) error {
	var last error

	for _, enc := range probe.Encoders {
		out, err := p.runner.Run(ctx, SampleArgs(enc, sample))
		if err == nil {
			return nil
		}

		last = fmt.Errorf("%s: %s", enc, failureText(out, err))
	}

	return last
}

// usable reports whether the cached rows were all produced by this ffmpeg
// build and cover every codec the decode axis probes today. A mixed set means a
// probe was interrupted, a missing codec means the matrix grew since the cache
// was written; neither is trusted.
func usable(cached []domain.HWCapability, version, device string) (Capabilities, bool) {
	if len(cached) == 0 {
		return Capabilities{}, false
	}

	probedAt := cached[0].ProbedAt
	decoded := map[string]bool{}

	for _, e := range cached {
		if e.FfmpegVersion != version {
			return Capabilities{}, false
		}

		if e.ProbedAt.After(probedAt) {
			probedAt = e.ProbedAt
		}

		if e.Direction == string(DirectionDecode) {
			decoded[e.Codec] = true
		}
	}

	for _, probe := range DecodeProbes() {
		if !decoded[probe.Codec] {
			return Capabilities{}, false
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
