package fsx

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type osFS struct{}

var _ FS = osFS{}

// OS returns the real filesystem.
func OS() FS { return osFS{} }

func (osFS) Stat(path string) (FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("stat %s: %w", path, err)
	}

	return fromFileInfo(fi), nil
}

func fromFileInfo(fi os.FileInfo) FileInfo {
	out := FileInfo{
		Size:  fi.Size(),
		Mode:  fi.Mode(),
		MTime: fi.ModTime(),
		IsDir: fi.IsDir(),
		NLink: 1,
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		out.UID = int(st.Uid)
		out.GID = int(st.Gid)
		out.NLink = int(st.Nlink)
		out.Device = st.Dev
	}

	return out
}

func (osFS) Statfs(path string) (SpaceInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return SpaceInfo{}, fmt.Errorf("statfs %s: %w", path, err)
	}

	bsize := uint64(st.Bsize)

	return SpaceInfo{
		TotalBytes: st.Blocks * bsize,
		FreeBytes:  st.Bavail * bsize,
	}, nil
}

func (osFS) Open(path string) (io.ReadSeekCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return f, nil
}

func (osFS) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return nil
}

func (osFS) Rename(oldpath, newpath string) error {
	if err := os.Rename(oldpath, newpath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", oldpath, newpath, err)
	}

	return nil
}

func (osFS) SyncFile(path string) error { return syncPath(path, os.O_RDWR) }

func (osFS) SyncDir(path string) error { return syncPath(path, os.O_RDONLY) }

func syncPath(path string, flag int) error {
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return fmt.Errorf("open %s for sync: %w", path, err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync %s: %w", path, err)
	}

	return nil
}

func (osFS) Chmod(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	return nil
}

func (osFS) Chtimes(path string, atime, mtime time.Time) error {
	if err := os.Chtimes(path, atime, mtime); err != nil {
		return fmt.Errorf("chtimes %s: %w", path, err)
	}

	return nil
}

func (osFS) Chown(path string, uid, gid int) error {
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}

	return nil
}

func (osFS) WalkDir(root string, fn func(string, FileInfo, error) error) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fn(path, FileInfo{}, err)
		}

		fi, statErr := d.Info()
		if statErr != nil {
			return fn(path, FileInfo{}, statErr)
		}

		return fn(path, fromFileInfo(fi), nil)
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}

	return nil
}

func (osFS) MkdirAll(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("mkdirall %s: %w", path, err)
	}

	return nil
}

func (osFS) Glob(pattern string) ([]string, error) {
	m, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}

	return m, nil
}
