package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
)

// RecheckInterval is how long the scheduler waits before re-reading the
// settings when the schedule is off or unreadable. It bounds how long a
// settings change takes to be noticed.
const RecheckInterval = time.Minute

// Scheduler fires the daily scan of plan.md 13.2. It re-reads settings.scan_cron
// on every tick, so changing the time in the UI takes effect at the next wake
// rather than at the next restart.
type Scheduler struct {
	store   ScanStore
	scanner *Scanner
	clock   clock.Clock
	logger  *slog.Logger
}

// NewScheduler returns a Scheduler.
func NewScheduler(st ScanStore, scanner *Scanner, clk clock.Clock, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		store:   st,
		scanner: scanner,
		clock:   clk,
		logger:  logger.With(slog.String("component", "ingest.scheduler")),
	}
}

// Run blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		// A cancelled context is a clean shutdown, not a failure, and checking
		// here rather than only in the select keeps the loop deterministic when
		// the scan itself was what got cancelled.
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is how Run is meant to end
		}

		wait, fire := s.nextWait(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-s.clock.After(wait):
		}

		if !fire {
			continue
		}

		if _, err := s.scanner.ScanAll(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			s.logger.Error("scheduled scan failed", slog.String("error", err.Error()))
		}
	}
}

// nextWait reports how long to sleep and whether waking up means running a
// scan. A disabled or unparseable schedule sleeps a short interval instead, so
// fixing it in the UI does not need a restart.
func (s *Scheduler) nextWait(ctx context.Context) (time.Duration, bool) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.logger.Error("could not read settings, retrying",
			slog.String("error", err.Error()))

		return RecheckInterval, false
	}

	if !settings.ScanEnabled {
		return RecheckInterval, false
	}

	schedule, err := ParseCron(settings.ScanCron)
	if err != nil {
		s.logger.Error("scan_cron is not a schedule Codarr can read, so no scan is running",
			slog.String("scan_cron", settings.ScanCron), slog.String("error", err.Error()))

		return RecheckInterval, false
	}

	now := s.clock.Now()

	next := schedule.Next(now)
	if next.IsZero() {
		s.logger.Error("scan_cron matches no time in the next few years",
			slog.String("scan_cron", settings.ScanCron))

		return RecheckInterval, false
	}

	// Never sleep past the recheck interval in one go: a settings change made
	// at 04:05 should not wait until tomorrow to be noticed.
	if d := next.Sub(now); d > RecheckInterval {
		return RecheckInterval, false
	}

	return next.Sub(now), true
}
