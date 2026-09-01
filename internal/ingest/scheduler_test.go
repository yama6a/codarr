package ingest_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/ingest/mock"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// countingStore cancels once settings have been read enough times; clock.Fake's After
// fires immediately, so the loop runs at full speed and nothing sleeps.
func countingStore(t *testing.T, cancel context.CancelFunc, reads *atomic.Int64,
	after int64, s domain.Settings,
) *mock.ScanStoreMock {
	t.Helper()

	return &mock.ScanStoreMock{
		GetSettingsFunc: func(context.Context) (domain.Settings, error) {
			if reads.Add(1) >= after {
				cancel()
			}

			return s, nil
		},
		ListRootsFunc: func(context.Context) ([]domain.Root, error) {
			return []domain.Root{}, nil
		},
		ListMediaStatsByRootFunc: func(context.Context, int64) ([]store.MediaStat, error) {
			return nil, nil
		},
		GetRootFunc: func(_ context.Context, id int64) (domain.Root, error) {
			return domain.Root{ID: id}, nil
		},
		MarkMediaMissingFunc: func(context.Context, []int64) (int64, error) { return 0, nil },
	}
}

func TestScheduler_RunFiresTheScanAtTheCronTime(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var reads atomic.Int64

	// 03:00 with a 04:00 cron: one hour away, so the first wake is a recheck
	// rather than a scan.
	clk := clock.NewFake(time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC))
	st := countingStore(t, cancel, &reads, 1000, settings())
	st.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		cancel()

		return []domain.Root{}, nil
	}

	an, seen := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))

	require.Empty(t, *seen, "no roots, so nothing to walk")
	require.Equal(t, time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC), clk.Now(),
		"the loop walked forward a minute at a time and stopped on the hour")
	require.Len(t, st.ListRootsCalls(), 1, "the scan ran once, at 04:00")
}

func TestScheduler_RunSleepsWhenScanningIsDisabled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var reads atomic.Int64

	off := settings()
	off.ScanEnabled = false

	clk := clock.NewFake(now)
	st := countingStore(t, cancel, &reads, 3, off)

	an, _ := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))

	require.Empty(t, st.ListRootsCalls())
	require.Equal(t, now.Add(3*ingest.RecheckInterval), clk.Now())
}

func TestScheduler_RunKeepsRetryingAnUnreadableCron(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var reads atomic.Int64

	bad := settings()
	bad.ScanCron = "not a cron"

	clk := clock.NewFake(now)
	st := countingStore(t, cancel, &reads, 2, bad)

	an, _ := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))

	require.Empty(t, st.ListRootsCalls(), "a broken schedule never fires a scan")
}

func TestScheduler_RunKeepsRetryingWhenSettingsCannotBeRead(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var reads atomic.Int64

	clk := clock.NewFake(now)
	st := countingStore(t, cancel, &reads, 2, settings())
	st.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		if reads.Add(1) >= 2 {
			cancel()
		}

		return domain.Settings{}, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))
	require.Empty(t, st.ListRootsCalls())
}

func TestScheduler_RunSleepsWhenTheCronMatchesNothing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var reads atomic.Int64

	impossible := settings()
	impossible.ScanCron = "0 4 30 2 *"

	clk := clock.NewFake(now)
	st := countingStore(t, cancel, &reads, 2, impossible)

	an, _ := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))
	require.Empty(t, st.ListRootsCalls())
}

func TestScheduler_RunStopsOnAnAlreadyCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	clk := clock.NewFake(now)

	var reads atomic.Int64

	st := countingStore(t, cancel, &reads, 1000, settings())

	an, _ := recordingAnalyzer()
	scanner := ingest.NewScanner(newTree(moviesRoot).fs(), st, an, clk, discardLogger())

	require.NoError(t, ingest.NewScheduler(st, scanner, clk, discardLogger()).Run(ctx))
	require.Empty(t, st.ListRootsCalls())
}
