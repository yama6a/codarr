package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/api"
	"github.com/yama6a/codarr/internal/api/mock"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
)

// The handler tests run against the generated router with moq'd services
// (plan.md 2.3), so the routing, the parameter binding and the response codes
// are exercised rather than the method bodies alone.

var testNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	store    *mock.StoreMock
	queue    *mock.QueueMock
	analyzer *mock.AnalyzerMock
	scanner  *mock.ScannerMock
	webhooks *mock.WebhooksMock
	hardware *mock.HardwareMock
	fp       *mock.FingerprinterMock
	fs       *mock.FSMock
	db       *mock.PingerMock
	plexAuth *mock.PlexAuthMock
	plex     *mock.PlexClientMock
	arr      *mock.ArrClientMock
	handler  http.Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		store:    &mock.StoreMock{},
		queue:    &mock.QueueMock{},
		analyzer: &mock.AnalyzerMock{},
		scanner:  &mock.ScannerMock{},
		webhooks: &mock.WebhooksMock{},
		hardware: &mock.HardwareMock{},
		fp:       &mock.FingerprinterMock{},
		fs:       &mock.FSMock{},
		db:       &mock.PingerMock{PingContextFunc: func(context.Context) error { return nil }},
		plexAuth: &mock.PlexAuthMock{},
		plex:     &mock.PlexClientMock{},
		arr:      &mock.ArrClientMock{},
	}

	server := api.New(api.Deps{
		Store:         h.store,
		DB:            h.db,
		Queue:         h.queue,
		Analyzer:      h.analyzer,
		Scanner:       h.scanner,
		Webhooks:      h.webhooks,
		Hardware:      h.hardware,
		Fingerprinter: h.fp,
		FS:            h.fs,
		PlexAuth:      h.plexAuth,
		PlexFactory:   func(context.Context) (api.PlexClient, error) { return h.plex, nil },
		ArrFactory: func(context.Context, domain.ArrInstance) (api.ArrClient, error) {
			return h.arr, nil
		},
		Clock:  clock.NewFake(testNow),
		Logger: slog.New(slog.DiscardHandler),
		Build:  api.Build{Version: "test", Commit: "abc123", GoVersion: "go1.27.0"},
	})

	h.handler = server.Router(chi.NewRouter())

	return h
}

// withRoots is the usual starting point: one enabled root, so path validation
// has something to validate against.
func (h *harness) withRoots(paths ...string) *harness {
	roots := make([]domain.Root, 0, len(paths))
	for i, p := range paths {
		roots = append(roots, domain.Root{ID: int64(i + 1), Path: p, Enabled: true})
	}

	h.store.ListRootsFunc = func(context.Context) ([]domain.Root, error) { return roots, nil }

	return h
}

func (h *harness) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	return rec
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()

	require.Equal(t, want, rec.Code, rec.Body.String())

	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))

	return out
}

func noInstances(s *mock.StoreMock) {
	s.ListArrInstancesFunc = func(context.Context) ([]domain.ArrInstance, error) {
		return nil, nil
	}
}

func noJobs(s *mock.StoreMock) {
	s.ListJobsFunc = func(context.Context, store.JobFilter) ([]domain.Job, int, error) {
		return nil, 0, nil
	}
}

// plexPin is an alias so the mock's plex.Pin signature reads without a second
// import in every test file.
type plexPin = plex.Pin

func decodeJSON(raw []byte, into any) error {
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// ingestReport keeps the scanner mock's signature readable in the tests.
type ingestReport = ingest.ScanReport
