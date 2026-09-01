package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const plexColumns = `base_url, token, client_identifier, refresh_after, analyze_after,
	guard_active_streams, last_tested_at, last_test_result, updated_at`

// GetPlexConfig returns ErrNotFound while Plex is unconfigured, which is the
// state a fresh install starts in (plan.md 21).
func (s *store) GetPlexConfig(ctx context.Context) (domain.PlexConfig, error) {
	const query = `SELECT ` + plexColumns + ` FROM plex WHERE id = 1`

	var (
		out            domain.PlexConfig
		token          sql.NullString
		lastTestedAt   sql.NullString
		lastTestResult sql.NullString
		updatedAt      sql.NullString
	)

	err := s.db.read.QueryRowContext(ctx, query).Scan(
		&out.BaseURL, &token, &out.ClientIdentifier, &out.RefreshAfter, &out.AnalyzeAfter,
		&out.GuardActiveStreams, &lastTestedAt, &lastTestResult, &updatedAt)
	if notFound(err) {
		return domain.PlexConfig{}, ErrNotFound
	}

	if err != nil {
		return domain.PlexConfig{}, fmt.Errorf("select plex: %w", err)
	}

	out.Token = token.String
	out.LastTestResult = lastTestResult.String

	if out.LastTestedAt, err = scanTimePtr(lastTestedAt); err != nil {
		return domain.PlexConfig{}, err
	}

	if out.UpdatedAt, err = scanTime(updatedAt); err != nil {
		return domain.PlexConfig{}, err
	}

	return out, nil
}

// UpdatePlexConfig writes the row verbatim. Resolving domain.MaskedSecret back
// to the stored token is the API layer's job (plan.md 18.4).
func (s *store) UpdatePlexConfig(ctx context.Context, c domain.PlexConfig) error {
	const query = `
		INSERT INTO plex (id, ` + plexColumns + `)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			base_url             = excluded.base_url,
			token                = excluded.token,
			client_identifier    = excluded.client_identifier,
			refresh_after        = excluded.refresh_after,
			analyze_after        = excluded.analyze_after,
			guard_active_streams = excluded.guard_active_streams,
			last_tested_at       = excluded.last_tested_at,
			last_test_result     = excluded.last_test_result,
			updated_at           = excluded.updated_at`

	_, err := s.exec(ctx, query,
		c.BaseURL, nullString(c.Token), c.ClientIdentifier, c.RefreshAfter, c.AnalyzeAfter,
		c.GuardActiveStreams, formatTimePtr(c.LastTestedAt), nullString(c.LastTestResult),
		formatTime(c.UpdatedAt))

	return err
}

func (s *store) SetPlexTestResult(ctx context.Context, at time.Time, result string) error {
	const query = `UPDATE plex SET last_tested_at = ?, last_test_result = ? WHERE id = 1`

	return s.execOne(ctx, query, formatTime(at), result)
}

func (s *store) ListPlexPathMappings(ctx context.Context) ([]domain.PathMapping, error) {
	const query = `SELECT id, local, remote, sort FROM plex_path_mappings ORDER BY sort, id`

	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select plex path mappings: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.PathMapping{}

	for rows.Next() {
		var m domain.PathMapping
		if err := rows.Scan(&m.ID, &m.Local, &m.Remote, &m.Sort); err != nil {
			return nil, fmt.Errorf("scan plex path mapping: %w", err)
		}

		out = append(out, m)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *store) ReplacePlexPathMappings(ctx context.Context, m []domain.PathMapping) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM plex_path_mappings`); err != nil {
			return fmt.Errorf("clear plex path mappings: %w", err)
		}

		const insert = `INSERT INTO plex_path_mappings (local, remote, sort) VALUES (?, ?, ?)`

		for i, mapping := range m {
			if _, err := tx.ExecContext(ctx, insert, mapping.Local, mapping.Remote, i); err != nil {
				return fmt.Errorf("insert plex path mapping: %w", err)
			}
		}

		return nil
	})
}
