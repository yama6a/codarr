package events_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/events"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

// newObserved builds the same two-core tee events.New builds, with the console core
// replaced by an observer so the fields it received can be asserted on.
func newObserved(t *testing.T, sink events.Store, onErr func(error)) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zapcore.DebugLevel)
	tee := zapcore.NewTee(
		events.Redacting(core),
		events.NewStoreCore(sink, clock.System(), zapcore.DebugLevel, onErr),
	)

	return zap.New(tee), logs
}

func TestStoreCore_WritesConsoleAndTheEventsTable(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	log, logs := newObserved(t, sink, func(error) {})

	log.With(zap.String("component", "job"), zap.Int64("job_id", 7)).
		Info("promotion complete", zap.Int64("media_file_id", 42))

	entries := logs.All()
	require.Len(t, entries, 1)
	require.Equal(t, "promotion complete", entries[0].Message)
	require.Equal(t, zapcore.InfoLevel, entries[0].Level)
	require.Equal(t, int64(7), entries[0].ContextMap()["job_id"])

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
func TestStoreCore_DatabaseFailureStillEmitsConsole(t *testing.T) {
	t.Parallel()

	var (
		seen []error
		sink = &fakeSink{err: errSinkDown}
	)

	log, logs := newObserved(t, sink, func(err error) { seen = append(seen, err) })
	log.Error("the sky is falling")

	require.Equal(t, 1, logs.FilterMessage("the sky is falling").Len())
	require.Empty(t, sink.events())
	require.Len(t, seen, 1)
	require.ErrorIs(t, seen[0], errSinkDown)
}

func TestStoreCore_DebugNeverReachesTheTable(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	log, logs := newObserved(t, sink, func(error) {})

	log.Debug("noisy")
	log.Info("kept")

	require.Equal(t, 1, logs.FilterMessage("noisy").Len())

	rows := sink.events()
	require.Len(t, rows, 1)
	require.Equal(t, "kept", rows[0].Message)
}

func TestRedacting_MasksSecretsInBothSinks(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	log, logs := newObserved(t, sink, func(error) {})

	log.With(zap.String("token", "plex-secret-token")).
		Info("calling plex", zap.String("api_key", "radarr-secret-key"))

	fields := logs.All()[0].ContextMap()
	require.Equal(t, domain.MaskedSecret, fields["token"])
	require.Equal(t, domain.MaskedSecret, fields["api_key"])
}

// A secret nested under a map is still a secret, so redaction walks into it.
func TestRedacting_MasksNestedSecrets(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	log, logs := newObserved(t, sink, func(error) {})

	log.Info("calling radarr", zap.Any("headers", map[string]any{
		"X-Api-Key": "radarr-secret-key",
		"Accept":    "application/json",
	}))

	headers, ok := logs.All()[0].ContextMap()["headers"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, domain.MaskedSecret, headers["X-Api-Key"])
	require.Equal(t, "application/json", headers["Accept"])
}

func TestStoreCore_NamespacedFieldsDoNotClaimColumns(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{}
	log, _ := newObserved(t, sink, func(error) {})

	log.With(zap.Namespace("plex")).Info("nested", zap.Int64("job_id", 9))

	rows := sink.events()
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].JobID)
	require.Equal(t, events.DefaultCategory, rows[0].Category)
}

func TestNew_WithoutAStoreIsPlainJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	events.New(events.Options{Out: &out}).Info("hello")

	require.Contains(t, out.String(), `"msg":"hello"`)
	require.Equal(t, 1, strings.Count(out.String(), "\n"))
}

func TestPruner_PrunesImmediatelyAndOnEveryTick(t *testing.T) {
	t.Parallel()

	sink := &fakeSink{pruneN: 3}

	// A real clock with an hour-long interval: the immediate prune is what is
	// under test, and the tick would only make the assertion racy.
	clk := clock.System()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- events.NewPruner(sink, clk, zap.NewNop(), time.Hour).Run(ctx) }()

	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()

		return len(sink.prunes) >= 1
	}, time.Second, time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}
