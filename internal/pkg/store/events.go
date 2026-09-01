package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const (
	eventColumns = `id, level, category, message, media_file_id, job_id, created_at`
	// Whichever of the two bites harder wins. See plan.md 24.
	eventRetention = 30 * 24 * time.Hour
	eventMaxRows   = 100_000
)

// AppendEvent returns its error rather than swallowing it, so the caller can still
// emit the stdout line that plan.md 24 makes the source of truth.
func (s *store) AppendEvent(ctx context.Context, e domain.Event) (int64, error) {
	const query = `
		INSERT INTO events (level, category, message, media_file_id, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING id`

	var id int64

	err := s.db.write.QueryRowContext(ctx, query, e.Level, e.Category, e.Message,
		ptrValue(e.MediaFileID), ptrValue(e.JobID), formatTime(e.CreatedAt)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}

	return id, nil
}

// ListEvents is the cursor read of plan.md 18.5: everything after SinceID, in
// insertion order, so the UI can append rather than re-render.
func (s *store) ListEvents(ctx context.Context, f EventFilter) ([]domain.Event, error) {
	var c conds

	c.add("id > ?", f.SinceID)
	c.in("level", f.Level)
	c.in("category", f.Category)

	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}

	//nolint:gosec // the only interpolation is placeholder lists built from constants
	query := `SELECT ` + eventColumns + ` FROM events` + c.where() + ` ORDER BY id ASC LIMIT ?`

	rows, err := s.db.read.QueryContext(ctx, query, append(c.args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("select events: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.Event{}

	for rows.Next() {
		var (
			e         domain.Event
			mediaID   sql.NullInt64
			jobID     sql.NullInt64
			createdAt sql.NullString
		)

		if err := rows.Scan(&e.ID, &e.Level, &e.Category, &e.Message, &mediaID, &jobID,
			&createdAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		e.MediaFileID = int64Ptr(mediaID)
		e.JobID = int64Ptr(jobID)

		if e.CreatedAt, err = scanTime(createdAt); err != nil {
			return nil, err
		}

		out = append(out, e)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

// PruneEvents applies both bounds of plan.md 24 and returns how many rows went.
// Whichever bound bites harder wins, which is what "the smaller of" means.
func (s *store) PruneEvents(ctx context.Context, now time.Time) (int64, error) {
	var deleted int64

	err := s.write(ctx, func(tx *sql.Tx) error {
		const byAge = `DELETE FROM events WHERE created_at < ?`

		res, err := tx.ExecContext(ctx, byAge, formatTime(now.Add(-eventRetention)))
		if err != nil {
			return fmt.Errorf("prune events by age: %w", err)
		}

		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}

		deleted += n

		const byCount = `
			DELETE FROM events WHERE id <= (
				SELECT id FROM events ORDER BY id DESC LIMIT 1 OFFSET ?
			)`

		res, err = tx.ExecContext(ctx, byCount, eventMaxRows)
		if err != nil {
			return fmt.Errorf("prune events by count: %w", err)
		}

		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}

		deleted += n

		return nil
	})
	if err != nil {
		return 0, err
	}

	return deleted, nil
}
