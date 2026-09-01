package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/yama6a/codarr/internal/pkg/clock"
)

// PruneInterval is how often the retention bounds of plan.md 24 are applied; the
// bounds themselves live in internal/pkg/store, which owns the statements.
const PruneInterval = time.Hour

// PruneStore is the retention half of the events table.
type PruneStore interface {
	PruneEvents(ctx context.Context, now time.Time) (int64, error)
}

// Pruner applies the retention bounds on a schedule.
type Pruner struct {
	store    PruneStore
	clk      clock.Clock
	log      *slog.Logger
	interval time.Duration
}

// NewPruner returns a Pruner. A zero interval means PruneInterval.
func NewPruner(st PruneStore, clk clock.Clock, log *slog.Logger, interval time.Duration) *Pruner {
	if interval <= 0 {
		interval = PruneInterval
	}

	return &Pruner{
		store:    st,
		clk:      clk,
		log:      log.With(slog.String("component", "events.prune")),
		interval: interval,
	}
}

// Run prunes once immediately, then on every tick, and blocks until ctx ends.
func (p *Pruner) Run(ctx context.Context) error {
	p.once(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.clk.After(p.interval):
			p.once(ctx)
		}
	}
}

func (p *Pruner) once(ctx context.Context) {
	deleted, err := p.store.PruneEvents(ctx, p.clk.Now())
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		p.log.ErrorContext(ctx, "pruning the events table failed", slog.String("error", err.Error()))

		return
	}

	if deleted > 0 {
		p.log.InfoContext(ctx, "pruned the events table", slog.Int64("deleted", deleted))
	}
}
