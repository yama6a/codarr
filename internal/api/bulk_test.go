package api_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/job"
)

// plan.md 19: every bulk operation is dry-run first, and the confirmation has to
// be able to say exactly what it is about to do.

func TestRecheckAllMedia_DryRunReportsTheBreakdownAndQueuesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.RecheckAllFunc = func(_ context.Context, confirm bool) (job.RecheckResult, error) {
		require.False(t, confirm)

		return job.RecheckResult{
			DryRun:       true,
			Examined:     120,
			Count:        9,
			ByPlanKind:   job.PlanKindBreakdown{Remux: 4, AudioOnly: 3, Full: 2},
			MediaFileIDs: []int64{1, 2, 3},
			Irreversible: true,
		}, nil
	}

	got := decodeInto[gen.RecheckResult](t,
		h.do(t, "POST", "/api/media/recheck-all", gen.RecheckAllRequest{Confirm: false}), 200)

	require.True(t, got.DryRun)
	require.Equal(t, 120, got.Examined)
	require.Equal(t, 9, got.Count)
	require.Equal(t, gen.PlanKindBreakdown{AudioOnly: 3, Full: 2, Remux: 4, Skip: 0}, got.ByPlanKind)
	require.Empty(t, got.QueuedJobIds)
	require.NotNil(t, got.MediaFileIds)
	require.Equal(t, []int64{1, 2, 3}, *got.MediaFileIds)
}

func TestRecheckAllMedia_ConfirmQueues(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.RecheckAllFunc = func(_ context.Context, confirm bool) (job.RecheckResult, error) {
		require.True(t, confirm)

		return job.RecheckResult{Count: 2, QueuedJobIDs: []int64{11, 12}, Irreversible: true}, nil
	}

	got := decodeInto[gen.RecheckResult](t,
		h.do(t, "POST", "/api/media/recheck-all", gen.RecheckAllRequest{Confirm: true}), 200)

	require.False(t, got.DryRun)
	require.Equal(t, []int64{11, 12}, got.QueuedJobIds)
}

// An empty body selects nothing rather than everything, so a mis-sent request
// cannot queue the library.
func TestRecheckSelectedMedia_EmptyBodySelectsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.RecheckFunc = func(_ context.Context, req job.Recheck) (job.RecheckResult, error) {
		require.Empty(t, req.IDs)
		require.Nil(t, req.Filter)

		return job.RecheckResult{DryRun: true, Irreversible: true}, nil
	}

	got := decodeInto[gen.RecheckResult](t,
		h.do(t, "POST", "/api/media/recheck-selected", gen.RecheckSelectedRequest{}), 200)

	require.Equal(t, 0, got.Count)
	require.Empty(t, got.QueuedJobIds)
}

func TestRecheckSelectedMedia_TakesEitherIdsOrAFilter(t *testing.T) {
	t.Parallel()

	t.Run("ids", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.queue.RecheckFunc = func(_ context.Context, req job.Recheck) (job.RecheckResult, error) {
			require.Equal(t, []int64{4, 5}, req.IDs)
			require.Nil(t, req.Filter)
			require.True(t, req.Confirm)

			return job.RecheckResult{Count: 2, QueuedJobIDs: []int64{7, 8}}, nil
		}

		ids := []int64{4, 5}
		got := decodeInto[gen.RecheckResult](t, h.do(t, "POST", "/api/media/recheck-selected",
			gen.RecheckSelectedRequest{Confirm: true, Ids: &ids}), 200)

		require.Equal(t, []int64{7, 8}, got.QueuedJobIds)
	})

	t.Run("filter", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.queue.RecheckFunc = func(_ context.Context, req job.Recheck) (job.RecheckResult, error) {
			require.Empty(t, req.IDs)
			require.NotNil(t, req.Filter)
			require.Equal(t, []string{"h264"}, req.Filter.VideoCodec)

			return job.RecheckResult{DryRun: true, Count: 40_000}, nil
		}

		codec := "h264"
		got := decodeInto[gen.RecheckResult](t, h.do(t, "POST", "/api/media/recheck-selected",
			gen.RecheckSelectedRequest{Filter: &gen.MediaFilter{VideoCodec: &codec}}), 200)

		require.Equal(t, 40_000, got.Count)
	})

	t.Run("both is a bad request", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		ids := []int64{1}
		codec := "h264"

		rec := h.do(t, "POST", "/api/media/recheck-selected", gen.RecheckSelectedRequest{
			Filter: &gen.MediaFilter{VideoCodec: &codec},
			Ids:    &ids,
		})

		require.Equal(t, 400, rec.Code, rec.Body.String())
		require.Empty(t, h.queue.RecheckCalls())
	})
}

func TestPreviewSpaceSweep_ReportsTheCountAndBreakdownWithoutQueueing(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.SpaceSweepPreviewFunc = func(context.Context) (job.SpaceSweepPreview, error) {
		return job.SpaceSweepPreview{
			Count:                2,
			Examined:             30,
			ByPlanKind:           job.PlanKindBreakdown{Full: 2},
			CurrentBytes:         100,
			ProjectedBytes:       55,
			ProjectedSavingBytes: 45,
			ProjectedSavingPct:   45,
			Irreversible:         true,
			Candidates: []job.SpaceSweepCandidate{{
				MediaFileID: 1, Path: "/media/movies/a.mkv", Filename: "a.mkv",
				VideoCodec: "h264", CurrentBytes: 60, ProjectedBytes: 30,
				ProjectedSavingBytes: 30, ProjectedSavingPct: 50,
			}},
		}, nil
	}

	got := decodeInto[gen.SpaceSweepPreview](t, h.do(t, "POST", "/api/space-sweep/preview", nil), 200)

	require.Equal(t, 2, got.Count)
	require.Equal(t, 30, got.Examined)
	require.Equal(t, gen.PlanKindBreakdown{Full: 2}, got.ByPlanKind)
	require.True(t, got.Irreversible)
	require.Len(t, got.Candidates, 1)
	require.Equal(t, "a.mkv", got.Candidates[0].Filename)
	require.Empty(t, h.queue.SpaceSweepRunCalls())
}

// The run form requires confirm true. There is no undo (plan.md 15.5).
func TestRunSpaceSweep_RejectsConfirmFalse(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]any{
		"confirm false": gen.SpaceSweepRunRequest{Confirm: false},
		"empty body":    map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			rec := h.do(t, "POST", "/api/space-sweep/run", body)

			err := decodeInto[gen.Error](t, rec, 400)
			require.Equal(t, "bad_request", err.Error)
			require.Contains(t, err.Message, "cannot be undone")
			require.Empty(t, h.queue.SpaceSweepRunCalls())
		})
	}
}

func TestRunSpaceSweep_ConfirmQueues(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.queue.SpaceSweepRunFunc = func(_ context.Context, ids []int64, confirm bool) (job.SpaceSweepPreview, error) {
		require.True(t, confirm)
		require.Equal(t, []int64{3}, ids)

		return job.SpaceSweepPreview{
			Count:                1,
			ByPlanKind:           job.PlanKindBreakdown{Full: 1},
			ProjectedSavingBytes: 30,
			QueuedJobIDs:         []int64{42},
		}, nil
	}

	ids := []int64{3}
	got := decodeInto[gen.SpaceSweepRunResult](t, h.do(t, "POST", "/api/space-sweep/run",
		gen.SpaceSweepRunRequest{Confirm: true, MediaFileIds: &ids}), 200)

	require.Equal(t, 1, got.Count)
	require.Equal(t, []int64{42}, got.QueuedJobIds)
	require.Equal(t, int64(30), got.ProjectedSavingBytes)
}
