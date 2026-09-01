package fingerprint_test

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/fingerprint"
	"github.com/yama6a/codarr/internal/pkg/fsx"
)

const mib = 1 << 20

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

type memFS struct {
	files   map[string][]byte
	openErr error
}

var _ fsx.FS = (*memFS)(nil)

func (m *memFS) Open(path string) (io.ReadSeekCloser, error) {
	if m.openErr != nil {
		return nil, m.openErr
	}

	b, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}

	return nopCloser{bytes.NewReader(b)}, nil
}

func (*memFS) Stat(string) (fsx.FileInfo, error)                             { panic("unused") }
func (*memFS) Statfs(string) (fsx.SpaceInfo, error)                          { panic("unused") }
func (*memFS) Remove(string) error                                           { panic("unused") }
func (*memFS) Rename(string, string) error                                   { panic("unused") }
func (*memFS) SyncFile(string) error                                         { panic("unused") }
func (*memFS) SyncDir(string) error                                          { panic("unused") }
func (*memFS) Chmod(string, os.FileMode) error                               { panic("unused") }
func (*memFS) Chtimes(string, time.Time, time.Time) error                    { panic("unused") }
func (*memFS) Chown(string, int, int) error                                  { panic("unused") }
func (*memFS) WalkDir(string, func(string, fsx.FileInfo, error) error) error { panic("unused") }
func (*memFS) MkdirAll(string, os.FileMode) error                            { panic("unused") }
func (*memFS) Glob(string) ([]string, error)                                 { panic("unused") }

func body(t *testing.T, seed int64, n int) []byte {
	t.Helper()

	b := make([]byte, n)
	_, err := rand.New(rand.NewSource(seed)).Read(b) //nolint:gosec // deterministic test data, not a secret
	require.NoError(t, err)

	return b
}

func sparse(t *testing.T, b []byte) string {
	t.Helper()

	fp, err := fingerprint.New(&memFS{files: map[string][]byte{"/f": b}}).Sparse("/f")
	require.NoError(t, err)

	return fp
}

func TestSparse_IsDeterministicAndPrefixed(t *testing.T) {
	t.Parallel()

	b := body(t, 1, 5*mib)

	first := sparse(t, b)
	second := sparse(t, append([]byte(nil), b...))

	require.Equal(t, first, second)
	require.Equal(t, "xxh3-128", fingerprint.AlgoOf(first))
	require.Len(t, first, len("xxh3-128:")+32)
}

func TestSparse_ChangedHeadChangesFingerprint(t *testing.T) {
	t.Parallel()

	b := body(t, 2, 5*mib)
	modified := append([]byte(nil), b...)
	modified[0] ^= 0xff

	require.NotEqual(t, sparse(t, b), sparse(t, modified))
}

func TestSparse_ChangedTailChangesFingerprint(t *testing.T) {
	t.Parallel()

	b := body(t, 3, 5*mib)
	modified := append([]byte(nil), b...)
	modified[len(modified)-1] ^= 0xff

	require.NotEqual(t, sparse(t, b), sparse(t, modified))
}

// plan.md 12.1 names the interior gap explicitly: a same-length edit between the
// head and the tail is not caught, and ZFS scrubbing covers accidental corruption.
func TestSparse_InteriorEditIsNotCaught(t *testing.T) {
	t.Parallel()

	b := body(t, 4, 5*mib)
	modified := append([]byte(nil), b...)
	modified[2*mib] ^= 0xff

	require.Equal(t, sparse(t, b), sparse(t, modified))
}

func TestSparse_ChangedSizeChangesFingerprint(t *testing.T) {
	t.Parallel()

	b := body(t, 5, 3*mib)

	require.NotEqual(t, sparse(t, b), sparse(t, b[:len(b)-1]))
}

// A file below 2 MiB has an overlapping head and tail, and below 1 MiB they are
// the same bytes. Both must still produce a stable, size-sensitive value.
func TestSparse_SmallFilesWithOverlappingHeadAndTail(t *testing.T) {
	t.Parallel()

	seen := map[string]int{}

	for _, size := range []int{1, 512, mib - 1, mib, mib + 1, 2*mib - 1, 2 * mib} {
		b := body(t, 6, size)

		fp := sparse(t, b)
		require.Equal(t, fp, sparse(t, b), "size %d is not deterministic", size)

		seen[fp] = size
	}

	require.Len(t, seen, 7)
}

func TestSparse_EmptyFile(t *testing.T) {
	t.Parallel()

	empty := sparse(t, []byte{})

	require.Equal(t, empty, sparse(t, nil))
	require.NotEqual(t, empty, sparse(t, []byte{0}))
	require.Equal(t, "xxh3-128", fingerprint.AlgoOf(empty))
}

// The head-and-tail regions are identical here, so only the size suffix
// distinguishes them. Without it these two collide.
func TestSparse_SizeSuffixSeparatesSameHeadAndTail(t *testing.T) {
	t.Parallel()

	head := body(t, 7, mib)
	tail := body(t, 8, mib)

	short := append(append([]byte(nil), head...), tail...)
	long := append(append(append([]byte(nil), head...), make([]byte, mib)...), tail...)

	require.NotEqual(t, sparse(t, short), sparse(t, long))
}

func TestSparse_PropagatesOpenError(t *testing.T) {
	t.Parallel()

	_, err := fingerprint.New(&memFS{openErr: os.ErrPermission}).Sparse("/f")
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestSparse_RejectsEmptyPath(t *testing.T) {
	t.Parallel()

	_, err := fingerprint.New(&memFS{}).Sparse("")
	require.ErrorIs(t, err, fingerprint.ErrEmptyPath)
}

func TestFull_HashesEveryByte(t *testing.T) {
	t.Parallel()

	b := body(t, 9, 5*mib)
	modified := append([]byte(nil), b...)
	modified[2*mib] ^= 0xff

	fs := &memFS{files: map[string][]byte{"/a": b, "/b": modified, "/c": append([]byte(nil), b...)}}
	f := fingerprint.New(fs)

	a, err := f.Full("/a")
	require.NoError(t, err)

	interior, err := f.Full("/b")
	require.NoError(t, err)

	same, err := f.Full("/c")
	require.NoError(t, err)

	require.Equal(t, a, same)
	require.NotEqual(t, a, interior)
	require.Equal(t, "xxh3-128", fingerprint.AlgoOf(a))
}

func TestFull_DiffersFromSparse(t *testing.T) {
	t.Parallel()

	b := body(t, 10, 512)
	fs := &memFS{files: map[string][]byte{"/f": b}}

	full, err := fingerprint.New(fs).Full("/f")
	require.NoError(t, err)

	require.NotEqual(t, sparse(t, b), full)
}

func TestFull_EmptyFile(t *testing.T) {
	t.Parallel()

	fs := &memFS{files: map[string][]byte{"/f": nil}}

	got, err := fingerprint.New(fs).Full("/f")
	require.NoError(t, err)
	require.Equal(t, "xxh3-128", fingerprint.AlgoOf(got))
}

func TestFull_PropagatesOpenError(t *testing.T) {
	t.Parallel()

	_, err := fingerprint.New(&memFS{openErr: os.ErrPermission}).Full("/f")
	require.ErrorIs(t, err, os.ErrPermission)

	_, err = fingerprint.New(&memFS{}).Full("")
	require.ErrorIs(t, err, fingerprint.ErrEmptyPath)
}

func TestAlgoOf_ReturnsEmptyWithoutPrefix(t *testing.T) {
	t.Parallel()

	require.Empty(t, fingerprint.AlgoOf("deadbeef"))
	require.Empty(t, fingerprint.AlgoOf(""))
}

func TestSparse_MatchesRealFilesystem(t *testing.T) {
	t.Parallel()

	b := body(t, 11, 3*mib)

	dir := t.TempDir()
	path := dir + "/sample.mkv"
	require.NoError(t, os.WriteFile(path, b, 0o600))

	got, err := fingerprint.New(fsx.OS()).Sparse(path)
	require.NoError(t, err)
	require.Equal(t, sparse(t, b), got)

	_, err = fingerprint.New(fsx.OS()).Sparse(dir + "/missing.mkv")
	require.True(t, errors.Is(err, os.ErrNotExist))
}
