package metrics_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/metrics"
	"github.com/yama6a/codarr/internal/pkg/store"
)

var errSourceDown = errors.New("database is unavailable")

type fakeSource struct {
	jobs  map[domain.JobState]int
	kinds map[domain.Kind]int
	stats store.Stats
	err   error
}

func (f fakeSource) CountJobsByState(context.Context) (map[domain.JobState]int, error) {
	return f.jobs, f.err
}

func (f fakeSource) CountMediaByPlanKind(context.Context) (map[domain.Kind]int, error) {
	return f.kinds, f.err
}

func (f fakeSource) Stats(context.Context) (store.Stats, error) { return f.stats, f.err }

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	require.Equal(t, 200, rec.Code)

	return rec.Body.String()
}

// plan.md 24 names every series exactly; the UI and the dashboards are written
// against these names, so a rename is a breaking change.
func TestMetrics_ExposesEverySeriesSection24Names(t *testing.T) {
	t.Parallel()

	m := metrics.New()

	m.JobObserved(domain.JobQueued, domain.KindFull, domain.OriginManual)
	m.JobFailed(domain.FailFfmpeg)
	m.JobRequeued()
	m.EncoderFallback(domain.EncoderQSV, domain.EncoderVAAPI)
	m.DecodeFallback()
	m.Error("upstream_error")
	m.TranscodeDuration(domain.KindFull, domain.EncoderQSV, 1200)
	m.EstimateError(-300)
	m.SetQueueDepth(4)
	m.SetAwaitingStreamEnd(1)
	m.SetFilesByPlanKind(map[domain.Kind]int{domain.KindFull: 12})
	m.SetBytes(100, 40, 60)
	m.SetPlex(true, 2)
	m.SetArrUp("radarr-4k", true)

	body := scrape(t, m)

	for _, want := range []string{
		`codarr_jobs_total{kind="full",origin="manual",state="queued"} 1`,
		`codarr_queue_depth 4`,
		`codarr_transcode_duration_seconds_bucket{encoder="hevc_qsv",kind="full",le="1800"} 1`,
		`codarr_transcode_estimate_error_seconds_sum -300`,
		`codarr_bytes_in_total 100`,
		`codarr_bytes_out_total 40`,
		`codarr_bytes_saved_total 60`,
		`codarr_encoder_fallback_total{from="hevc_qsv",to="hevc_vaapi"} 1`,
		`codarr_decode_fallback_total 1`,
		`codarr_files_by_plan_kind{kind="full"} 12`,
		`codarr_plex_up 1`,
		`codarr_plex_active_sessions 2`,
		`codarr_arr_up{instance="radarr-4k"} 1`,
		`codarr_jobs_awaiting_stream_end 1`,
		`codarr_jobs_failed_total{failure_code="ffmpeg_failed"} 1`,
		`codarr_jobs_requeued_total 1`,
		`codarr_errors_total{category="upstream_error"} 1`,
	} {
		require.Contains(t, body, want)
	}
}

// plan.md 6.2: an AV1 source re-encoded to HEVC grows, by design, so the saved
// total can legitimately go negative and cannot be a counter.
func TestMetrics_BytesSavedGoesNegative(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.SetBytes(100, 160, -60)

	require.Contains(t, scrape(t, m), "codarr_bytes_saved_total -60")
}

func TestMetrics_PlanKindGaugeReportsZeroesForEmptyKinds(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.SetFilesByPlanKind(map[domain.Kind]int{domain.KindRemux: 3, domain.KindFull: 1})
	m.SetFilesByPlanKind(map[domain.Kind]int{domain.KindRemux: 3})

	body := scrape(t, m)
	require.Contains(t, body, `codarr_files_by_plan_kind{kind="remux"} 3`)
	require.Contains(t, body, `codarr_files_by_plan_kind{kind="full"} 0`)
	require.Contains(t, body, `codarr_files_by_plan_kind{kind="skip"} 0`)
}

func TestMetrics_ForgetArrDropsTheSeries(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.SetArrUp("sonarr-anime", false)
	require.Contains(t, scrape(t, m), `codarr_arr_up{instance="sonarr-anime"}`)

	m.ForgetArr("sonarr-anime")
	require.NotContains(t, scrape(t, m), `instance="sonarr-anime"`)
}

func TestRefresher_SetsGaugesFromTheStore(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	src := fakeSource{
		jobs:  map[domain.JobState]int{domain.JobQueued: 5, domain.JobAwaitingStreamEnd: 2},
		kinds: map[domain.Kind]int{domain.KindSkip: 900, domain.KindAudioOnly: 7},
		stats: store.Stats{BytesIn: 900, BytesOut: 500, BytesSaved: 400},
	}

	probed := false
	probe := func(context.Context, *metrics.Metrics) { probed = true }

	metrics.NewRefresher(m, src, clock.System(), slog.New(slog.DiscardHandler), 0, probe).
		Refresh(t.Context())

	body := scrape(t, m)
	require.Contains(t, body, "codarr_queue_depth 5")
	require.Contains(t, body, "codarr_jobs_awaiting_stream_end 2")
	require.Contains(t, body, `codarr_files_by_plan_kind{kind="skip"} 900`)
	require.Contains(t, body, `codarr_files_by_plan_kind{kind="audio_only"} 7`)
	require.Contains(t, body, "codarr_bytes_saved_total 400")
	require.True(t, probed)
}

// A refresh that cannot read the store keeps the last known values rather than
// zeroing them, so a scrape gap does not look like an empty library.
func TestRefresher_StoreFailureKeepsTheLastValues(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.SetQueueDepth(11)

	metrics.NewRefresher(m, fakeSource{err: errSourceDown}, clock.System(),
		slog.New(slog.DiscardHandler), 0).Refresh(t.Context())

	require.Contains(t, scrape(t, m), "codarr_queue_depth 11")
}

func TestMetrics_HandlerServesTheGoCollectorToo(t *testing.T) {
	t.Parallel()

	require.Contains(t, scrape(t, metrics.New()), "go_goroutines")
}
