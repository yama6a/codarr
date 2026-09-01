package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const settingsColumns = `temp_dir, qsv_device, scan_enabled, scan_cron, scan_rate_limit_fps,
	queue_paused, prioritise_quick_jobs, full_hash_enabled, updated_at`

func (s *store) EnsureSettings(ctx context.Context, defaults domain.Settings) error {
	const query = `
		INSERT INTO settings (id, ` + settingsColumns + `)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`

	_, err := s.exec(ctx, query,
		defaults.TempDir, defaults.QSVDevice, defaults.ScanEnabled, defaults.ScanCron,
		defaults.ScanRateLimitFPS, defaults.QueuePaused, defaults.PrioritiseQuickJobs,
		defaults.FullHashEnabled, formatTime(defaults.UpdatedAt))

	return err
}

func (s *store) GetSettings(ctx context.Context) (domain.Settings, error) {
	const query = `SELECT ` + settingsColumns + ` FROM settings WHERE id = 1`

	var (
		out       domain.Settings
		updatedAt sql.NullString
	)

	err := s.db.read.QueryRowContext(ctx, query).Scan(
		&out.TempDir, &out.QSVDevice, &out.ScanEnabled, &out.ScanCron, &out.ScanRateLimitFPS,
		&out.QueuePaused, &out.PrioritiseQuickJobs, &out.FullHashEnabled, &updatedAt)
	if notFound(err) {
		return domain.Settings{}, ErrNotFound
	}

	if err != nil {
		return domain.Settings{}, fmt.Errorf("select settings: %w", err)
	}

	out.UpdatedAt, err = scanTime(updatedAt)
	if err != nil {
		return domain.Settings{}, err
	}

	return out, nil
}

func (s *store) UpdateSettings(ctx context.Context, in domain.Settings) error {
	const query = `
		INSERT INTO settings (id, ` + settingsColumns + `)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			temp_dir              = excluded.temp_dir,
			qsv_device            = excluded.qsv_device,
			scan_enabled          = excluded.scan_enabled,
			scan_cron             = excluded.scan_cron,
			scan_rate_limit_fps   = excluded.scan_rate_limit_fps,
			queue_paused          = excluded.queue_paused,
			prioritise_quick_jobs = excluded.prioritise_quick_jobs,
			full_hash_enabled     = excluded.full_hash_enabled,
			updated_at            = excluded.updated_at`

	_, err := s.exec(ctx, query,
		in.TempDir, in.QSVDevice, in.ScanEnabled, in.ScanCron, in.ScanRateLimitFPS,
		in.QueuePaused, in.PrioritiseQuickJobs, in.FullHashEnabled, formatTime(in.UpdatedAt))

	return err
}
