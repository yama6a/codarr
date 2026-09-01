package promote

// Error categories for codarr_errors_total (plan.md 24). Promotion swallows these
// three, so none of them ever reaches jobs_failed_total.
const (
	// ErrorStreamGuard is Plex failing to answer whether the target is streamed;
	// it defers rather than fails (15.6), so a dead Plex silently stalls the queue.
	ErrorStreamGuard = "plex_stream_guard"

	// ErrorNotify is the post-promotion Plex and *arr refresh, which can only ever
	// be a warning because the source is already gone.
	ErrorNotify = "notify"

	// ErrorOrphanSweep is the staging-file sweep of 15.2 failing to walk.
	ErrorOrphanSweep = "orphan_sweep"
)

// Metrics is the part of plan.md 24's surface promotion produces; Deps.Metrics may
// be nil, and nothing here may ever fail a promotion.
type Metrics interface {
	Error(category string)
}

type recorder struct{ m Metrics }

func (r recorder) error(category string) {
	if r.m != nil {
		r.m.Error(category)
	}
}
