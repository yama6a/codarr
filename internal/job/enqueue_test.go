package job_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

func TestService_EnqueueQueuesAnAnalysedFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)
	require.True(t, res.Enqueued)
	require.NotNil(t, res.JobID)
	require.Equal(t, domain.KindAudioOnly, res.PlanKind)

	queued := h.jobRow(*res.JobID)
	require.Equal(t, domain.JobQueued, queued.State)
	require.Equal(t, domain.OriginIngest, queued.Origin)
	require.Equal(t, domain.MediaQueued, h.mediaRow().Status)

	// 14.3: the estimate is written at enqueue, on the transform record.
	require.Positive(t, queued.Transform.Duration.Estimated)
	require.Nil(t, queued.Transform.Duration.Actual)
}

// plan.md 17.1 and the section 26 checklist: never create a second active job
// for the same file.
func TestService_EnqueueIsIdempotent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	first, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)
	require.True(t, first.Enqueued)

	second, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginManual)
	require.NoError(t, err)
	require.False(t, second.Enqueued, "a duplicate enqueue is a no-op, not an error")
	require.Equal(t, first.JobID, second.JobID)
	require.Contains(t, second.Reason, "already")
	require.NotContains(t, h.store.callList(), "EnqueueJob:duplicate",
		"the active-job check answers before the insert is attempted")
}

func TestService_EnqueueRefusesFilesThatAreNotWork(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		arrange func(h *harness)
		reason  string
	}{
		{
			name: "already matches the policy",
			arrange: func(h *harness) {
				h.store.putMedia(mediaFile(skipProbe()))
			},
			reason: "already matches the policy",
		},
		{
			name: "on the ignore list",
			arrange: func(h *harness) {
				m := mediaFile(audioOnlyProbe())
				m.Ignored = true
				h.store.putMedia(m)
			},
			reason: "ignore list",
		},
		{
			name: "never analysed",
			arrange: func(h *harness) {
				m := mediaFile(audioOnlyProbe())
				m.Plan = nil
				m.AnalyzedAt = nil
				h.store.putMedia(m)
			},
			reason: "not been analysed",
		},
		{
			name: "missing from disk",
			arrange: func(h *harness) {
				m := mediaFile(audioOnlyProbe())
				m.Status = domain.MediaMissing
				h.store.putMedia(m)
			},
			reason: "missing from disk",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			tc.arrange(h)

			res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginManual)
			require.NoError(t, err)
			require.False(t, res.Enqueued)
			require.Contains(t, res.Reason, tc.reason)
			require.Nil(t, res.JobID)
		})
	}
}

// plan.md 19: quick wins clear first, so the I/O bound kinds outrank encodes.
func TestService_EnqueuePriority(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		probe    func() ffprobe.Result
		quick    bool
		kind     domain.Kind
		priority int
	}{
		{"audio_only ahead of full", audioOnlyProbe, true, domain.KindAudioOnly, domain.PriorityQuick},
		{"full behind everything", fullProbe, true, domain.KindFull, domain.PriorityFull},
		{"audio_only without the setting", audioOnlyProbe, false, domain.KindAudioOnly, domain.PriorityNormal},
		{"full without the setting", fullProbe, false, domain.KindFull, domain.PriorityNormal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.store.settings.PrioritiseQuickJobs = tc.quick
			h.store.putMedia(mediaFile(tc.probe()))

			res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
			require.NoError(t, err)
			require.True(t, res.Enqueued)
			require.Equal(t, tc.kind, res.PlanKind)
			require.Equal(t, tc.priority, h.jobRow(*res.JobID).Priority)
		})
	}
}

// plan.md 17.2: for a full job the target bitrate is unknown at enqueue,
// because the sample probe has not run. The UI shows "calculating".
func TestService_EnqueueLeavesTheFullJobBitrateUnknown(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.store.putMedia(mediaFile(fullProbe()))

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginIngest)
	require.NoError(t, err)

	transform := h.jobRow(*res.JobID).Transform
	require.Equal(t, domain.DecisionEncode, transform.Video.Action)
	require.NotNil(t, transform.Video.Before)
	require.NotNil(t, transform.Video.After)
	require.Nil(t, transform.Video.After.BitrateKbps, "the UI shows \"calculating\" until the job starts")
}

// plan.md 11: the sweep is the one path that re-encodes video the policy would
// copy, and origin is what carries that intent into the queue.
func TestService_EnqueueForTheSpaceSweepPlansAFullJob(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginSpaceSweep)
	require.NoError(t, err)
	require.True(t, res.Enqueued)
	require.Equal(t, domain.KindFull, res.PlanKind, "the file plans as audio_only, the sweep makes it full")

	queued := h.jobRow(*res.JobID)
	require.Equal(t, domain.KindFull, queued.Kind)
	require.Equal(t, domain.PriorityFull, queued.Priority)
	require.Equal(t, domain.DecisionEncode, queued.Transform.Video.Action)
}

// plan.md 9: profile 5 Dolby Vision video is never re-encoded, whatever it
// would save.
func TestService_EnqueueForTheSpaceSweepRefusesDolbyVisionProfile5(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	m := mediaFile(audioOnlyProbe())
	plan := *m.Plan
	plan.DolbyVision = true
	plan.DolbyVisionProfile = 5
	m.Plan = &plan
	h.store.putMedia(m)

	res, err := h.svc.Enqueue(t.Context(), mediaID, domain.OriginSpaceSweep)
	require.NoError(t, err)
	require.False(t, res.Enqueued)
	require.Contains(t, res.Reason, "nothing to re-encode")
}
