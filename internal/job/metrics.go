package job

import (
	"context"
	"log/slog"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Error categories for codarr_errors_total. Each one names a place the worker
// swallows an error and carries on, which is exactly the set that is otherwise
// invisible: a failed job already reports itself through jobs_failed_total.
const (
	errorWorker      = "worker"
	errorRecovery    = "recovery"
	errorOrphanSweep = "orphan_sweep"
	errorProgress    = "progress"
	errorStaging     = "staging"
	errorState       = "state"
	errorBitrateProb = "bitrate_probe"
	errorIdet        = "idet"
	errorNotify      = "notify"
)

// Metrics is the part of the Prometheus surface of plan.md 24 the worker
// produces. Every other series is either an API seam or a gauge the refresher
// reads back out of SQLite.
//
// It is optional. Deps.Metrics may be nil, every call goes through recorder,
// and nothing here may ever fail a job.
type Metrics interface {
	JobObserved(state domain.JobState, kind domain.Kind, origin domain.JobOrigin)
	JobFailed(code domain.FailureCode)
	JobRequeued()
	EncoderFallback(from, to domain.Encoder)
	DecodeFallback()
	Error(category string)
	TranscodeDuration(kind domain.Kind, encoder domain.Encoder, seconds float64)
	EstimateError(seconds float64)
	SetQueueDepth(n int)
	SetAwaitingStreamEnd(n int)
}

// recorder is the one place the optional dependency is nil-checked.
type recorder struct{ m Metrics }

func (r recorder) enabled() bool { return r.m != nil }

func (r recorder) jobObserved(state domain.JobState, kind domain.Kind, origin domain.JobOrigin) {
	if r.m != nil {
		r.m.JobObserved(state, kind, origin)
	}
}

func (r recorder) jobFailed(code domain.FailureCode) {
	if r.m != nil {
		r.m.JobFailed(code)
	}
}

func (r recorder) jobRequeued() {
	if r.m != nil {
		r.m.JobRequeued()
	}
}

func (r recorder) encoderFallback(from, to domain.Encoder) {
	if r.m != nil {
		r.m.EncoderFallback(from, to)
	}
}

func (r recorder) decodeFallback() {
	if r.m != nil {
		r.m.DecodeFallback()
	}
}

func (r recorder) error(category string) {
	if r.m != nil {
		r.m.Error(category)
	}
}

func (r recorder) transcodeDuration(kind domain.Kind, encoder domain.Encoder, seconds float64) {
	if r.m != nil {
		r.m.TranscodeDuration(kind, encoder, seconds)
	}
}

func (r recorder) estimateError(seconds float64) {
	if r.m != nil {
		r.m.EstimateError(seconds)
	}
}

func (r recorder) queue(queued, awaiting int) {
	if r.m != nil {
		r.m.SetQueueDepth(queued)
		r.m.SetAwaitingStreamEnd(awaiting)
	}
}

// observe records one state transition the worker made and re-reads the two
// queue gauges. internal/pkg/metrics refreshes the same two from the same query
// every 15 seconds; doing it here as well means a transition is visible on the
// next scrape instead of up to a refresh later, and the two writers can never
// disagree because the number comes from the same count.
func (s *Service) observe(ctx context.Context, state domain.JobState, kind domain.Kind, origin domain.JobOrigin) {
	s.mx.jobObserved(state, kind, origin)
	s.syncQueueGauges(ctx)
}

func (s *Service) syncQueueGauges(ctx context.Context) {
	if !s.mx.enabled() {
		return
	}

	counts, err := s.store.CountJobsByState(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "counting jobs by state for the queue gauges failed", slog.Any("error", err))

		return
	}

	s.mx.queue(counts[domain.JobQueued], counts[domain.JobAwaitingStreamEnd])
}

// recordDuration is the completion half of plan.md 14.3: both the estimate and
// the measurement are stored, and the delta between them is the series that
// says whether the rolling averages are worth anything.
func (s *Service) recordDuration(t *task, actual int) {
	s.mx.transcodeDuration(t.plan.Kind, t.selection.Encoder, float64(actual))
	s.mx.estimateError(float64(actual - t.estimate))
}
