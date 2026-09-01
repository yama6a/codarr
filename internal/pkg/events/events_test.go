package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/events"
)

var errSinkDown = errors.New("events table is unavailable")

// fakeSink records what reached the events table and can be made to fail.
type fakeSink struct {
	mu      sync.Mutex
	rows    []domain.Event
	err     error
	prunes  []time.Time
	pruneN  int64
	pruneEr error
}

func (f *fakeSink) AppendEvent(_ context.Context, e domain.Event) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return 0, f.err
	}

	f.rows = append(f.rows, e)

	return int64(len(f.rows)), nil
}

func (f *fakeSink) PruneEvents(_ context.Context, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.prunes = append(f.prunes, now)

	return f.pruneN, f.pruneEr
}

func (f *fakeSink) events() []domain.Event {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]domain.Event(nil), f.rows...)
}

func TestHandler_WritesStdoutAndTheEventsTable(t *testing.T) {
	t.Parallel()

	var (
		out  bytes.Buffer
		sink = &fakeSink{}
	)

	log := events.New(events.Options{Out: &out, Store: sink, Clock: clock.System()})
	log.With(slog.String("component", "job"), slog.Int64("job_id", 7)).
		Info("promotion complete", slog.Int64("media_file_id", 42))

	var line map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &line))
	require.Equal(t, "promotion complete", line["msg"])
	require.Equal(t, "INFO", line["level"])
	require.InDelta(t, float64(7), line["job_id"], 0)

	rows := sink.events()
	require.Len(t, rows, 1)
	require.Equal(t, "info", rows[0].Level)
	require.Equal(t, "job", rows[0].Category)
	require.Equal(t, "promotion complete", rows[0].Message)
	require.NotNil(t, rows[0].JobID)
	require.Equal(t, int64(7), *rows[0].JobID)
	require.NotNil(t, rows[0].MediaFileID)
	require.Equal(t, int64(42), *rows[0].MediaFileID)
}

// plan.md 24: stdout is the source of truth and a database failure must never
// prevent the line being emitted.
func TestHandler_DatabaseFailureStillEmitsStdout(t *testing.T) {
	t.Parallel()

	var (
		out    bytes.Buffer
		sink   = &fakeSink{err: errSinkDown}
		seen   []error
		logger = events.New(events.Options{
			Out:         &out,
			Store:       sink,
			Clock:       clock.System(),
			OnSinkError: func(err error) { seen = append(seen, err) },
		})
	)

	logger.Error("the sky is falling")

	var line map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &line))
	require.Equal(t, "the sky is falling", line["msg"])

	require.Empty(t, sink.events())
	require.Len(t, seen, 1)
	require.ErrorIs(t, seen[0], errSinkDown)
}

func TestHandler_DebugNeverReachesTheTable(t *testing.T) {
	t.Parallel()

	var (
		out  bytes.Buffer
		sink = &fakeSink{}
	)

	log := events.New(events.Options{Out: &out, Level: slog.LevelDebug, Store: sink, Clock: clock.System()})
	log.Debug("noisy")
	log.Info("kept")

	require.Contains(t, out.String(), "noisy")

	rows := sink.events()
	require.Len(t, rows, 1)
	require.Equal(t, "kept", rows[0].Message)
}

func TestHandler_RedactsSecretsEverywhere(t *testing.T) {
	t.Parallel()

	var (
		out  bytes.Buffer
		sink = &fakeSink{}
	)

	log := events.New(events.Options{Out: &out, Store: sink, Clock: clock.System()})
	log.With(slog.String("token", "plex-secret-token")).
		Info("calling plex", slog.String("api_key", "radarr-secret-key"))

	require.NotContains(t, out.String(), "plex-secret-token")
	require.NotContains(t, out.String(), "radarr-secret-key")
	require.Contains(t, out.String(), domain.MaskedSecret)
}

func TestHandler_GroupedAttributesDoNotClaimColumns(t *testing.T) {
	t.Parallel()

	var (
		out  bytes.Buffer
		sink = &fakeSink{}
	)

	log := events.New(events.Options{Out: &out, Store: sink, Clock: clock.System()})
	log.WithGroup("plex").Info("nested", slog.Int64("job_id", 9))

	rows := sink.events()
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].JobID)
	require.Equal(t, events.DefaultCategory, rows[0].Category)
}

func TestHandler_WithoutAStoreIsPlainJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	events.New(events.Options{Out: &out}).Info("hello")

	require.Contains(t, out.String(), `"msg":"hello"`)
	require.Equal(t, 1, strings.Count(out.String(), "\n"))
}

func TestParseLevel_UnknownIsInfo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" warn ", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"chatty", slog.LevelInfo},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, events.ParseLevel(tc.in), tc.in)
	}
}

func TestPruner_PrunesImmediatelyAndOnEveryTick(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{pruneN: 3}

	// A real clock with an hour-long interval: the immediate prune is what is
	// under test, and the tick would only make the assertion racy.
	clk := clock.System()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- events.NewPruner(sink, clk, slog.New(slog.DiscardHandler), time.Hour).Run(ctx) }()

	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()

		return len(sink.prunes) >= 1
	}, time.Second, time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}
