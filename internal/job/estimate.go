package job

import (
	"context"
	"errors"
	"log/slog"
	"math"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// The seeds of plan.md 14.3, used until real jobs have measured anything. All
// three are deliberately pessimistic: an estimate that turns out generous is
// read as progress, one that turns out short reads as a stall.
const (
	// SeedThroughput is the read+write rate an I/O bound job is assumed to get,
	// in bytes per second. audio_only and remux move the whole file twice.
	SeedThroughput = 40 << 20

	// SeedSpeedHardware is the encoded-seconds-per-wall-second a fixed-function
	// encoder is assumed to manage before one has been measured.
	SeedSpeedHardware = 1.5

	// SeedSpeedSoftware is the same for libx265, which is roughly an order of
	// magnitude slower and is why a silent fallback has to be visible (10.2).
	SeedSpeedSoftware = 0.2
)

// throughputWindow caps the rolling average's sample count so a library whose
// storage changes still converges instead of being pinned by ten thousand old
// measurements.
const throughputWindow = 20

// estimator is the two estimates of plan.md 14.3 and the rolling averages
// behind them. throughput_stats holds bytes per second for the I/O bound kinds
// and encoded seconds per wall second for full, keyed by (kind, encoder,
// resolution) in both cases.
type estimator struct {
	store ThroughputStore
	clk   clock.Clock
	log   *slog.Logger
}

// work is what an estimate is computed from.
type work struct {
	kind         domain.Kind
	encoder      domain.Encoder
	resolution   string
	sourceBytes  int64
	mediaSeconds float64
}

// ioBound reports the kinds whose cost is moving the file rather than encoding
// it. plan.md 7: audio encoding inside an audio_only job is negligible because
// every stream encodes concurrently with the copy.
func (w work) ioBound() bool { return w.kind != domain.KindFull }

// key is the throughput_stats natural key. The I/O bound kinds do not vary by
// encoder or resolution, so they share one row per kind.
func (w work) key() (domain.Kind, string, string) {
	if w.ioBound() {
		return w.kind, "", ""
	}

	return w.kind, string(w.encoder), w.resolution
}

// seed is the value used until the first real measurement lands.
func (w work) seed() float64 {
	switch {
	case w.ioBound():
		return SeedThroughput
	case w.encoder == domain.EncoderSoftware:
		return SeedSpeedSoftware
	default:
		return SeedSpeedHardware
	}
}

// observe is what this job actually achieved, in the same unit as the average.
func (w work) observe(actualSeconds int) float64 {
	if actualSeconds <= 0 {
		return 0
	}

	if w.ioBound() {
		return float64(w.sourceBytes) * 2 / float64(actualSeconds)
	}

	return w.mediaSeconds / float64(actualSeconds)
}

// predict turns a rate into seconds.
func (w work) predict(rate float64) int {
	if rate <= 0 {
		return 0
	}

	if w.ioBound() {
		return int(math.Ceil(float64(w.sourceBytes) * 2 / rate))
	}

	return int(math.Ceil(w.mediaSeconds / rate))
}

// Estimate is plan.md 14.3's prediction: source_bytes * 2 / throughput for the
// I/O bound kinds, media_duration / speed_ratio for full. It never returns an
// error, because a missing statistic is the normal case on a fresh install and
// a job must not fail for want of a progress bar.
func (e estimator) Estimate(ctx context.Context, w work) int {
	kind, encoder, resolution := w.key()

	stat, err := e.store.GetThroughputStat(ctx, kind, encoder, resolution)
	if err != nil || stat.Samples == 0 || stat.AvgValue <= 0 {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			e.log.WarnContext(ctx, "reading a throughput statistic failed, using the seed",
				slog.String("kind", string(kind)), slog.Any("error", err))
		}

		return w.predict(w.seed())
	}

	return w.predict(stat.AvgValue)
}

// Record is plan.md 14.3's "measure at completion". The average is a rolling
// one so a library that moves to faster storage converges rather than carrying
// its history forever.
func (e estimator) Record(ctx context.Context, w work, actualSeconds int) {
	observed := w.observe(actualSeconds)
	if observed <= 0 {
		return
	}

	kind, encoder, resolution := w.key()

	stat, err := e.store.GetThroughputStat(ctx, kind, encoder, resolution)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		e.log.WarnContext(ctx, "reading a throughput statistic failed, not recording this job",
			slog.String("kind", string(kind)), slog.Any("error", err))

		return
	}

	n := min(stat.Samples, throughputWindow)
	updated := domain.ThroughputStat{
		Kind:       kind,
		Encoder:    encoder,
		Resolution: resolution,
		Samples:    n + 1,
		AvgValue:   (stat.AvgValue*float64(n) + observed) / float64(n+1),
		UpdatedAt:  e.clk.Now(),
	}

	if err := e.store.UpsertThroughputStat(ctx, updated); err != nil {
		e.log.WarnContext(ctx, "storing a throughput statistic failed",
			slog.String("kind", string(kind)), slog.Any("error", err))
	}
}
