package api

import (
	"context"
	"fmt"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// GetHealth is liveness. It answers as long as the process is serving; anything
// that needs a dependency belongs in GetReady.
func (s *Server) GetHealth(_ context.Context, _ gen.GetHealthRequestObject) (gen.GetHealthResponseObject, error) {
	return gen.GetHealth200JSONResponse{Status: gen.HealthStatusOk}, nil
}

// GetReady is readiness. The database is the only hard dependency: Plex and the
// *arrs are optional and their absence is a configuration state, not an outage.
func (s *Server) GetReady(ctx context.Context, _ gen.GetReadyRequestObject) (gen.GetReadyResponseObject, error) {
	check := gen.ReadinessCheck{Name: "database", Ok: true}

	if s.db == nil {
		check.Ok = false
		check.Message = ptrOf("no database handle wired")
	} else if err := s.db.PingContext(ctx); err != nil {
		check.Ok = false
		check.Message = ptrOf(err.Error())
	}

	body := gen.Readiness{Checks: []gen.ReadinessCheck{check}, Ready: check.Ok}

	if !body.Ready {
		return gen.GetReady503JSONResponse(body), nil
	}

	return gen.GetReady200JSONResponse(body), nil
}

// GetVersion is the build identity, including the policy hash: changing it
// makes every tagged file eligible again (plan.md 12).
func (s *Server) GetVersion(_ context.Context, _ gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	return gen.GetVersion200JSONResponse{
		BuiltAt:       s.build.BuiltAt,
		Commit:        s.build.Commit,
		FfmpegVersion: strPtr(s.ffmpeg),
		GoVersion:     s.build.GoVersion,
		PolicyHash:    ptrOf(decide.PolicyHash()),
		Version:       s.build.Version,
	}, nil
}

// GetStats is the library and throughput totals of plan.md 18.1.
func (s *Server) GetStats(ctx context.Context, _ gen.GetStatsRequestObject) (gen.GetStatsResponseObject, error) {
	out, err := s.stats(ctx)
	if err != nil {
		return gen.GetStatsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetStats200JSONResponse(out), nil
}

func (s *Server) stats(ctx context.Context) (gen.Stats, error) {
	totals, err := s.store.Stats(ctx)
	if err != nil {
		return gen.Stats{}, fmt.Errorf("read throughput stats: %w", err)
	}

	byStatus, err := s.store.CountMediaByStatus(ctx)
	if err != nil {
		return gen.Stats{}, fmt.Errorf("count media by status: %w", err)
	}

	byJobState, err := s.store.CountJobsByState(ctx)
	if err != nil {
		return gen.Stats{}, fmt.Errorf("count jobs by state: %w", err)
	}

	filesTotal := 0
	for _, n := range byStatus {
		filesTotal += n
	}

	out := gen.Stats{
		BytesIn:       totals.BytesIn,
		BytesOut:      totals.BytesOut,
		BytesSaved:    totals.BytesSaved,
		EncodeSeconds: totals.EncodeSeconds,
		FilesDone:     byStatus[domain.MediaDone],
		FilesFailed:   byStatus[domain.MediaFailed],
		FilesIgnored:  byStatus[domain.MediaIgnored],
		FilesMissing:  byStatus[domain.MediaMissing],
		FilesPending: byStatus[domain.MediaNew] + byStatus[domain.MediaAnalyzed] +
			byStatus[domain.MediaQueued] + byStatus[domain.MediaProcessing],
		FilesSkipped: byStatus[domain.MediaSkipped],
		FilesTotal:   filesTotal,
		JobsDone:     byJobState[domain.JobDone],
		JobsFailed:   byJobState[domain.JobFailed],
	}

	if totals.BytesIn > 0 {
		out.AvgSavingPct = ptrOf(float64(totals.BytesSaved) / float64(totals.BytesIn) * 100)
	}

	return out, nil
}

// ListEvents is the log view of plan.md 18.5: a cursor read, ascending by id,
// so the UI appends rather than re-renders.
func (s *Server) ListEvents(ctx context.Context, req gen.ListEventsRequestObject) (gen.ListEventsResponseObject, error) {
	limit := DefaultEventLimit
	if req.Params.Limit != nil && *req.Params.Limit > 0 {
		limit = min(*req.Params.Limit, MaxEventLimit)
	}

	filter := storeEventFilter(req.Params, limit)

	// One row over the limit is what tells has_more from "exactly a full page".
	filter.Limit = limit + 1

	rows, err := s.store.ListEvents(ctx, filter)
	if err != nil {
		return gen.ListEventsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]gen.Event, 0, len(rows))
	for _, e := range rows {
		items = append(items, event(e))
	}

	next := int64(0)
	if req.Params.SinceId != nil {
		next = *req.Params.SinceId
	}

	if len(items) > 0 {
		next = items[len(items)-1].Id
	}

	return gen.ListEvents200JSONResponse{HasMore: hasMore, Items: items, NextSinceId: next}, nil
}

// atLeast expands the "minimum level, inclusive" parameter into the explicit
// list the store filters on, because levels are stored as text and text has no
// ordering that means anything.
func atLeast(level gen.EventLevel) []string {
	order := []gen.EventLevel{gen.EventLevelDebug, gen.EventLevelInfo, gen.EventLevelWarn, gen.EventLevelError}

	for i, l := range order {
		if l == level {
			out := make([]string, 0, len(order)-i)
			for _, rest := range order[i:] {
				out = append(out, string(rest))
			}

			return out
		}
	}

	return nil
}

// GetPolicy renders the hard-coded policy for read-only display (plan.md 18.4).
func (s *Server) GetPolicy(_ context.Context, _ gen.GetPolicyRequestObject) (gen.GetPolicyResponseObject, error) {
	return gen.GetPolicy200JSONResponse(policy()), nil
}

func storeEventFilter(p gen.ListEventsParams, limit int) store.EventFilter {
	f := store.EventFilter{Limit: limit}

	if p.Level != nil {
		f.Level = atLeast(*p.Level)
	}

	if p.Category != nil && *p.Category != "" {
		f.Category = []string{*p.Category}
	}

	if p.SinceId != nil {
		f.SinceID = *p.SinceId
	}

	return f
}
