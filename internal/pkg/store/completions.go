package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// ListCompletions merges done jobs with skipped analyses, newest first, because a
// file the policy already accepts is a completion the dashboard should show too.
func (s *store) ListCompletions(ctx context.Context, limit, offset int) ([]domain.Completion, int, error) {
	const countQuery = `
		SELECT (SELECT COUNT(*) FROM jobs WHERE state = ?)
		     + (SELECT COUNT(*) FROM media_files WHERE status = ? AND analyzed_at IS NOT NULL)`

	var total int
	if err := s.db.read.QueryRowContext(ctx, countQuery,
		string(domain.JobDone), string(domain.MediaSkipped)).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count completions: %w", err)
	}

	if limit <= 0 {
		limit = 50
	}

	const query = `
		SELECT j.id, m.id, m.path, j.kind, 0, COALESCE(j.source_size, 0), COALESCE(j.output_size, 0),
		       COALESCE(j.actual_seconds, 0), j.fell_back, j.finished_at AS at
		FROM jobs j JOIN media_files m ON m.id = j.media_file_id
		WHERE j.state = ?
		UNION ALL
		SELECT NULL, m.id, m.path, COALESCE(m.plan_kind, ''), 1, 0, 0, 0, 0, m.analyzed_at AS at
		FROM media_files m
		WHERE m.status = ? AND m.analyzed_at IS NOT NULL
		ORDER BY at DESC, 2 DESC LIMIT ? OFFSET ?`

	rows, err := s.db.read.QueryContext(ctx, query,
		string(domain.JobDone), string(domain.MediaSkipped), limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select completions: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.Completion{}

	for rows.Next() {
		var (
			c     domain.Completion
			jobID sql.NullInt64
			kind  string
			at    sql.NullString
		)

		if err := rows.Scan(&jobID, &c.MediaFileID, &c.Path, &kind, &c.Skipped, &c.SourceSize,
			&c.OutputSize, &c.ActualSeconds, &c.FellBack, &at); err != nil {
			return nil, 0, fmt.Errorf("scan completion: %w", err)
		}

		c.JobID = int64Ptr(jobID)
		c.Kind = domain.Kind(kind)

		if c.At, err = scanTime(at); err != nil {
			return nil, 0, err
		}

		out = append(out, c)
	}

	if err := closeRows(rows); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}
