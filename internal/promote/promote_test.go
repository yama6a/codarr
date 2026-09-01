package promote_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/fsx"
	"github.com/yama6a/codarr/internal/promote"
)

func TestPromote_ReplacesTheSourceAndRecordsTheIdentity(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)
	require.Equal(t, promote.Result{
		Renamed: true,
		Identity: domain.OutputIdentity{
			Fingerprint: "xxh3-128:" + strings.Repeat("a", 32),
			SizeBytes:   sourceSize / 2,
			MTime:       1700000000,
			PolicyHash:  "policy-abc",
			RecordedAt:  time.Unix(1700009999, 0).UTC(),
		},
		OutputSize: sourceSize / 2,
	}, got)

	_, staged := h.fs.get(stagingPath)
	require.False(t, staged, "the staging file should have been renamed away")

	promoted, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, sourceSize/2, promoted.size)
	require.Equal(t, os.FileMode(0o640), promoted.mode)
	require.Equal(t, time.Unix(1700000000, 0).UTC(), promoted.mtime)
	require.Equal(t, 568, promoted.uid)
}

// plan.md 15.2: the whole ordered sequence, including the re-check that has to
// sit immediately before the rename.
func TestPromote_FollowsTheSequenceInOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)

	require.Equal(t, []string{
		"ffprobe.Probe " + stagingPath,
		"fs.Stat " + stagingPath,
		"fs.Stat " + sourcePath,
		"plex.IsStreaming " + sourcePath,
		"fs.SyncFile " + stagingPath,
		"fs.SyncDir " + destDir,
		"plex.IsStreaming " + sourcePath,
		"fs.Rename " + stagingPath + " -> " + sourcePath,
		"fs.Chown " + sourcePath,
		"fs.Chmod " + sourcePath,
		"fs.Chtimes " + sourcePath,
		"fs.Stat " + sourcePath,
		"fingerprint.Sparse " + sourcePath,
		"notify.Promoted " + sourcePath,
	}, h.rec.list())
}

// plan.md 15.6: no logging, no database write, no allocation between the final
// Plex check and the rename. Nothing at all may be recorded between them.
func TestPromote_NothingHappensBetweenTheFinalPlexCheckAndTheRename(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)

	requireRenameFollowsTheFinalCheck(t, h.rec.list())
}

// plan.md 15.2 step 9: the identity is recorded after the metadata restore,
// because restoring mtime changes what a later scan compares against.
func TestPromote_FingerprintsAfterTheMetadataRestore(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)

	calls := h.rec.list()
	require.Less(t, indexOf(t, calls, "fs.Chtimes "+sourcePath), indexOf(t, calls, "fingerprint.Sparse "+sourcePath))
}

func TestPromote_DefersWhilePlexIsStreaming(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	answers := []bool{true, true, false, false}
	i := 0
	h.guard.IsStreamingFunc = func(_ context.Context, path string) (bool, string, error) {
		h.rec.add("plex.IsStreaming", path)

		streaming := answers[i]
		i++

		return streaming, "Kostas on Chrome", nil
	}

	var blocked []string

	req := request()
	req.OnBlocked = func(reason string) { blocked = append(blocked, reason) }

	_, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, []string{"Kostas on Chrome", "Kostas on Chrome"}, blocked)
	require.Equal(t, 2*promote.DefaultStreamRetry, h.clk.Now().Sub(time.Unix(1700009999, 0).UTC()))
}

// A stream that starts inside the re-check window sends the job back to
// waiting; nothing is renamed.
func TestPromote_StreamStartingInTheFinalWindowDefersTheRename(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	answers := []bool{false, true, false, false}
	i := 0
	h.guard.IsStreamingFunc = func(_ context.Context, path string) (bool, string, error) {
		h.rec.add("plex.IsStreaming", path)

		streaming := answers[i]
		i++

		return streaming, "Yama on the TV", nil
	}

	var blocked []string

	req := request()
	req.OnBlocked = func(reason string) { blocked = append(blocked, reason) }

	_, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, []string{"Yama on the TV"}, blocked)

	calls := h.rec.list()
	require.Equal(t, 2, countOf(calls, "fs.SyncFile "+stagingPath))
	requireRenameFollowsTheFinalCheck(t, calls)
}

// An unreachable Plex defers rather than fails: awaiting_stream_end is just as
// closed as failing, and it keeps the finished encode instead of discarding it.
func TestPromote_PlexErrorDefersAndRetries(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	attempts := 0
	h.guard.IsStreamingFunc = func(_ context.Context, path string) (bool, string, error) {
		h.rec.add("plex.IsStreaming", path)
		attempts++

		if attempts <= 2 {
			return false, "", errors.New("connection refused")
		}

		return false, "", nil
	}

	var blocked []string

	req := request()
	req.OnBlocked = func(reason string) { blocked = append(blocked, reason) }

	got, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.True(t, got.Renamed)
	require.Len(t, blocked, 2)
	require.Contains(t, blocked[0], "connection refused")

	_, ok := h.fs.get(sourcePath)
	require.True(t, ok)
}

// The same rule inside the final window: an error there is a deferral, not a
// rename and not a failure.
func TestPromote_PlexErrorInTheFinalWindowDefers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	attempts := 0
	h.guard.IsStreamingFunc = func(_ context.Context, path string) (bool, string, error) {
		h.rec.add("plex.IsStreaming", path)
		attempts++

		if attempts == 2 {
			return false, "", errors.New("connection reset")
		}

		return false, "", nil
	}

	var blocked []string

	req := request()
	req.OnBlocked = func(reason string) { blocked = append(blocked, reason) }

	_, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, blocked, 1)
	require.Contains(t, blocked[0], "connection reset")
	requireRenameFollowsTheFinalCheck(t, h.rec.list())
}

func TestPromote_CancelledWhileWaitingForAStreamToEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	h := newHarness(t)
	h.guard.IsStreamingFunc = func(context.Context, string) (bool, string, error) {
		cancel()

		return true, "Kostas on Chrome", nil
	}

	got, err := h.promoter.Promote(ctx, request())
	requireFailure(t, err, domain.FailPromote, "was cancelled")
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, got.Renamed)
}

func TestPromote_VerificationFailureLeavesTheSourceAlone(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.probeReturns(func(o *promote.Output) { o.DurationSeconds = 1 })

	_, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailVerification, "output duration 1s differs from source 5121s")

	source, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, sourceSize, source.size)

	_, ok = h.fs.get(stagingPath)
	require.True(t, ok)

	require.Empty(t, h.notifier.NotifyPromotedCalls())
}

// plan.md 15.4's nlink guard is re-checked at the last moment: preflight ran
// before an encode that may have taken hours.
func TestPromote_HardlinkAppearingDuringTheEncodeStopsTheReplace(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	source, ok := h.fs.get(sourcePath)
	require.True(t, ok)

	source.nlink = 3

	_, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "gained hard links during the encode (nlink 3)")
	require.Empty(t, h.fs.synced)
}

func TestPromote_MissingSourceAtReplaceTime(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	require.NoError(t, h.fs.Remove(sourcePath))

	_, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "could not be stat'd before the replace")
}

// plan.md 12.2: the whole-file hash is over the staging file, after
// verification and before promotion.
func TestPromote_FullHashWhenEnabled(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.FullHashEnabled = true

	got, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, got.Identity.FullHash)
	require.Equal(t, "xxh3-128:"+strings.Repeat("b", 32), *got.Identity.FullHash)
	require.Equal(t, []string{stagingPath}, fullHashPaths(h))
}

func TestPromote_FullHashOffByDefault(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)
	require.Nil(t, got.Identity.FullHash)
	require.Empty(t, h.fp.FullCalls())
}

func TestPromote_FullHashFailureStopsTheReplace(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fp.FullFunc = func(string) (string, error) { return "", os.ErrPermission }

	req := request()
	req.FullHashEnabled = true

	_, err := h.promoter.Promote(t.Context(), req)
	requireFailure(t, err, domain.FailPromote, "computing the whole-file hash")

	_, ok := h.fs.get(sourcePath)
	require.True(t, ok)
}

// plan.md 15.6: root_squash denies chown, and that is never a job failure.
func TestPromote_ChownFailureIsAWarningNotAFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Chown", sourcePath, os.ErrPermission)

	got, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)
	require.Len(t, got.Warnings, 1)
	require.Contains(t, got.Warnings[0], "ownership 568:568 was not restored")
	require.Contains(t, got.Warnings[0], "root_squash")

	promoted, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, os.FileMode(0o640), promoted.mode)
	require.Equal(t, time.Unix(1700000000, 0).UTC(), promoted.mtime)
}

func TestPromote_ChmodFailureIsAJobFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Chmod", sourcePath, os.ErrPermission)

	got, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "the replace succeeded but restoring mode 640")

	// The source is gone, so the caller must still be handed the identity.
	require.True(t, got.Renamed)
	require.NotEmpty(t, got.Identity.Fingerprint)
	require.Equal(t, sourceSize/2, got.Identity.SizeBytes)

	// mtime is restored even though the mode restore failed.
	promoted, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, time.Unix(1700000000, 0).UTC(), promoted.mtime)
}

func TestPromote_ChtimesFailureIsAJobFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Chtimes", sourcePath, os.ErrPermission)

	got, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "restoring the modification time")
	require.True(t, got.Renamed)
	require.NotEmpty(t, got.Identity.Fingerprint)
}

func TestPromote_FingerprintFailureAfterTheReplaceIsAJobFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fp.SparseFunc = func(string) (string, error) { return "", os.ErrPermission }

	got, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "would be re-encoded on the next scan")

	// The only case where Renamed is true and there is no identity to persist.
	require.True(t, got.Renamed)
	require.Empty(t, got.Identity.Fingerprint)
}

func TestPromote_SyncFailureStopsTheReplace(t *testing.T) {
	t.Parallel()

	for name, path := range map[string]string{"file": "fs.SyncFile " + stagingPath, "dir": "fs.SyncDir " + destDir} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			op, target, _ := strings.Cut(path, " ")
			h.fs.failOn(op, target, os.ErrPermission)

			_, err := h.promoter.Promote(t.Context(), request())
			requireFailure(t, err, domain.FailPromote, "fsync of the")

			source, ok := h.fs.get(sourcePath)
			require.True(t, ok)
			require.Equal(t, sourceSize, source.size)
		})
	}
}

func TestPromote_RenameFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fs.failOn("fs.Rename", stagingPath+" -> "+sourcePath, os.ErrPermission)

	_, err := h.promoter.Promote(t.Context(), request())
	requireFailure(t, err, domain.FailPromote, "replacing "+sourcePath+" with the staged output failed")
}

// plan.md 15.1: rename(2) is not atomic across filesystems, so a cross-device
// staging file is copied to a destination-side sibling and that is what moves.
func TestPromote_CrossDeviceStagingIsCopiedFirst(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tempStaging := tempDir + "/.codarr-staging-42.mkv"
	h.fs.addFile(tempStaging, sourceSize/2)

	req := request()
	req.Staging = promote.Staging{Path: tempStaging, FinalPath: stagingPath, UsedTempDir: true, CrossDevice: true}

	_, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)

	calls := h.rec.list()
	require.Contains(t, calls, "fs.Copy "+tempStaging+" -> "+stagingPath)
	requireRenameFollowsTheFinalCheck(t, calls)
}

// A temp-dir staging file that happens to be on the destination filesystem
// needs no copy: rename() will not return EXDEV.
func TestPromote_TempStagingOnTheSameDeviceIsRenamedDirectly(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tempStaging := tempDir + "/.codarr-staging-42.mkv"
	h.fs.addFile(tempStaging, sourceSize/2)

	req := request()
	req.Staging = promote.Staging{Path: tempStaging, FinalPath: stagingPath, UsedTempDir: true}

	_, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.Empty(t, h.copier.CopyCalls())
	require.Contains(t, h.rec.list(), "fs.Rename "+tempStaging+" -> "+sourcePath)
}

// plan.md 15.1 gated preflight on 1.2x the SOURCE size, but the copy back is of
// the OUTPUT. Without this check the copy dies mid-write and leaves a partial
// dotfile.
func TestPromote_CrossDeviceCopyChecksRoomForTheOutputFirst(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tempStaging := tempDir + "/.codarr-staging-42.mkv"
	h.fs.addFile(tempStaging, sourceSize/2)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize/2) - 1}

	req := request()
	req.Staging = promote.Staging{Path: tempStaging, FinalPath: stagingPath, UsedTempDir: true, CrossDevice: true}

	got, err := h.promoter.Promote(t.Context(), req)
	requireFailure(t, err, domain.FailPreflight, "is not enough for the 4294967296 byte staged output")
	require.False(t, got.Renamed)
	require.Empty(t, h.copier.CopyCalls())

	source, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, sourceSize, source.size)
}

func TestPromote_CrossDeviceCopyProceedsWhenTheOutputFits(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	tempStaging := tempDir + "/.codarr-staging-42.mkv"
	h.fs.addFile(tempStaging, sourceSize/2)
	h.fs.space[destDir] = fsx.SpaceInfo{FreeBytes: uint64(sourceSize / 2)}

	req := request()
	req.Staging = promote.Staging{Path: tempStaging, FinalPath: stagingPath, UsedTempDir: true, CrossDevice: true}

	got, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.True(t, got.Renamed)
}

func TestPromote_CopyFailureStopsTheReplace(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.copier.CopyFunc = func(context.Context, string, string) (int64, error) { return 0, os.ErrPermission }

	tempStaging := tempDir + "/.codarr-staging-42.mkv"
	h.fs.addFile(tempStaging, sourceSize/2)

	req := request()
	req.Staging = promote.Staging{Path: tempStaging, FinalPath: stagingPath, UsedTempDir: true, CrossDevice: true}

	_, err := h.promoter.Promote(t.Context(), req)
	requireFailure(t, err, domain.FailPromote, "onto the destination filesystem")

	source, ok := h.fs.get(sourcePath)
	require.True(t, ok)
	require.Equal(t, sourceSize, source.size)
}

// Notification happens after the source is already gone, so it cannot fail the job.
func TestPromote_NotificationFailureIsAWarning(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.notifier.NotifyPromotedFunc = func(context.Context, string) error { return errors.New("radarr timed out") }

	got, err := h.promoter.Promote(t.Context(), request())
	require.NoError(t, err)
	require.Len(t, got.Warnings, 1)
	require.Contains(t, got.Warnings[0], "radarr timed out")
	require.NotEmpty(t, got.Identity.Fingerprint)
}

func TestPromote_CarriesVerificationWarningsThrough(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	req := request()
	req.Plan.DolbyVision = true
	req.Plan.DolbyVisionProfile = 7

	got, err := h.promoter.Promote(t.Context(), req)
	require.NoError(t, err)
	require.Len(t, got.Warnings, 1)
	require.Contains(t, got.Warnings[0], "degrades to HDR10")
}

func TestNew_DefaultsTheRetryInterval(t *testing.T) {
	t.Parallel()

	require.NotNil(t, promote.New(promote.Deps{}))
}

func TestFSCopier_CopiesThroughTheRealFilesystem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src, dst := dir+"/source.mkv", dir+"/.codarr-staging-1.mkv"
	body := []byte("a staged output")
	require.NoError(t, os.WriteFile(src, body, 0o600))

	n, err := promote.NewFSCopier(fsx.OS()).Copy(t.Context(), src, dst)
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), n)

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestFSCopier_WrapsAFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := promote.NewFSCopier(fsx.OS()).Copy(t.Context(), dir+"/missing.mkv", dir+"/out.mkv")
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, err.Error(), "copy staged output")
}

func indexOf(t *testing.T, calls []string, want string) int {
	t.Helper()

	for i, c := range calls {
		if c == want {
			return i
		}
	}

	require.Failf(t, "call not found", "%q is not in %v", want, calls)

	return -1
}

func countOf(calls []string, want string) int {
	n := 0

	for _, c := range calls {
		if c == want {
			n++
		}
	}

	return n
}

func fullHashPaths(h *harness) []string {
	calls := h.fp.FullCalls()
	out := make([]string, 0, len(calls))

	for _, c := range calls {
		out = append(out, c.Path)
	}

	return out
}
