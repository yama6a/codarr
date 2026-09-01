package promote

// Error categories for codarr_errors_total (plan.md 24). Promotion swallows
// exactly three errors, and each one is invisible without this: the job it
// belongs to either succeeds or waits, so none of them reaches
// jobs_failed_total.
const (
	// ErrorStreamGuard is Plex failing to answer whether the target is being
	// streamed. It defers rather than fails (15.6), so a Plex that is down
	// silently stalls the queue instead of breaking it.
	ErrorStreamGuard = "plex_stream_guard"

	// ErrorNotify is the post-promotion Plex and *arr refresh. The source is
	// already gone by then, so it can only ever be a warning.
	ErrorNotify = "notify"

	// ErrorOrphanSweep is the staging-file sweep of 15.2 failing to walk.
	ErrorOrphanSweep = "orphan_sweep"
)

// Metrics is the part of plan.md 24's surface promotion produces. Deps.Metrics
// may be nil, every call goes through recorder, and nothing here may ever fail
// a promotion.
type Metrics interface {
	Error(category string)
}

type recorder struct{ m Metrics }

func (r recorder) error(category string) {
	if r.m != nil {
		r.m.Error(category)
	}
}
