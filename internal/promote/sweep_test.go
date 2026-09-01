package promote_test

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSweep_RemovesUnclaimedDebris(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	claimed := destDir + "/.codarr-staging-7.mkv"
	h.fs.addFile(claimed, 1)
	h.fs.addFile(destDir+"/.codarr-staging-99.mkv", 1)
	h.fs.addDir(destDir + "/.codarr-writetest-13")
	h.fs.addFile(tempDir+"/.codarr-staging-5.mkv", 1)

	removed, err := h.promoter.Sweep(t.Context(), []string{"/media"}, []string{claimed, stagingPath})
	require.NoError(t, err)
	require.Equal(t, []string{
		destDir + "/.codarr-staging-99.mkv",
		destDir + "/.codarr-writetest-13",
		tempDir + "/.codarr-staging-5.mkv",
	}, removed)

	_, ok := h.fs.get(claimed)
	require.True(t, ok, "a claimed staging file is left alone")

	_, ok = h.fs.get(stagingPath)
	require.True(t, ok)

	_, ok = h.fs.get(sourcePath)
	require.True(t, ok, "real media is never touched")
}

func TestSweep_LeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.addFile(destDir+"/Dune.en.srt", 1)
	h.fs.addFile(destDir+"/.nfo", 1)

	removed, err := h.promoter.Sweep(t.Context(), []string{"/media"}, []string{stagingPath})
	require.NoError(t, err)
	require.Empty(t, removed)
}

func TestSweep_ContinuesPastAFailedRemoval(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	stuck := destDir + "/.codarr-staging-1.mkv"
	h.fs.addFile(stuck, 1)
	h.fs.addFile(destDir+"/.codarr-staging-2.mkv", 1)
	h.fs.failOn("fs.Remove", stuck, os.ErrPermission)

	removed, err := h.promoter.Sweep(t.Context(), []string{"/media"}, nil)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Equal(t, []string{destDir + "/.codarr-staging-2.mkv", stagingPath}, removed)
}

func TestSweep_ReportsWalkErrors(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	boom := errors.New("stale file handle")
	h.fs.walkErr[destDir] = boom

	_, err := h.promoter.Sweep(t.Context(), []string{"/media"}, []string{stagingPath})
	require.ErrorIs(t, err, boom)
}

func TestSweep_WithoutATempDir(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withoutTempDir()
	h.fs.addFile(tempDir+"/.codarr-staging-5.mkv", 1)

	removed, err := h.promoter.Sweep(t.Context(), []string{"/media"}, []string{stagingPath})
	require.NoError(t, err)
	require.Empty(t, removed)
	require.NotContains(t, h.rec.list(), "fs.Glob "+tempDir+"/.codarr-*")
}
