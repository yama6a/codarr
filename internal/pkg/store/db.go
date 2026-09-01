// Package store is Codarr's SQLite persistence layer. Every query lives behind
// the Store interface defined in store.go.
//
// # Time representation
//
// SQLite has no native time type. Every TIMESTAMP column in 001_schema.sql
// holds an RFC 3339 string in UTC, always nine fractional digits, produced by
// timeLayout below. Fixed width is the point: it makes the text sort
// identically to the instant, which ORDER BY queued_at in the queue claim
// depends on. The INTEGER columns named mtime are the exception and stay unix
// seconds, because that is what os.FileInfo hands us and what a later scan
// compares against.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	"github.com/yama6a/codarr/data"

	_ "modernc.org/sqlite" // pure-Go SQLite driver; the binary stays CGO-free
)

const (
	driverName      = "sqlite"
	migrateDialect  = "sqlite3"
	migrationsRoot  = "migrations"
	readPoolMaxOpen = 8
	pingTimeout     = 10 * time.Second
)

// DB is the pair of pools plan.md 17 calls for. WAL gives many readers and one
// writer, but database/sql will happily open several writing connections and
// collect SQLITE_BUSY, so writes are funnelled through a pool capped at one
// connection and reads get their own.
type DB struct {
	read  *sql.DB
	write *sql.DB
}

// Reader returns the read pool. Statements that write must not use it.
func (d *DB) Reader() *sql.DB { return d.read }

// Writer returns the single-connection write pool.
func (d *DB) Writer() *sql.DB { return d.write }

// Close closes both pools, reporting the first failure.
func (d *DB) Close() error {
	writeErr := d.write.Close()
	readErr := d.read.Close()

	return errors.Join(writeErr, readErr)
}

// Open opens path with the pragmas from plan.md 17 and returns both pools. It
// does not migrate; call Migrate.
func Open(ctx context.Context, path string) (*DB, error) {
	write, err := sql.Open(driverName, writeDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite write pool: %w", err)
	}

	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := write.PingContext(pingCtx); err != nil {
		_ = write.Close()

		return nil, fmt.Errorf("ping sqlite write pool: %w", err)
	}

	read, err := sql.Open(driverName, readDSN(path))
	if err != nil {
		_ = write.Close()

		return nil, fmt.Errorf("open sqlite read pool: %w", err)
	}

	read.SetMaxOpenConns(readPoolMaxOpen)
	read.SetMaxIdleConns(readPoolMaxOpen)
	read.SetConnMaxLifetime(0)

	if err := read.PingContext(pingCtx); err != nil {
		_ = read.Close()
		_ = write.Close()

		return nil, fmt.Errorf("ping sqlite read pool: %w", err)
	}

	return &DB{read: read, write: write}, nil
}

// Migrate applies the embedded migrations through the write pool.
func Migrate(db *DB, logger *slog.Logger) error {
	source := migrate.EmbedFileSystemMigrationSource{
		FileSystem: data.FS,
		Root:       migrationsRoot,
	}

	n, err := migrate.Exec(db.write, migrateDialect, source, migrate.Up)
	if err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("migrations complete", slog.Int("applied", n))

	return nil
}

// OpenAndMigrate is the wiring path in cmd/codarr: open, migrate, hand back.
func OpenAndMigrate(ctx context.Context, path string, logger *slog.Logger) (*DB, error) {
	db, err := Open(ctx, path)
	if err != nil {
		return nil, err
	}

	if err := Migrate(db, logger); err != nil {
		_ = db.Close()

		return nil, err
	}

	return db, nil
}

func writeDSN(path string) string {
	q := url.Values{}
	q.Set("_busy_timeout", "5000")
	q.Set("_journal_mode", "WAL")
	q.Set("_foreign_keys", "1")
	q.Set("_synchronous", "NORMAL")
	// Take the write lock at BEGIN rather than on first write. With one
	// connection there is nothing to upgrade against, but it keeps the
	// behaviour honest if the cap is ever raised.
	q.Set("_txlock", "immediate")

	return "file:" + url.PathEscape(path) + "?" + q.Encode()
}

func readDSN(path string) string {
	q := url.Values{}
	q.Set("_busy_timeout", "5000")
	q.Set("_foreign_keys", "1")
	// Makes "every write goes through the write pool" enforced by SQLite
	// rather than by convention.
	q.Set("_query_only", "1")

	return "file:" + url.PathEscape(path) + "?" + q.Encode()
}
