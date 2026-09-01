// Package fsx is the filesystem boundary: promotion is irreversible and rests on stat,
// free space, rename and fsync behaving exactly as expected.
package fsx

import (
	"context"
	"io"
	"os"
	"time"
)

//go:generate go run -mod=mod github.com/matryer/moq -out mock/fs_mock.go -pkg mock . FS

// FileInfo is the subset of stat Codarr acts on. NLink > 1 fails preflight, because
// renaming over a seeding copy that shares an inode would damage it.
type FileInfo struct {
	Size   int64
	Mode   os.FileMode
	MTime  time.Time
	UID    int
	GID    int
	NLink  int
	Device uint64
	IsDir  bool
}

// SpaceInfo is what statfs reports for a filesystem.
type SpaceInfo struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// WriteSyncCloser is a file open for writing; Sync runs before Close on anything to be
// renamed into place, since data and directory entry are separately durable (plan.md 15.2).
type WriteSyncCloser interface {
	io.WriteCloser
	Sync() error
}

type FS interface {
	Stat(path string) (FileInfo, error)
	Statfs(path string) (SpaceInfo, error)
	Open(path string) (io.ReadSeekCloser, error)
	Remove(path string) error

	// Create fails when the path already exists, which is what makes a staging
	// name a claim rather than a race.
	Create(path string, mode os.FileMode) (WriteSyncCloser, error)

	// Copy is the EXDEV path of plan.md 15.1, for a staging file that landed on
	// another filesystem. It overwrites, so a crashed attempt does not block the retry.
	Copy(ctx context.Context, src, dst string) (int64, error)

	// Rename replaces newpath atomically. On NFSv4 this is a single server-side
	// RENAME, which is why staging is always a sibling of the target.
	Rename(oldpath, newpath string) error

	// SyncFile and SyncDir must both run before the rename: the data and the
	// directory entry are separately durable.
	SyncFile(path string) error
	SyncDir(path string) error

	Chmod(path string, mode os.FileMode) error
	Chtimes(path string, atime, mtime time.Time) error

	// Chown is best effort. A root_squash export denies it, and that is not a
	// job failure.
	Chown(path string, uid, gid int) error

	WalkDir(root string, fn func(path string, info FileInfo, err error) error) error
	MkdirAll(path string, mode os.FileMode) error
	Glob(pattern string) ([]string, error)
}
