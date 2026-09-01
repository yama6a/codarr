// Package storetest hands store tests a migrated SQLite database of their own.
//
// SQLite is a file, so isolation is a fresh file per test rather than a shared
// server: t.TempDir gives each one its own, and t.Cleanup closes the pools.
package storetest

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// NewDB returns a Store over a fresh, migrated database.
//
//nolint:ireturn // the Store interface is what callers hold
func NewDB(t *testing.T) store.Store {
	t.Helper()

	return NewStore(t, NewRawDB(t))
}

// NewRawDB returns the pools themselves, for a test that needs to reach past
// the Store to set up a row or assert on one.
func NewRawDB(t *testing.T) *store.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "codarr.db")

	db, err := store.OpenAndMigrate(t.Context(), path, Logger())
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// NewStore wraps an existing DB, for a test that holds both.
//
//nolint:ireturn // the Store interface is what callers hold
func NewStore(t *testing.T, db *store.DB) store.Store {
	t.Helper()

	return store.New(db, Logger())
}

// Logger is a discarding slog.Logger, so store logging does not colour test output.
func Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
