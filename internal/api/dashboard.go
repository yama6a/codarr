package api

import (
	"context"
	"fmt"
	"time"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// CompatibilityTTL is how long the breakdown is reused: the per-reason counts read every
// non-skip plan, and the dashboard polls every 10 seconds (plan.md 18.6).
const CompatibilityTTL = time.Minute

const compatPageSize = 500

// GetDashboard is the one call the UI polls, built from indexed counts and short limited
// reads so the 10-second interval costs one request rather than six.
func (s *Server) GetDashboard(
	ctx context.Context, _ gen.GetDashboardRequestObject,
) (gen.GetDashboardResponseObject, error) {
	out, err := s.dashboard(ctx)
	if err != nil {
		return gen.GetDashboarddefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetDashboard200JSONResponse(out), nil
}

func (s *Server) dashboard(ctx context.Context) (gen.Dashboard, error) {
	queue, err := s.queueState(ctx)
	if err != nil {
		return gen.Dashboard{}, err
	}

	cache := newMediaCache(s.store)

	completions, err := s.listState(ctx, cache, DashboardListSize, domain.JobDone)
	if err != nil {
		return gen.Dashboard{}, err
	}

	failures, err := s.listState(ctx, cache, DashboardListSize, domain.JobFailed)
	if err != nil {
		return gen.Dashboard{}, err
	}

	totals, err := s.stats(ctx)
	if err != nil {
		return gen.Dashboard{}, err
	}

	compat, err := s.compatibility(ctx)
	if err != nil {
		return gen.Dashboard{}, err
	}

	return gen.Dashboard{
		AwaitingStreamEnd: queue.AwaitingStreamEnd,
		Compatibility:     compat,
		CurrentJob:        queue.Running,
		Failures:          failures,
		GeneratedAt:       s.clk.Now(),
		Queue:             queue.Queued,
		QueueDepth:        queue.Depth,
		QueuePaused:       queue.Paused,
		RecentCompletions: completions,
		Stats:             totals,
	}, nil
}

// compatibility is the number that tracks progress toward the primary goal
// (plan.md 18.1): how many files still force playback transcoding, and why.
func (s *Server) compatibility(ctx context.Context) (gen.CompatibilitySummary, error) {
	s.compat.mu.Lock()
	defer s.compat.mu.Unlock()

	if !s.compat.computed.IsZero() && s.clk.Now().Sub(s.compat.computed) < CompatibilityTTL {
		return s.compat.value, nil
	}

	byKind, err := s.store.CountMediaByPlanKind(ctx)
	if err != nil {
		return gen.CompatibilitySummary{}, fmt.Errorf("count media by plan kind: %w", err)
	}

	byStatus, err := s.store.CountMediaByStatus(ctx)
	if err != nil {
		return gen.CompatibilitySummary{}, fmt.Errorf("count media by status: %w", err)
	}

	analysed := 0
	for _, n := range byKind {
		analysed += n
	}

	total := 0
	for _, n := range byStatus {
		total += n
	}

	reasons, err := s.reasonBreakdown(ctx)
	if err != nil {
		return gen.CompatibilitySummary{}, err
	}

	out := gen.CompatibilitySummary{
		ByPlanKind:       planKindBreakdown(byKind),
		ByReason:         reasons,
		FilesAnalyzed:    analysed,
		FilesCompatible:  byKind[domain.KindSkip],
		FilesNeedingWork: analysed - byKind[domain.KindSkip],
		FilesUnanalyzed:  max(total-analysed, 0),
	}

	s.compat.value, s.compat.computed = out, s.clk.Now()

	return out, nil
}

// The counts overlap: one file can need three of them at once.
//
// Derived from the stored plan's stream decisions, because the kind cannot tell a
// subtitle conversion from an audio re-encode and the reason strings are prose.
func (s *Server) reasonBreakdown(ctx context.Context) (gen.CompatibilityReasons, error) {
	filter := store.MediaFilter{
		PlanKind: []domain.Kind{domain.KindRemux, domain.KindAudioOnly, domain.KindFull},
		Sort:     store.SortPath,
		Limit:    compatPageSize,
	}

	var out gen.CompatibilityReasons

	for {
		rows, total, err := s.store.ListMediaFiles(ctx, filter)
		if err != nil {
			return gen.CompatibilityReasons{}, fmt.Errorf("list media files: %w", err)
		}

		for _, m := range rows {
			classifyPlan(m.Plan, &out)
		}

		filter.Offset += len(rows)

		if len(rows) == 0 || filter.Offset >= total {
			return out, nil
		}
	}
}

func classifyPlan(p *domain.Plan, out *gen.CompatibilityReasons) {
	if p == nil {
		return
	}

	if p.SourceContainer != string(p.OutputContainer) {
		out.Container++
	}

	video, audio, subtitle := streamWork(p.Streams)

	// A level rewrite or a deinterlace is video work even though the stream
	// itself is copied, so the video count is either-or rather than additive.
	if video || p.LevelRewrite || p.Deinterlace {
		out.Video++
	}

	if audio {
		out.Audio++
	}

	if subtitle {
		out.Subtitles++
	}
}

func streamWork(streams []domain.StreamPlan) (video, audio, subtitle bool) {
	for _, s := range streams {
		switch s.Type {
		case domain.StreamVideo:
			video = video || s.Decision == domain.DecisionEncode
		case domain.StreamAudio:
			audio = audio || s.Decision == domain.DecisionEncode
		case domain.StreamSubtitle:
			subtitle = subtitle || s.Decision == domain.DecisionConvert || s.Decision == domain.DecisionDrop
		}
	}

	return video, audio, subtitle
}
