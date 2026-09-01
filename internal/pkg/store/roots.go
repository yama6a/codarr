package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const rootColumns = `id, path, arr_instance_id, imported, enabled, created_at`

func (s *store) CreateRoot(ctx context.Context, r domain.Root) (domain.Root, error) {
	const query = `
		INSERT INTO roots (path, arr_instance_id, imported, enabled, created_at)
		VALUES (?, ?, ?, ?, ?)
		RETURNING id`

	err := s.db.write.QueryRowContext(ctx, query,
		r.Path, ptrValue(r.ArrInstanceID), r.Imported, r.Enabled, formatTime(r.CreatedAt),
	).Scan(&r.ID)
	if err != nil {
		return domain.Root{}, fmt.Errorf("insert root: %w", err)
	}

	return r, nil
}

func (s *store) DeleteRoot(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM roots WHERE id = ?`, id)
}

func (s *store) GetRoot(ctx context.Context, id int64) (domain.Root, error) {
	const query = `SELECT ` + rootColumns + ` FROM roots WHERE id = ?`

	r, err := scanRoot(s.db.read.QueryRowContext(ctx, query, id))
	if notFound(err) {
		return domain.Root{}, ErrNotFound
	}

	if err != nil {
		return domain.Root{}, err
	}

	return r, nil
}

func (s *store) ListRoots(ctx context.Context) ([]domain.Root, error) {
	const query = `SELECT ` + rootColumns + ` FROM roots ORDER BY path`

	rows, err := s.db.read.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select roots: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.Root{}

	for rows.Next() {
		r, err := scanRoot(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return out, nil
}

func (s *store) SetRootEnabled(ctx context.Context, id int64, enabled bool) error {
	return s.execOne(ctx, `UPDATE roots SET enabled = ? WHERE id = ?`, enabled, id)
}

func scanRoot(row rowScanner) (domain.Root, error) {
	var (
		out       domain.Root
		instance  sql.NullInt64
		createdAt sql.NullString
	)

	if err := row.Scan(&out.ID, &out.Path, &instance, &out.Imported, &out.Enabled, &createdAt); err != nil {
		return domain.Root{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	out.ArrInstanceID = int64Ptr(instance)

	var err error
	if out.CreatedAt, err = scanTime(createdAt); err != nil {
		return domain.Root{}, err
	}

	return out, nil
}
