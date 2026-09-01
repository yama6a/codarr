// Package fingerprint answers "is this still the same file?" cheaply enough to run on
// every scan: two 1 MiB reads regardless of file size, over NFS (plan.md 12.1).
package fingerprint

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/zeebo/xxh3"
)

// Algo is the value written to media_files.fingerprint_algo. The column exists
// so this can change later, so every value carries it as a prefix.
const Algo = "xxh3-128"

// ChunkBytes is how much of the head and of the tail is hashed.
const ChunkBytes int64 = 1 << 20

// ErrEmptyPath is returned rather than letting an empty path reach the filesystem.
var ErrEmptyPath = errors.New("fingerprint: empty path")

// Fingerprinter computes file identities through an fsx.FS.
type Fingerprinter struct {
	fs fsx.FS
}

// New returns a Fingerprinter reading through fs.
func New(fs fsx.FS) *Fingerprinter { return &Fingerprinter{fs: fs} }

// Sparse is the scan-time fingerprint of plan.md 12.1: xxh3-128 over the first
// 1 MiB, the last 1 MiB and the exact byte size as an eight-byte suffix.
func (f *Fingerprinter) Sparse(path string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}

	r, err := f.fs.Open(path)
	if err != nil {
		return "", fmt.Errorf("fingerprint %s: %w", path, err)
	}
	defer func() { _ = r.Close() }()

	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return "", fmt.Errorf("fingerprint %s: size: %w", path, err)
	}

	h := xxh3.New()

	// Head and tail overlap below 2 MiB and are the same bytes below 1 MiB. That
	// region is then hashed twice, which is deterministic and therefore fine.
	if err := copyRange(h, r, 0, min(size, ChunkBytes)); err != nil {
		return "", fmt.Errorf("fingerprint %s: head: %w", path, err)
	}

	if err := copyRange(h, r, max(0, size-ChunkBytes), min(size, ChunkBytes)); err != nil {
		return "", fmt.Errorf("fingerprint %s: tail: %w", path, err)
	}

	var suffix [8]byte

	binary.BigEndian.PutUint64(suffix[:], uint64(size))

	if _, err := h.Write(suffix[:]); err != nil {
		return "", fmt.Errorf("fingerprint %s: size suffix: %w", path, err)
	}

	return format(h), nil
}

// Full is the on-demand whole-file hash of plan.md 12.2, run at promotion and by the
// integrity endpoint but never during a scan.
func (f *Fingerprinter) Full(path string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}

	r, err := f.fs.Open(path)
	if err != nil {
		return "", fmt.Errorf("full hash %s: %w", path, err)
	}
	defer func() { _ = r.Close() }()

	h := xxh3.New()

	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("full hash %s: %w", path, err)
	}

	return format(h), nil
}

// AlgoOf returns the algorithm prefix of a stored fingerprint, or "" if it
// carries none.
func AlgoOf(fingerprint string) string {
	algo, _, ok := strings.Cut(fingerprint, ":")
	if !ok {
		return ""
	}

	return algo
}

func copyRange(h io.Writer, r io.ReadSeeker, offset, n int64) error {
	if n == 0 {
		return nil
	}

	if _, err := r.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek to %d: %w", offset, err)
	}

	if _, err := io.CopyN(h, r, n); err != nil {
		return fmt.Errorf("read %d bytes at %d: %w", n, offset, err)
	}

	return nil
}

func format(h *xxh3.Hasher) string {
	sum := h.Sum128().Bytes()

	return Algo + ":" + hex.EncodeToString(sum[:])
}
