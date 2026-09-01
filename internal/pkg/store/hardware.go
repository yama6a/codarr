package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const hwColumns = `id, backend, codec, profile, direction, works, error, ffmpeg_version, probed_at`

// ReplaceHWCapabilities swaps the whole probe result in one transaction. A
// probe is a complete picture of one ffmpeg build (plan.md 10.1), so merging
// old rows into new ones would keep stale entries alive across an upgrade.
func (s *store) ReplaceHWCapabilities(ctx context.Context, caps []domain.HWCapability) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM hw_capabilities`); err != nil {
			return fmt.Errorf("clear hw capabilities: %w", err)
		}

		const insert = `
			INSERT INTO hw_capabilities (backend, codec, profile, direction, works, error,
				ffmpeg_version, probed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

		for _, c := range caps {
			_, err := tx.ExecContext(ctx, insert, c.Backend, c.Codec, c.Profile, c.Direction,
				c.Works, nullString(c.Error), nullString(c.FfmpegVersion), formatTime(c.ProbedAt))
			if err != nil {
				return fmt.Errorf("insert hw capability: %w", err)
			}
		}

		return nil
	})
}

func (s *store) ListHWCapabilities(ctx context.Context) ([]domain.HWCapability, error) {
	const query = `SELECT ` + hwColumns + ` FROM hw_capabilities ORDER BY backend, direction, codec, profile`

	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select hw capabilities: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.HWCapability{}

	for rows.Next() {
		var (
			c        domain.HWCapability
			errText  sql.NullString
			version  sql.NullString
			probedAt sql.NullString
		)

		if err := rows.Scan(&c.ID, &c.Backend, &c.Codec, &c.Profile, &c.Direction, &c.Works,
			&errText, &version, &probedAt); err != nil {
			return nil, fmt.Errorf("scan hw capability: %w", err)
		}

		c.Error = errText.String
		c.FfmpegVersion = version.String

		if c.ProbedAt, err = scanTime(probedAt); err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}
