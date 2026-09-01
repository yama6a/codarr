package job_test

import (
	"sync"

	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// transition is one codarr_jobs_total sample.
type transition struct {
	State  domain.JobState
	Kind   domain.Kind
	Origin domain.JobOrigin
}

// fallback is one codarr_encoder_fallback_total sample.
type fallback struct {
	From domain.Encoder
	To   domain.Encoder
}

// duration is one codarr_transcode_duration_seconds sample.
type duration struct {
	Kind    domain.Kind
	Encoder domain.Encoder
	Seconds float64
}

// fakeMetrics accumulates what the worker recorded. It is hand-written rather
// than generated for the same reason fakeStore is: these tests assert the
// series that came out, not that a method was reached.
type fakeMetrics struct {
	mu sync.Mutex

	transitions    []transition
	failed         []domain.FailureCode
	requeued       int
	encoderChain   []fallback
	decodeChain    int
	errors         []string
	durations      []duration
	estimateErrors []float64
	queueDepth     []int
	awaiting       []int
}

var _ job.Metrics = (*fakeMetrics)(nil)

func (f *fakeMetrics) JobObserved(state domain.JobState, kind domain.Kind, origin domain.JobOrigin) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.transitions = append(f.transitions, transition{State: state, Kind: kind, Origin: origin})
}

func (f *fakeMetrics) JobFailed(code domain.FailureCode) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failed = append(f.failed, code)
}

func (f *fakeMetrics) JobRequeued() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requeued++
}

func (f *fakeMetrics) EncoderFallback(from, to domain.Encoder) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.encoderChain = append(f.encoderChain, fallback{From: from, To: to})
}

func (f *fakeMetrics) DecodeFallback() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.decodeChain++
}

func (f *fakeMetrics) Error(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.errors = append(f.errors, category)
}

func (f *fakeMetrics) TranscodeDuration(kind domain.Kind, encoder domain.Encoder, seconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.durations = append(f.durations, duration{Kind: kind, Encoder: encoder, Seconds: seconds})
}

func (f *fakeMetrics) EstimateError(seconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.estimateErrors = append(f.estimateErrors, seconds)
}

func (f *fakeMetrics) SetQueueDepth(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.queueDepth = append(f.queueDepth, n)
}

func (f *fakeMetrics) SetAwaitingStreamEnd(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.awaiting = append(f.awaiting, n)
}

func (f *fakeMetrics) states() []transition {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]transition(nil), f.transitions...)
}

func (f *fakeMetrics) snapshot() metricsSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	return metricsSnapshot{
		Failed:         append([]domain.FailureCode(nil), f.failed...),
		Requeued:       f.requeued,
		EncoderChain:   append([]fallback(nil), f.encoderChain...),
		DecodeChain:    f.decodeChain,
		Errors:         append([]string(nil), f.errors...),
		Durations:      append([]duration(nil), f.durations...),
		EstimateErrors: append([]float64(nil), f.estimateErrors...),
	}
}

// metricsSnapshot is everything but the transitions and the gauges, so a test
// can assert the whole thing rather than a field at a time.
type metricsSnapshot struct {
	Failed         []domain.FailureCode
	Requeued       int
	EncoderChain   []fallback
	DecodeChain    int
	Errors         []string
	Durations      []duration
	EstimateErrors []float64
}
