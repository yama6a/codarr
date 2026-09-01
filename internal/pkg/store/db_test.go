package store_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

func TestDB_PragmasAreSetOnBothPools(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)

	var journal string
	require.NoError(t, db.Writer().QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal))
	require.Equal(t, "wal", journal)

	for _, write := range []bool{true, false} {
		fk, busy := pragmaPair(t, db, write)
		require.Equal(t, 1, fk, "foreign_keys, write pool: %v", write)
		require.Equal(t, 5000, busy, "busy_timeout, write pool: %v", write)
	}
}

func pragmaPair(t *testing.T, db *store.DB, write bool) (int, int) {
	t.Helper()

	pool := db.Reader()
	if write {
		pool = db.Writer()
	}

	var fk, busy int

	require.NoError(t, pool.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk))
	require.NoError(t, pool.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busy))

	return fk, busy
}

// The read pool's split is enforced by SQLite rather than by convention: a write that
// reached it fails loudly instead of racing the worker for the write lock.
func TestDB_ReadPoolIsQueryOnly(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)

	_, err := db.Reader().ExecContext(t.Context(),
		`INSERT INTO events (level, category, message, created_at) VALUES ('info', 'test', 'x', '2026-01-01T00:00:00.000000000Z')`)
	require.Error(t, err)
}

// The integration test plan.md 17 asks for: without the single-connection write pool,
// sixteen concurrent writers are where SQLITE_BUSY shows up.
func TestDB_ConcurrentWritesNeverReturnBusy(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	const (
		writers        = 16
		writesPerGroup = 10
	)

	media := make([]domain.MediaFile, writers)
	for i := range writers {
		media[i] = seedMedia(t, s, "/library/movies/concurrent-"+strconv.Itoa(i)+".mkv")
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	record := func(err error) {
		if err == nil {
			return
		}

		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for i := range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for n := range writesPerGroup {
				_, err := s.AppendEvent(t.Context(), domain.Event{
					Level:       "info",
					Category:    "test",
					Message:     "write " + strconv.Itoa(i) + "/" + strconv.Itoa(n),
					MediaFileID: &media[i].ID,
					CreatedAt:   testTime(),
				})
				record(err)

				record(s.SetMediaStatus(t.Context(), media[i].ID, domain.MediaAnalyzed, ""))
			}
		}()
	}

	for range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range writesPerGroup {
				_, _, err := s.ListMediaFiles(t.Context(), store.MediaFilter{Limit: 100})
				record(err)
			}
		}()
	}

	wg.Wait()

	require.Empty(t, errs)

	events, err := s.ListEvents(t.Context(), store.EventFilter{Limit: 1000})
	require.NoError(t, err)
	require.Len(t, events, writers*writesPerGroup)
}

func TestDB_MigrationsAreIdempotent(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)

	require.NoError(t, store.Migrate(db, storetest.Logger()))
}
