package job

import (
	"context"
	"fmt"
	"strings"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// EnqueueResult is what one enqueue did. Enqueue is idempotent, so "nothing,
// and here is why" is a normal answer rather than an error (17.1).
type EnqueueResult struct {
	MediaFileID int64
	Enqueued    bool
	JobID       *int64
	PlanKind    domain.Kind
	Reason      string
}

// Enqueue queues one file, where nothing to do is a no-op with a reason rather
// than an error; the partial unique index collapses a race into one job (17.1).
func (s *Service) Enqueue(ctx context.Context, mediaFileID int64, origin domain.JobOrigin) (EnqueueResult, error) {
	media, err := s.store.GetMediaFile(ctx, mediaFileID)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("loading media file %d: %w", mediaFileID, err)
	}

	return s.enqueue(ctx, media, origin)
}

func (s *Service) enqueue(ctx context.Context, media domain.MediaFile, origin domain.JobOrigin) (EnqueueResult, error) {
	res := EnqueueResult{MediaFileID: media.ID, PlanKind: media.PlanKind}

	if reason, ok := blockedFromQueue(media); !ok {
		res.Reason = reason

		return res, nil
	}

	plan, ok := planFor(origin, *media.Plan)
	if !ok {
		res.Reason = "the current policy plans this file's video as a copy, so the space sweep has nothing to re-encode"

		return res, nil
	}

	res.PlanKind = plan.Kind

	active, found, err := s.store.ActiveJobForMedia(ctx, media.ID)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("looking for an active job on media file %d: %w", media.ID, err)
	}

	if found {
		id := active.ID
		res.JobID = &id
		res.Reason = fmt.Sprintf("job %d is already %s for this file", active.ID, active.State)

		return res, nil
	}

	return s.insert(ctx, media, plan, origin, res)
}

func (s *Service) insert(
	ctx context.Context,
	media domain.MediaFile,
	plan domain.Plan,
	origin domain.JobOrigin,
	res EnqueueResult,
) (EnqueueResult, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("reading the queue settings: %w", err)
	}

	probe, err := storedProbe(media)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("reading the stored probe of media file %d: %w", media.ID, err)
	}

	// plan.md 14.3: rough for a full job, because neither the sample probe nor the
	// encoder choice has happened; the worker refines it when the job starts.
	estimate := s.est.Estimate(ctx, work{
		kind:         plan.Kind,
		sourceBytes:  media.SizeBytes,
		mediaSeconds: probe.Duration(),
		resolution:   resolutionOf(probe),
	})

	created, ok, err := s.store.EnqueueJob(ctx, domain.Job{
		MediaFileID: media.ID,
		Kind:        plan.Kind,
		Origin:      origin,
		Priority:    priorityFor(plan.Kind, settings.PrioritiseQuickJobs),
		Transform:   decide.NewTransform(probe, plan, estimate),
		QueuedAt:    s.clk.Now(),
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueueing media file %d: %w", media.ID, err)
	}

	if !ok {
		res.Reason = "this file already has an active job"

		return res, nil
	}

	res.Enqueued = true
	res.JobID = &created.ID
	res.Reason = "queued as " + string(plan.Kind)

	s.notify()
	s.observe(ctx, domain.JobQueued, created.Kind, created.Origin)

	return res, nil
}

// blockedFromQueue is every reason a file is not work, in the order the UI
// should explain them.
func blockedFromQueue(m domain.MediaFile) (string, bool) {
	switch {
	case m.Ignored:
		return "the file is on the ignore list", false
	case m.Plan == nil || m.AnalyzedAt == nil:
		return "the file has not been analysed yet", false
	case m.Status == domain.MediaMissing:
		return "the file is missing from disk", false
	case m.PlanKind == domain.KindSkip:
		return skipReason(m), false
	default:
		return "", true
	}
}

func skipReason(m domain.MediaFile) string {
	if len(m.PlanReasons) > 0 {
		return "the file already matches the policy: " + strings.Join(m.PlanReasons, "; ")
	}

	return "the file already matches the policy"
}

// sweepReason is what the space sweep records against the stream it forces.
const sweepReason = "space reclaim sweep: re-encoded to HEVC at the measured target"

// The one thing origin changes about a plan: the space sweep of plan.md 11
// re-encodes video the policy would otherwise copy.
func planFor(origin domain.JobOrigin, plan domain.Plan) (domain.Plan, bool) {
	if origin != domain.OriginSpaceSweep {
		return plan, true
	}

	return decide.ForceVideoEncode(plan, sweepReason)
}

// priorityFor is plan.md 19: lower runs first, normal is 100, and the I/O bound
// kinds go ahead of encodes so quick wins clear first.
func priorityFor(kind domain.Kind, prioritiseQuick bool) int {
	if !prioritiseQuick {
		return domain.PriorityNormal
	}

	switch kind {
	case domain.KindRemux, domain.KindAudioOnly:
		return domain.PriorityQuick
	case domain.KindFull:
		return domain.PriorityFull
	case domain.KindSkip:
		return domain.PriorityNormal
	default:
		return domain.PriorityNormal
	}
}
