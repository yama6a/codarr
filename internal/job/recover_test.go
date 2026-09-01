package job_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/promote"
)

// promoted makes the destination look like Codarr's finished output: a
// fingerprint that no longer matches what analysis recorded, and the global
// tags of plan.md 12 carrying the current policy hash.
func promoted(h *harness) {
	h.fp.SparseFunc = func(string) (string, error) { return outputFP, nil }
	h.prober.ProbeFunc = func(_ context.Context, _ string) (*ffprobe.Result, error) {
		return parse(h.t, taggedProbe(decide.PolicyHash())), nil
	}
}

func TestService_RecoverRequeuesAJobInterruptedWhileRunning(t *testing.T) {
	t.Parallel()

	for _, state := range []domain.JobState{domain.JobRunning, domain.JobVerifying} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			j := h.interrupted(state, 0)
			h.addFile(stagingPath, sourceSize/3)

			require.NoError(t, h.svc.Recover(t.Context()))

			after := h.jobRow(j.ID)
			require.Equal(t, domain.JobQueued, after.State)
			require.Equal(t, 1, after.Attempt)
			require.Contains(t, h.removedPaths(), stagingPath, "a half-written encode is deleted")
		})
	}
}

func TestService_RecoverFailsAJobThatHitTheAutomaticRestartCap(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobRunning, domain.MaxAutoAttempts)
	h.addFile(stagingPath, sourceSize/3)

	require.NoError(t, h.svc.Recover(t.Context()))

	after := h.jobRow(j.ID)
	require.Equal(t, domain.JobFailed, after.State)
	require.Equal(t, domain.FailInterrupted, after.FailureCode)
	require.NotEmpty(t, after.FailureMessage)
	require.Contains(t, h.removedPaths(), stagingPath)
}

func TestService_RecoverRequeuesAnInterruptedPromotionWhoseRenameNeverHappened(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobPromoting, 0)
	h.addFile(stagingPath, sourceSize/2)

	// The default fingerprint still matches what analysis recorded, so the file
	// on disk is the source and the rename cannot have landed.
	require.NoError(t, h.svc.Recover(t.Context()))

	after := h.jobRow(j.ID)
	require.Equal(t, domain.JobQueued, after.State)
	require.Equal(t, 1, after.Attempt)
	require.Contains(t, h.removedPaths(), stagingPath, "the staging file is orphaned")
}

func TestService_RecoverFinishesAnInterruptedPromotionWhoseRenameLanded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobPromoting, 0)
	promoted(h)

	require.NoError(t, h.svc.Recover(t.Context()))

	after := h.jobRow(j.ID)
	require.Equal(t, domain.JobDone, after.State)
	require.Equal(t, outputFP, after.OutputFingerprint)
	require.NotNil(t, after.Transform.OutputIdentity)
	require.Equal(t, decide.PolicyHash(), after.Transform.OutputIdentity.PolicyHash)

	media := h.mediaRow()
	require.Equal(t, domain.MediaDone, media.Status)
	require.Equal(t, outputFP, media.CodarrOutputFingerprint)

	// 19.2 asks specifically for the notifications that never fired.
	require.Len(t, h.notifier.NotifyPromotedCalls(), 1)
	require.Equal(t, sourcePath, h.notifier.NotifyPromotedCalls()[0].Path)
	require.NotContains(t, h.removedPaths(), sourcePath)
}

func TestService_RecoverFailsAnInterruptedPromotionItCannotDecide(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobPromoting, 0)

	// Neither the source analysis recorded nor an output of the current policy.
	h.fp.SparseFunc = func(string) (string, error) { return "xxh3-128:deadbeef", nil }

	require.NoError(t, h.svc.Recover(t.Context()))

	h.requireFailed(j.ID, domain.FailPromote, "neither the source analysis recorded")
}

func TestService_RecoverResumesAnInterruptedPromotionWhoseOutputStillVerifies(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobAwaitingStreamEnd, 0)
	h.addFile(stagingPath, sourceSize/2)

	require.NoError(t, h.svc.Recover(t.Context()))

	// Left exactly where it was, and never deleted: it is a verified output
	// already sitting on the destination filesystem.
	require.Equal(t, domain.JobAwaitingStreamEnd, h.jobRow(j.ID).State)
	require.NotContains(t, h.removedPaths(), stagingPath)

	swept := h.promoter.SweepCalls()
	require.Len(t, swept, 1)
	require.Equal(t, []string{stagingPath}, swept[0].Claimed,
		"the orphan sweep must not delete a staging file a live job still owns")

	// The worker picks it up ahead of anything queued and finishes it.
	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)
}

func TestService_RecoverRequeuesAnInterruptedPromotionWhoseOutputNoLongerVerifies(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobAwaitingStreamEnd, 0)
	h.addFile(stagingPath, sourceSize/2)

	h.promoter.VerifyFunc = func(context.Context, promote.Request) ([]string, error) {
		return nil, &promote.Error{
			Code:    domain.FailVerification,
			Message: "the output has 2 audio streams, the plan expected 3",
		}
	}

	require.NoError(t, h.svc.Recover(t.Context()))

	after := h.jobRow(j.ID)
	require.Equal(t, domain.JobQueued, after.State)
	require.Equal(t, 1, after.Attempt)
	require.Contains(t, h.removedPaths(), stagingPath)
}

func TestService_RecoverFinishesAnAwaitingJobWhoseRenameActuallyLanded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobAwaitingStreamEnd, 0)

	// The staging file is gone because it was renamed into place: the promoter
	// reports a block but never an unblock, so the row can still say
	// awaiting_stream_end at the moment of the replace.
	promoted(h)

	require.NoError(t, h.svc.Recover(t.Context()))

	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)
	require.Equal(t, outputFP, h.mediaRow().CodarrOutputFingerprint)
	require.Len(t, h.notifier.NotifyPromotedCalls(), 1)
}

func TestService_RecoverSweepsOrphansAgainstEveryRoot(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.roots = append(h.store.roots, domain.Root{ID: 2, Path: "/media/tv", Enabled: false})

	require.NoError(t, h.svc.Recover(t.Context()))

	swept := h.promoter.SweepCalls()
	require.Len(t, swept, 1)
	require.Equal(t, []string{"/media/movies", "/media/tv"}, swept[0].Roots,
		"debris in a disabled root is still debris")
	require.Empty(t, swept[0].Claimed)
}

func TestService_RecoverLeavesTerminalJobsAlone(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	done := h.store.putJob(domain.Job{MediaFileID: mediaID, State: domain.JobDone, Kind: domain.KindAudioOnly})
	failed := h.store.putJob(domain.Job{MediaFileID: 8, State: domain.JobFailed, Kind: domain.KindFull})
	queued := h.store.putJob(domain.Job{MediaFileID: 9, State: domain.JobQueued, Kind: domain.KindRemux})

	require.NoError(t, h.svc.Recover(t.Context()))

	require.Equal(t, domain.JobDone, h.jobRow(done.ID).State)
	require.Equal(t, domain.JobFailed, h.jobRow(failed.ID).State)
	require.Equal(t, domain.JobQueued, h.jobRow(queued.ID).State)
}

// plan.md 15.1: the temp directory is the fallback when the destination lacks
// space, and rename(2) is not atomic across filesystems. CrossDevice is not a
// column, so resuming has to re-measure it.
func TestService_ResumeRebuildsCrossDeviceStagingFromTheTempDirectory(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interruptedInTemp(domain.JobAwaitingStreamEnd)
	h.addFile(tempDir+"/.codarr-staging-1.mkv", sourceSize/2)
	h.setDevice(tempDir, 2)

	require.NoError(t, h.svc.Recover(t.Context()))

	verified := h.promoter.VerifyCalls()
	require.Len(t, verified, 1)
	require.Equal(t, promote.Staging{
		Path:        tempDir + "/.codarr-staging-1.mkv",
		FinalPath:   destDir + "/.codarr-staging-1.mkv",
		UsedTempDir: true,
		CrossDevice: true,
	}, verified[0].Req.Staging)

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	promotedReq := h.promoter.PromoteCalls()
	require.Len(t, promotedReq, 1)
	require.True(t, promotedReq[0].Req.Staging.CrossDevice)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)
}

func TestService_ResumeIgnoresAJobThatIsNoLongerAwaiting(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	j := h.interrupted(domain.JobAwaitingStreamEnd, 0)
	h.addFile(stagingPath, sourceSize/2)

	require.NoError(t, h.svc.Recover(t.Context()))
	require.NoError(t, h.svc.Cancel(t.Context(), j.ID))

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran, "the pending entry is still consumed")
	require.Equal(t, domain.JobCancelled, h.jobRow(j.ID).State)
	require.Empty(t, h.promoter.PromoteCalls())
}

// plan.md 15.3's legacy-container fallback compares the output against ffmpeg's
// own out_time, because a VOB or AVI header routinely lies about duration. That
// value is written when the encode ends and read back here, because 19.2
// resumes the promotion in a process that never ran the encode.
func TestService_ResumedLegacyContainerJobStillHasTheDurationFallback(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFileAt(legacyPath, legacyProbe()))
	h.addFile(legacyPath, sourceSize)

	staging := destDir + "/.codarr-staging-1.mkv"
	started := h.clk.Now().Add(-time.Hour)

	j := h.store.putJob(domain.Job{
		MediaFileID:    mediaID,
		Kind:           domain.KindAudioOnly,
		Origin:         domain.OriginIngest,
		Priority:       domain.PriorityQuick,
		State:          domain.JobAwaitingStreamEnd,
		StagingPath:    staging,
		FinalOutTimeUS: 7_200_000_000,
		StartedAt:      &started,
		QueuedAt:       h.clk.Now().Add(-2 * time.Hour),
	})
	h.addFile(staging, sourceSize/2)

	require.NoError(t, h.svc.Recover(t.Context()))

	verified := h.promoter.VerifyCalls()
	require.Len(t, verified, 1)
	require.True(t, verified[0].Req.Source.LegacyContainer)
	require.InDelta(t, mediaDur, verified[0].Req.FinalOutTimeSeconds, 0.001,
		"the re-verification on resume needs the fallback too, not just the promotion")

	ran, err := h.svc.RunOnce(t.Context())
	require.NoError(t, err)
	require.True(t, ran)

	promoteCalls := h.promoter.PromoteCalls()
	require.Len(t, promoteCalls, 1)
	require.True(t, promoteCalls[0].Req.Source.LegacyContainer)
	require.InDelta(t, mediaDur, promoteCalls[0].Req.FinalOutTimeSeconds, 0.001)
	require.Equal(t, domain.JobDone, h.jobRow(j.ID).State)
}
