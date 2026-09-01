package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const arrColumns = `id, name, flavour, base_url, api_key, webhook_id, rescan_after,
	unmonitor_after, enabled, last_tested_at, last_test_result, created_at, updated_at`

func (s *store) CreateArrInstance(ctx context.Context, a domain.ArrInstance) (domain.ArrInstance, error) {
	const query = `
		INSERT INTO arr_instances (name, flavour, base_url, api_key, webhook_id, rescan_after,
			unmonitor_after, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id`

	err := s.db.write.QueryRowContext(ctx, query,
		a.Name, string(a.Flavour), a.BaseURL, a.APIKey, a.WebhookID, a.RescanAfter,
		a.UnmonitorAfter, a.Enabled, formatTime(a.CreatedAt), formatTime(a.UpdatedAt),
	).Scan(&a.ID)
	if err != nil {
		return domain.ArrInstance{}, fmt.Errorf("insert arr instance: %w", err)
	}

	return a, nil
}

// UpdateArrInstance writes the row verbatim; resolving domain.MaskedSecret back
// to the stored API key is the API layer's job (plan.md 18.4).
func (s *store) UpdateArrInstance(ctx context.Context, a domain.ArrInstance) error {
	const query = `
		UPDATE arr_instances SET
			name = ?, flavour = ?, base_url = ?, api_key = ?, webhook_id = ?,
			rescan_after = ?, unmonitor_after = ?, enabled = ?, updated_at = ?
		WHERE id = ?`

	return s.execOne(ctx, query,
		a.Name, string(a.Flavour), a.BaseURL, a.APIKey, a.WebhookID,
		a.RescanAfter, a.UnmonitorAfter, a.Enabled, formatTime(a.UpdatedAt), a.ID)
}

func (s *store) DeleteArrInstance(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM arr_instances WHERE id = ?`, id)
}

func (s *store) GetArrInstance(ctx context.Context, id int64) (domain.ArrInstance, error) {
	const query = `SELECT ` + arrColumns + ` FROM arr_instances WHERE id = ?`

	return s.arrInstanceRow(s.db.read.QueryRowContext(ctx, query, id))
}

func (s *store) GetArrInstanceByWebhookID(ctx context.Context, webhookID string) (domain.ArrInstance, error) {
	const query = `SELECT ` + arrColumns + ` FROM arr_instances WHERE webhook_id = ?`

	return s.arrInstanceRow(s.db.read.QueryRowContext(ctx, query, webhookID))
}

func (s *store) ListArrInstances(ctx context.Context) ([]domain.ArrInstance, error) {
	const query = `SELECT ` + arrColumns + ` FROM arr_instances ORDER BY name`

	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select arr instances: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.ArrInstance{}

	for rows.Next() {
		a, err := scanArrInstance(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *store) SetArrTestResult(ctx context.Context, id int64, at time.Time, result string) error {
	const query = `UPDATE arr_instances SET last_tested_at = ?, last_test_result = ? WHERE id = ?`

	return s.execOne(ctx, query, formatTime(at), result, id)
}

func (s *store) ListArrPathMappings(ctx context.Context, arrInstanceID int64) ([]domain.PathMapping, error) {
	const query = `
		SELECT id, local, remote, sort FROM arr_path_mappings
		WHERE arr_instance_id = ? ORDER BY sort, id`

	rows, err := s.db.read.QueryContext(ctx, query, arrInstanceID)
	if err != nil {
		return nil, fmt.Errorf("select arr path mappings: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.PathMapping{}

	for rows.Next() {
		var m domain.PathMapping
		if err := rows.Scan(&m.ID, &m.Local, &m.Remote, &m.Sort); err != nil {
			return nil, fmt.Errorf("scan arr path mapping: %w", err)
		}

		out = append(out, m)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *store) ReplaceArrPathMappings(ctx context.Context, arrInstanceID int64, m []domain.PathMapping) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		const removeAll = `DELETE FROM arr_path_mappings WHERE arr_instance_id = ?`
		if _, err := tx.ExecContext(ctx, removeAll, arrInstanceID); err != nil {
			return fmt.Errorf("clear arr path mappings: %w", err)
		}

		const insert = `
			INSERT INTO arr_path_mappings (arr_instance_id, local, remote, sort)
			VALUES (?, ?, ?, ?)`

		for i, mapping := range m {
			if _, err := tx.ExecContext(ctx, insert, arrInstanceID, mapping.Local, mapping.Remote, i); err != nil {
				return fmt.Errorf("insert arr path mapping: %w", err)
			}
		}

		return nil
	})
}

func (s *store) arrInstanceRow(row *sql.Row) (domain.ArrInstance, error) {
	a, err := scanArrInstance(row)
	if notFound(err) {
		return domain.ArrInstance{}, ErrNotFound
	}

	if err != nil {
		return domain.ArrInstance{}, err
	}

	return a, nil
}

func scanArrInstance(row rowScanner) (domain.ArrInstance, error) {
	var (
		out            domain.ArrInstance
		flavour        string
		lastTestedAt   sql.NullString
		lastTestResult sql.NullString
		createdAt      sql.NullString
		updatedAt      sql.NullString
	)

	err := row.Scan(&out.ID, &out.Name, &flavour, &out.BaseURL, &out.APIKey, &out.WebhookID,
		&out.RescanAfter, &out.UnmonitorAfter, &out.Enabled, &lastTestedAt, &lastTestResult,
		&createdAt, &updatedAt)
	if err != nil {
		return domain.ArrInstance{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	out.Flavour = domain.Flavour(flavour)
	out.LastTestResult = lastTestResult.String

	if out.LastTestedAt, err = scanTimePtr(lastTestedAt); err != nil {
		return domain.ArrInstance{}, err
	}

	if out.CreatedAt, err = scanTime(createdAt); err != nil {
		return domain.ArrInstance{}, err
	}

	if out.UpdatedAt, err = scanTime(updatedAt); err != nil {
		return domain.ArrInstance{}, err
	}

	return out, nil
}
