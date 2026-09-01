package store_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

func TestEventStore_AppendAndCursorRead(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	media := seedMedia(t, s, "/library/movies/logged.mkv")

	first, err := s.AppendEvent(t.Context(), domain.Event{
		Level: "info", Category: "queue", Message: "queued",
		MediaFileID: &media.ID, CreatedAt: testTime(),
	})
	require.NoError(t, err)

	second, err := s.AppendEvent(t.Context(), domain.Event{
		Level: "error", Category: "ffmpeg", Message: "exited 1", CreatedAt: testTime(),
	})
	require.NoError(t, err)
	require.Greater(t, second, first)

	all, err := s.ListEvents(t.Context(), store.EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []domain.Event{
		{ID: first, Level: "info", Category: "queue", Message: "queued", MediaFileID: &media.ID, CreatedAt: testTime()},
		{ID: second, Level: "error", Category: "ffmpeg", Message: "exited 1", CreatedAt: testTime()},
	}, all)

	after, err := s.ListEvents(t.Context(), store.EventFilter{SinceID: first})
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, second, after[0].ID)

	errorsOnly, err := s.ListEvents(t.Context(), store.EventFilter{Level: []string{"error"}})
	require.NoError(t, err)
	require.Len(t, errorsOnly, 1)
	require.Equal(t, second, errorsOnly[0].ID)

	byCategory, err := s.ListEvents(t.Context(), store.EventFilter{Category: []string{"queue"}})
	require.NoError(t, err)
	require.Len(t, byCategory, 1)
	require.Equal(t, first, byCategory[0].ID)
}

// TestEventStore_AppendReportsItsFailure: the events table is a convenience for
// the UI and stdout is the source of truth (plan.md 24), so a write failure has
// to come back to the caller rather than be swallowed here.
func TestEventStore_AppendReportsItsFailure(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	_, err := db.Writer().ExecContext(t.Context(), `DROP TABLE events`)
	require.NoError(t, err)

	_, err = s.AppendEvent(t.Context(), domain.Event{
		Level: "info", Category: "queue", Message: "queued", CreatedAt: testTime(),
	})
	require.Error(t, err)
}

func TestEventStore_PruneDropsRowsOlderThan30Days(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)
	now := testTime()

	old, err := s.AppendEvent(t.Context(), domain.Event{
		Level: "info", Category: "scan", Message: "ancient",
		CreatedAt: now.Add(-31 * 24 * time.Hour),
	})
	require.NoError(t, err)

	recent, err := s.AppendEvent(t.Context(), domain.Event{
		Level: "info", Category: "scan", Message: "recent",
		CreatedAt: now.Add(-29 * 24 * time.Hour),
	})
	require.NoError(t, err)

	deleted, err := s.PruneEvents(t.Context(), now)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	remaining, err := s.ListEvents(t.Context(), store.EventFilter{})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, recent, remaining[0].ID)
	require.NotEqual(t, old, remaining[0].ID)
}

func TestEventStore_PruneKeepsTheRowCapWhenNothingIsOldEnough(t *testing.T) {
	t.Parallel()

	db := storetest.NewRawDB(t)
	s := storetest.NewStore(t, db)

	// 100k rows through AppendEvent would dominate the suite; the cap is the
	// same DELETE either way, so seed past it directly.
	const overflow = 5

	_, err := db.Writer().ExecContext(t.Context(), `
		WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 100005)
		INSERT INTO events (level, category, message, created_at)
		SELECT 'info', 'scan', 'row ' || n, ? FROM seq`, formatForTest(testTime()))
	require.NoError(t, err)

	deleted, err := s.PruneEvents(t.Context(), testTime())
	require.NoError(t, err)
	require.Equal(t, int64(overflow), deleted)

	var remaining int
	require.NoError(t, db.Reader().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events`).Scan(&remaining))
	require.Equal(t, 100_000, remaining)
}

func formatForTest(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func TestEventStore_ListRespectsTheLimit(t *testing.T) {
	t.Parallel()

	s := storetest.NewDB(t)

	for i := range 5 {
		_, err := s.AppendEvent(t.Context(), domain.Event{
			Level: "info", Category: "scan", Message: "row " + strconv.Itoa(i), CreatedAt: testTime(),
		})
		require.NoError(t, err)
	}

	page, err := s.ListEvents(t.Context(), store.EventFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page, 2)
}
