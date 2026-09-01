package job_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffmpeg"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// 10 MB per 60-second sample is 1.33 Mbps, lifted to the 2.5 Mbps 1080p floor;
// against an 8.42 Mbps source that clears the threshold comfortably.
const sweepSampleSize = int64(10_000_000)

// samplesOfSize answers every sample probe with a file of the given size and
// nothing else, which is all the bulk operations run.
func samplesOfSize(h *harness, size int64) {
	h.encoder.RunFunc = func(_ context.Context, args []string, _ func(ffmpeg.Progress)) (ffmpeg.RunResult, error) {
		h.recordRun(args)

		out := args[len(args)-1]
		if strings.Contains(out, ".codarr-probe-") {
			h.addFile(out, size)

			return ffmpeg.RunResult{Argv: args}, nil
		}

		return ffmpeg.RunResult{Argv: args, FinalOutTime: time.Duration(mediaDur) * time.Second}, nil
	}
}

func TestService_RecheckAllPreviewsWithoutQueueingAnything(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))
	h.store.putMedia(withID(12, skipProbe(), nil))
	h.store.putMedia(withID(13, fullProbe(), nil))

	res, err := h.svc.RecheckAll(t.Context(), false)
	require.NoError(t, err)

	require.True(t, res.DryRun)
	require.True(t, res.Irreversible)
	require.Equal(t, 3, res.Examined, "only the done files are re-checked")
	require.Equal(t, 2, res.Count, "the one already matching the policy is skipped")
	require.Equal(t, job.PlanKindBreakdown{AudioOnly: 1, Full: 1}, res.ByPlanKind)
	require.Equal(t, []int64{11, 13}, res.MediaFileIDs)
	require.Empty(t, res.QueuedJobIDs)
	require.Len(t, h.analyzer.AnalyzeCalls(), 3, "a re-check re-probes before it decides")
}

func TestService_RecheckAllQueuesOnceConfirmed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))
	h.store.putMedia(withID(12, skipProbe(), nil))

	res, err := h.svc.RecheckAll(t.Context(), true)
	require.NoError(t, err)

	require.False(t, res.DryRun)
	require.Equal(t, 1, res.Count)
	require.Len(t, res.QueuedJobIDs, 1)
	require.Equal(t, domain.OriginRecheck, h.jobRow(res.QueuedJobIDs[0]).Origin)
}

// decisions.md: an empty request selects nothing rather than everything, so a
// mis-sent body cannot queue the library.
func TestService_RecheckWithNeitherIdsNorFilterSelectsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))

	res, err := h.svc.Recheck(t.Context(), job.Recheck{Confirm: true})
	require.NoError(t, err)
	require.Equal(t, 0, res.Examined)
	require.Empty(t, res.QueuedJobIDs)
}

func TestService_RecheckSelectedTakesIdsOrAFilter(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))
	h.store.putMedia(withID(12, audioOnlyProbe(), nil))

	byID, err := h.svc.Recheck(t.Context(), job.Recheck{IDs: []int64{12}})
	require.NoError(t, err)
	require.Equal(t, []int64{12}, byID.MediaFileIDs)

	byFilter, err := h.svc.Recheck(t.Context(), job.Recheck{
		Filter: &store.MediaFilter{Status: []domain.MediaStatus{domain.MediaDone}},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{11, 12}, byFilter.MediaFileIDs)
}

func TestService_RecheckSkipsAFileItCannotReanalyse(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(withID(11, audioOnlyProbe(), nil))
	h.store.putMedia(withID(12, audioOnlyProbe(), nil))

	h.analyzer.AnalyzeFunc = func(_ context.Context, m domain.MediaFile) (domain.MediaFile, error) {
		if m.ID == 11 {
			return domain.MediaFile{}, errors.New("ffprobe: exit status 1")
		}

		return m, nil
	}

	res, err := h.svc.Recheck(t.Context(), job.Recheck{IDs: []int64{11, 12}, Confirm: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Examined)
	require.Equal(t, []int64{12}, res.MediaFileIDs)
}

// plan.md 11: the whole point of measuring per file is to spare the ones that
// are large because the content is complex.
func TestService_SpaceSweepRefusesAFileBelowTheSavingThreshold(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	samplesOfSize(h, 30_000_000)

	res, err := h.svc.SpaceSweepPreview(t.Context())
	require.NoError(t, err)

	require.Equal(t, 1, res.Examined, "the file cleared the bitrate filter and was sample-probed")
	require.Equal(t, 0, res.Count, "and then failed the 35% test")
	require.Empty(t, res.Candidates)
	require.Zero(t, res.ProjectedSavingBytes)
}

func TestService_SpaceSweepKeepsAFileAboveTheSavingThreshold(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	samplesOfSize(h, sweepSampleSize)

	res, err := h.svc.SpaceSweepPreview(t.Context())
	require.NoError(t, err)

	require.True(t, res.Irreversible)
	require.Equal(t, 1, res.Count)
	require.Equal(t, job.PlanKindBreakdown{Full: 1}, res.ByPlanKind)
	require.Empty(t, res.QueuedJobIDs, "a preview queues nothing")

	candidate := res.Candidates[0]
	require.Equal(t, mediaID, candidate.MediaFileID)
	require.Equal(t, "Example.mkv", candidate.Filename)
	require.Equal(t, "h264", candidate.VideoCodec)
	require.Equal(t, 8420, candidate.CurrentVideoBitrateKbps)
	require.Equal(t, 2500, candidate.TargetVideoBitrateKbps, "the 1080p floor of 8.3 lifts the measurement")
	require.Equal(t, sourceSize, candidate.CurrentBytes)
	require.Greater(t, candidate.ProjectedSavingPct, job.SweepMinSavingPct)
	require.Equal(t, candidate.CurrentBytes-candidate.ProjectedBytes, candidate.ProjectedSavingBytes)
	require.Equal(t, res.ProjectedSavingBytes, candidate.ProjectedSavingBytes)
}

func TestService_SpaceSweepIgnoresFilesTheFilterExcludes(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	samplesOfSize(h, sweepSampleSize)

	// Below the 8 Mbps floor.
	h.store.putMedia(withID(11, audioOnlyProbe(), func(m *domain.MediaFile) { m.VideoBitrate = 4_000_000 }))
	// Already HEVC.
	h.store.putMedia(withID(12, audioOnlyProbe(), func(m *domain.MediaFile) { m.VideoCodec = "hevc" }))
	// On the ignore list.
	h.store.putMedia(withID(13, audioOnlyProbe(), func(m *domain.MediaFile) { m.Ignored = true }))

	res, err := h.svc.SpaceSweepPreview(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, res.Examined)
	require.Equal(t, mediaID, res.Candidates[0].MediaFileID)
}

// plan.md 15.5: the confirmation is mandatory, because every queued job
// replaces its source in place and there is no undo.
func TestService_SpaceSweepRunRefusesWithoutConfirmation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.svc.SpaceSweepRun(t.Context(), nil, false)
	require.ErrorIs(t, err, job.ErrConfirmationRequired)
	require.Empty(t, h.runArgs(), "a refused run does not even probe")
}

func TestService_SpaceSweepRunQueuesTheCandidatesItReEvaluated(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	samplesOfSize(h, sweepSampleSize)

	res, err := h.svc.SpaceSweepRun(t.Context(), []int64{mediaID}, true)
	require.NoError(t, err)
	require.Equal(t, 1, res.Count)
	require.Len(t, res.QueuedJobIDs, 1)

	queued := h.jobRow(res.QueuedJobIDs[0])
	require.Equal(t, domain.OriginSpaceSweep, queued.Origin)
	require.Equal(t, domain.KindFull, queued.Kind, "the sweep re-encodes video the policy would copy")
}

func TestService_SpaceSweepRunSkipsAFileThatNoLongerClearsTheThreshold(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	samplesOfSize(h, 30_000_000)

	res, err := h.svc.SpaceSweepRun(t.Context(), []int64{mediaID}, true)
	require.NoError(t, err)
	require.Equal(t, 0, res.Count)
	require.Empty(t, res.QueuedJobIDs, "the preview is re-evaluated, never trusted")
}
