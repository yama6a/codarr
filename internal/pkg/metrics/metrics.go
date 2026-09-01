// Package metrics is the Prometheus surface of plan.md 24, served at /metrics
// outside /api.
//
// Nothing here is a package-level variable. The registry is built and handed to
// New, so a test gets its own and the wiring stays in cmd/codarr like every
// other dependency.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Namespace prefixes every series.
const Namespace = "codarr"

// Metrics is the whole set of series plan.md 24 names.
type Metrics struct {
	registry *prometheus.Registry

	jobsTotal          *prometheus.CounterVec
	jobsFailedTotal    *prometheus.CounterVec
	jobsRequeuedTotal  prometheus.Counter
	encoderFallback    *prometheus.CounterVec
	decodeFallback     prometheus.Counter
	errorsTotal        *prometheus.CounterVec
	transcodeDuration  *prometheus.HistogramVec
	estimateError      prometheus.Histogram
	queueDepth         prometheus.Gauge
	awaitingStreamEnd  prometheus.Gauge
	filesByPlanKind    *prometheus.GaugeVec
	bytesIn            prometheus.Gauge
	bytesOut           prometheus.Gauge
	bytesSaved         prometheus.Gauge
	plexUp             prometheus.Gauge
	plexActiveSessions prometheus.Gauge
	arrUp              *prometheus.GaugeVec
}

// New builds every series and registers it, alongside the Go and process
// collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		registry: reg,

		jobsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "jobs_total",
			Help:      "Jobs observed entering each state.",
		}, []string{"state", "kind", "origin"}),

		jobsFailedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "jobs_failed_total",
			Help:      "Failed jobs by failure code (plan.md 19.1).",
		}, []string{"failure_code"}),

		jobsRequeuedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "jobs_requeued_total",
			Help:      "Jobs automatically re-queued after an interruption (plan.md 19.2).",
		}),

		encoderFallback: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "encoder_fallback_total",
			Help:      "Steps down the encoder chain of plan.md 10.2.",
		}, []string{"from", "to"}),

		decodeFallback: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "decode_fallback_total",
			Help:      "Retries that dropped from hardware to software decode.",
		}),

		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "errors_total",
			Help:      "Errors by category.",
		}, []string{"category"}),

		transcodeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "transcode_duration_seconds",
			Help:      "Wall-clock duration of a completed job.",
			Buckets:   durationBuckets(),
		}, []string{"kind", "encoder"}),

		estimateError: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "transcode_estimate_error_seconds",
			Help:      "Actual duration minus the estimate. Negative means the estimate was pessimistic.",
			Buckets:   estimateErrorBuckets(),
		}),

		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "queue_depth",
			Help:      "Jobs in the queued state.",
		}),

		awaitingStreamEnd: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "jobs_awaiting_stream_end",
			Help:      "Jobs deferred because Plex is streaming the target (plan.md 15.6).",
		}),

		filesByPlanKind: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "files_by_plan_kind",
			Help:      "Analysed library files by the plan kind they currently need.",
		}, []string{"kind"}),

		// plan.md 24 pins these three names. bytes_in and bytes_out only ever
		// grow, but all three are read back out of SQLite rather than
		// accumulated in process, so a restart must not reset them: a gauge set
		// from the stored totals is the only representation that survives one.
		bytesIn: prometheus.NewGauge(prometheus.GaugeOpts{ //nolint:promlinter // name pinned by plan.md 24
			Namespace: Namespace,
			Name:      "bytes_in_total",
			Help:      "Total source bytes of every promoted file.",
		}),

		bytesOut: prometheus.NewGauge(prometheus.GaugeOpts{ //nolint:promlinter // name pinned by plan.md 24
			Namespace: Namespace,
			Name:      "bytes_out_total",
			Help:      "Total output bytes of every promoted file.",
		}),

		// Legitimately negative: an AV1 source re-encoded to HEVC grows, by
		// design (plan.md 6.2), so this cannot be a counter.
		bytesSaved: prometheus.NewGauge(prometheus.GaugeOpts{ //nolint:promlinter // name pinned by plan.md 24
			Namespace: Namespace,
			Name:      "bytes_saved_total",
			Help:      "Source bytes minus output bytes. Negative when the library grew.",
		}),

		plexUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "plex_up",
			Help:      "1 when the configured Plex server answered the last check.",
		}),

		plexActiveSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "plex_active_sessions",
			Help:      "Sessions Plex reported playing at the last check.",
		}),

		arrUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "arr_up",
			Help:      "1 when an enabled *arr instance answered the last check.",
		}, []string{"instance"}),
	}

	reg.MustRegister(
		m.jobsTotal, m.jobsFailedTotal, m.jobsRequeuedTotal,
		m.encoderFallback, m.decodeFallback, m.errorsTotal,
		m.transcodeDuration, m.estimateError,
		m.queueDepth, m.awaitingStreamEnd, m.filesByPlanKind,
		m.bytesIn, m.bytesOut, m.bytesSaved,
		m.plexUp, m.plexActiveSessions, m.arrUp,
	)

	return m
}

// durationBuckets span a fast remux through a software-encoder fallback, which
// turns a 20-minute job into a 4-hour one.
func durationBuckets() []float64 {
	return []float64{10, 30, 60, 300, 900, 1800, 3600, 7200, 14400, 28800}
}

// estimateErrorBuckets are symmetric: an estimate can be wrong in either
// direction and the sign is the interesting part.
func estimateErrorBuckets() []float64 {
	return []float64{-7200, -3600, -1800, -600, -120, 0, 120, 600, 1800, 3600, 7200}
}

// Registry exposes the registry, for a test that wants to gather.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler serves /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// JobObserved records one job entering a state.
func (m *Metrics) JobObserved(state domain.JobState, kind domain.Kind, origin domain.JobOrigin) {
	m.jobsTotal.WithLabelValues(string(state), string(kind), string(origin)).Inc()
}

// JobFailed records a failure by its machine-readable code.
func (m *Metrics) JobFailed(code domain.FailureCode) {
	m.jobsFailedTotal.WithLabelValues(string(code)).Inc()
}

// JobRequeued records an automatic re-queue after an interruption.
func (m *Metrics) JobRequeued() { m.jobsRequeuedTotal.Inc() }

// EncoderFallback records a step down the encoder chain.
func (m *Metrics) EncoderFallback(from, to domain.Encoder) {
	m.encoderFallback.WithLabelValues(string(from), string(to)).Inc()
}

// DecodeFallback records the one software-decode retry of plan.md 10.1.
func (m *Metrics) DecodeFallback() { m.decodeFallback.Inc() }

// Error records one error against a category.
func (m *Metrics) Error(category string) { m.errorsTotal.WithLabelValues(category).Inc() }

// TranscodeDuration records how long a completed job took.
func (m *Metrics) TranscodeDuration(kind domain.Kind, encoder domain.Encoder, seconds float64) {
	m.transcodeDuration.WithLabelValues(string(kind), string(encoder)).Observe(seconds)
}

// EstimateError records actual minus estimated seconds.
func (m *Metrics) EstimateError(seconds float64) { m.estimateError.Observe(seconds) }

// SetQueueDepth sets the number of queued jobs.
func (m *Metrics) SetQueueDepth(n int) { m.queueDepth.Set(float64(n)) }

// SetAwaitingStreamEnd sets the number of deferred jobs.
func (m *Metrics) SetAwaitingStreamEnd(n int) { m.awaitingStreamEnd.Set(float64(n)) }

// SetFilesByPlanKind replaces the library breakdown. Every kind is written on
// every refresh, including the zeroes, so a kind that empties reports zero
// rather than freezing at its last non-zero value.
func (m *Metrics) SetFilesByPlanKind(counts map[domain.Kind]int) {
	for _, k := range []domain.Kind{domain.KindSkip, domain.KindRemux, domain.KindAudioOnly, domain.KindFull} {
		m.filesByPlanKind.WithLabelValues(string(k)).Set(float64(counts[k]))
	}
}

// SetBytes sets the three byte totals. Saved can be negative.
func (m *Metrics) SetBytes(in, out, saved int64) {
	m.bytesIn.Set(float64(in))
	m.bytesOut.Set(float64(out))
	m.bytesSaved.Set(float64(saved))
}

// SetPlex records whether Plex answered and how many sessions it reported.
func (m *Metrics) SetPlex(up bool, sessions int) {
	m.plexUp.Set(boolValue(up))
	m.plexActiveSessions.Set(float64(sessions))
}

// SetArrUp records whether one *arr instance answered.
func (m *Metrics) SetArrUp(instance string, up bool) {
	m.arrUp.WithLabelValues(instance).Set(boolValue(up))
}

// ForgetArr drops an instance's series, so deleting an instance in the UI stops
// it reporting forever.
func (m *Metrics) ForgetArr(instance string) { m.arrUp.DeleteLabelValues(instance) }

func boolValue(b bool) float64 {
	if b {
		return 1
	}

	return 0
}
