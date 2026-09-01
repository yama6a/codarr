package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const throughputColumns = `id, kind, encoder, resolution, samples, avg_value, updated_at`

// UpsertThroughputStat keys on (kind, encoder, resolution), matched through COALESCE
// because encoder and resolution are NULL for audio_only and remux (migration 002).
func (s *store) UpsertThroughputStat(ctx context.Context, st domain.ThroughputStat) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		const update = `
			UPDATE throughput_stats SET samples = ?, avg_value = ?, updated_at = ?
			WHERE kind = ? AND COALESCE(encoder, '') = ? AND COALESCE(resolution, '') = ?`

		res, err := tx.ExecContext(ctx, update, st.Samples, st.AvgValue, formatTime(st.UpdatedAt),
			string(st.Kind), st.Encoder, st.Resolution)
		if err != nil {
			return fmt.Errorf("update throughput stat: %w", err)
		}

		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}

		if n > 0 {
			return nil
		}

		const insert = `
			INSERT INTO throughput_stats (kind, encoder, resolution, samples, avg_value, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`

		if _, err := tx.ExecContext(ctx, insert, string(st.Kind), nullString(st.Encoder),
			nullString(st.Resolution), st.Samples, st.AvgValue, formatTime(st.UpdatedAt)); err != nil {
			return fmt.Errorf("insert throughput stat: %w", err)
		}

		return nil
	})
}

func (s *store) GetThroughputStat(
	ctx context.Context, kind domain.Kind, encoder, resolution string,
) (domain.ThroughputStat, error) {
	const query = `
		SELECT ` + throughputColumns + ` FROM throughput_stats
		WHERE kind = ? AND COALESCE(encoder, '') = ? AND COALESCE(resolution, '') = ?`

	st, err := scanThroughput(s.db.read.QueryRowContext(ctx, query, string(kind), encoder, resolution))
	if notFound(err) {
		return domain.ThroughputStat{}, ErrNotFound
	}

	if err != nil {
		return domain.ThroughputStat{}, err
	}

	return st, nil
}

func (s *store) ListThroughputStats(ctx context.Context) ([]domain.ThroughputStat, error) {
	const query = `SELECT ` + throughputColumns + ` FROM throughput_stats ORDER BY kind, encoder, resolution`

	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select throughput stats: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.ThroughputStat{}

	for rows.Next() {
		st, err := scanThroughput(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, st)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func scanThroughput(row rowScanner) (domain.ThroughputStat, error) {
	var (
		st         domain.ThroughputStat
		kind       string
		encoder    sql.NullString
		resolution sql.NullString
		updatedAt  sql.NullString
	)

	err := row.Scan(&st.ID, &kind, &encoder, &resolution, &st.Samples, &st.AvgValue, &updatedAt)
	if err != nil {
		return domain.ThroughputStat{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	st.Kind = domain.Kind(kind)
	st.Encoder = encoder.String
	st.Resolution = resolution.String

	if st.UpdatedAt, err = scanTime(updatedAt); err != nil {
		return domain.ThroughputStat{}, err
	}

	return st, nil
}
