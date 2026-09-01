package fsx_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/fsx"
)

func TestCreate_RefusesToClobberAnExistingFile(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/.codarr-staging-1.mkv"

	f, err := fsx.OS().Create(path, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	_, err = fsx.OS().Create(path, 0o600)
	require.ErrorIs(t, err, os.ErrExist)
}

func TestCreate_FailsOnAnUnwritableDirectory(t *testing.T) {
	t.Parallel()

	_, err := fsx.OS().Create(t.TempDir()+"/missing/file", 0o600)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestCopy_WritesTheWholeFileAndOverwrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src, dst := dir+"/src.mkv", dir+"/dst.mkv"
	body := make([]byte, 3<<20)

	for i := range body {
		body[i] = byte(i)
	}

	require.NoError(t, os.WriteFile(src, body, 0o600))
	require.NoError(t, os.WriteFile(dst, []byte("a partial file from a crashed attempt"), 0o600))

	n, err := fsx.OS().Copy(t.Context(), src, dst)
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), n)

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestCopy_MissingSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := fsx.OS().Copy(t.Context(), dir+"/missing.mkv", dir+"/dst.mkv")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = os.Stat(dir + "/dst.mkv")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestCopy_UnwritableDestination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := dir + "/src.mkv"
	require.NoError(t, os.WriteFile(src, []byte("x"), 0o600))

	_, err := fsx.OS().Copy(t.Context(), src, dir+"/missing/dst.mkv")
	require.ErrorIs(t, err, os.ErrNotExist)
}

// A promotion copy can run for minutes; a shutdown must not have to wait it out.
func TestCopy_StopsOnCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src, dst := dir+"/src.mkv", dir+"/dst.mkv"
	require.NoError(t, os.WriteFile(src, make([]byte, 4<<20), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	n, err := fsx.OS().Copy(ctx, src, dst)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, n)
}
