package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// RefreshInterval is how often the state-derived series are re-read. The UI
// polls every 10 seconds (plan.md 18.6) and Prometheus scrapes far less often,
// so this only has to be cheaper than the scrape.
const RefreshInterval = 15 * time.Second

// Source is the state the gauges are read back out of. The counters are
// incremented at the seams that cause them; only the gauges come from here,
// because a restart must not reset a total that SQLite already knows.
type Source interface {
	CountJobsByState(ctx context.Context) (map[domain.JobState]int, error)
	CountMediaByPlanKind(ctx context.Context) (map[domain.Kind]int, error)
	Stats(ctx context.Context) (store.Stats, error)
}

// Probe is an extra refresh step, for the series that need a live dependency
// rather than the database: plex_up, plex_active_sessions and arr_up.
type Probe func(ctx context.Context, m *Metrics)

// Refresher keeps the gauges current.
type Refresher struct {
	metrics  *Metrics
	source   Source
	clk      clock.Clock
	log      *slog.Logger
	interval time.Duration
	probes   []Probe
}

// NewRefresher returns a Refresher. A zero interval means RefreshInterval.
func NewRefresher(m *Metrics, src Source, clk clock.Clock, log *slog.Logger,
	interval time.Duration, probes ...Probe,
) *Refresher {
	if interval <= 0 {
		interval = RefreshInterval
	}

	return &Refresher{
		metrics:  m,
		source:   src,
		clk:      clk,
		log:      log.With(slog.String("component", "metrics")),
		interval: interval,
		probes:   probes,
	}
}

// Run refreshes once, then on every tick, and blocks until ctx ends.
func (r *Refresher) Run(ctx context.Context) error {
	r.Refresh(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.clk.After(r.interval):
			r.Refresh(ctx)
		}
	}
}

// Refresh reads the state-derived series once. A failure is logged and the
// previous values are kept; a scrape gap is not worth an error return.
func (r *Refresher) Refresh(ctx context.Context) {
	if states, err := r.source.CountJobsByState(ctx); err != nil {
		r.warn(ctx, "counting jobs by state failed", err)
	} else {
		r.metrics.SetQueueDepth(states[domain.JobQueued])
		r.metrics.SetAwaitingStreamEnd(states[domain.JobAwaitingStreamEnd])
	}

	if kinds, err := r.source.CountMediaByPlanKind(ctx); err != nil {
		r.warn(ctx, "counting media by plan kind failed", err)
	} else {
		r.metrics.SetFilesByPlanKind(kinds)
	}

	if s, err := r.source.Stats(ctx); err != nil {
		r.warn(ctx, "reading throughput stats failed", err)
	} else {
		r.metrics.SetBytes(s.BytesIn, s.BytesOut, s.BytesSaved)
	}

	for _, p := range r.probes {
		p(ctx, r.metrics)
	}
}

func (r *Refresher) warn(ctx context.Context, msg string, err error) {
	if ctx.Err() != nil {
		return
	}

	r.log.WarnContext(ctx, msg, slog.String("error", err.Error()))
}
