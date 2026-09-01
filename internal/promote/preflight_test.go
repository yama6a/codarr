package promote_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/promote"
)

func preflightRequest() promote.PreflightRequest {
	return promote.PreflightRequest{
		JobID:      jobID,
		SourcePath: sourcePath,
		Source:     sourceState(),
		OutputExt:  ".mkv",
	}
}

func TestPreflight_StagesBesideTheDestination(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got, err := h.promoter.Preflight(preflightRequest())
	require.NoError(t, err)
	require.Equal(t, promote.Staging{Path: stagingPath, FinalPath: stagingPath}, got)

	// The writability probe leaves nothing behind.
	_, ok := h.fs.get(destDir + "/.codarr-writetest-42")
	require.False(t, ok)
}

func TestPreflight_AcceptsAnExtensionWithoutADot(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := preflightRequest()
	req.OutputExt = "mp4"

	got, err := h.promoter.Preflight(req)
	require.NoError(t, err)
	require.Equal(t, destDir+"/.codarr-staging-42.mp4", got.Path)
}

func TestPreflight_RejectsARelativeSourcePath(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := preflightRequest()
	req.SourcePath = "Dune.mkv"

	_, err := h.promoter.Preflight(req)
	requireFailure(t, err, domain.FailPreflight, "is not absolute")
}

func TestPreflight_MissingSource(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	require.NoError(t, h.fs.Remove(sourcePath))

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "no longer exists")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPreflight_SourceIsADirectory(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.addDir(sourcePath)

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "is a directory")
}

// plan.md 15.4: one stat call, and it prevents damaging a hardlinked seeding copy.
func TestPreflight_HardlinkedSourceFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	file, ok := h.fs.get(sourcePath)
	require.True(t, ok)

	file.nlink = 2

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "has 2 hard links", "would damage the other copies")
}

func TestPreflight_SourceChangedSinceAnalysis(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		mutate func(*fakeFile)
		want   string
	}{
		"size": {
			mutate: func(f *fakeFile) { f.size = sourceSize - 1 },
			want:   "it is now 8589934591 bytes, analysis recorded 8589934592",
		},
		"mtime": {
			mutate: func(f *fakeFile) { f.mtime = time.Unix(1700000123, 0).UTC() },
			want:   "modification time is now 1700000123, analysis recorded 1700000000",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			file, ok := h.fs.get(sourcePath)
			require.True(t, ok)

			tc.mutate(file)

			_, err := h.promoter.Preflight(preflightRequest())
			requireFailure(t, err, domain.FailPreflight, "changed since analysis", tc.want)
		})
	}
}

func TestPreflight_FingerprintChangedSinceAnalysis(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fp.SparseFunc = func(string) (string, error) {
		return "xxh3-128:" + strings.Repeat("c", 32), nil
	}

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "fingerprint is now xxh3-128:ccc")
}

func TestPreflight_FingerprintErrorFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fp.SparseFunc = func(string) (string, error) { return "", os.ErrPermission }

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "could not be fingerprinted")
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestPreflight_SkipsTheFingerprintWhenAnalysisRecordedNone(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := preflightRequest()
	req.Source.Fingerprint = ""

	_, err := h.promoter.Preflight(req)
	require.NoError(t, err)
	require.Empty(t, h.fp.SparseCalls())
}

func TestPreflight_UnwritableDestination(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.MkdirAll", destDir+"/.codarr-writetest-42", os.ErrPermission)

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "is not writable")
	require.ErrorIs(t, err, os.ErrPermission)
}

func TestPreflight_WritabilityProbeThatCannotBeRemoved(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Remove", destDir+"/.codarr-writetest-42", os.ErrPermission)

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "could not be removed")
}

// plan.md 15.4: at least 1.2x the source size.
func TestPreflight_FallsBackToTempWhenTheDestinationIsTight(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize)}

	got, err := h.promoter.Preflight(preflightRequest())
	require.NoError(t, err)
	require.Equal(t, promote.Staging{
		Path:        tempDir + "/.codarr-staging-42.mkv",
		FinalPath:   stagingPath,
		UsedTempDir: true,
	}, got)
}

func TestPreflight_JustEnoughSpaceStaysOnTheDestination(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	size := sourceSize
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(float64(size) * promote.SpaceFactor)}

	got, err := h.promoter.Preflight(preflightRequest())
	require.NoError(t, err)
	require.False(t, got.UsedTempDir)
}

func TestPreflight_NoSpaceAnywhere(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: 1}
	h.fs.space[tempDir] = fsx.SpaceInfo{FreeBytes: 2}

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "neither the destination", "10307921510 bytes needed")
}

func TestPreflight_NoTempDirConfigured(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.withoutTempDir()
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: 1}

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "no temp directory is configured")
}

func TestPreflight_UnreadableFreeSpace(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Statfs", destDir, os.ErrPermission)

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "free space on")
}

// plan.md 15.6: NFSv4 hides a dataset split behind identical client-side paths,
// so the device numbers are compared rather than trusted.
func TestPreflight_DeviceMismatchOnTheDestinationFails(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.nextDevice[destDir] = []uint64{7}

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "different device numbers", "would not be atomic")
}

// The temp fallback is expected to land on another filesystem. That is not a
// failure, it is the reason plan.md 15.1 copies before renaming.
func TestPreflight_TempFallbackOnAnotherDeviceIsRecorded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize)}
	h.fs.addDir(tempDir).device = 99

	got, err := h.promoter.Preflight(preflightRequest())
	require.NoError(t, err)
	require.Equal(t, promote.Staging{
		Path:        tempDir + "/.codarr-staging-42.mkv",
		FinalPath:   stagingPath,
		UsedTempDir: true,
		CrossDevice: true,
	}, got)
}

// A temp directory that turns out to be the same filesystem needs no copy.
func TestPreflight_TempFallbackOnTheSameDeviceNeedsNoCopy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize)}

	got, err := h.promoter.Preflight(preflightRequest())
	require.NoError(t, err)
	require.False(t, got.CrossDevice)
	require.True(t, got.UsedTempDir)
}

func TestPreflight_UnstattableStagingDirectory(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize)}
	h.fs.failOn("fs.Stat", tempDir, os.ErrPermission)

	_, err := h.promoter.Preflight(preflightRequest())
	requireFailure(t, err, domain.FailPreflight, "staging directory")
}
