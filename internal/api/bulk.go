package api

import (
	"context"
	"strings"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// Every bulk operation is dry-run first (plan.md 19). A preview reports the
// count and the plan-kind breakdown so the confirmation can say exactly what it
// is about to do, and promotion is irreversible (15.5), so nothing here queues
// anything without confirm.

// RecheckAllMedia re-probes every done file and re-plans it against the current
// policy. confirm false examines everything and queues nothing.
func (s *Server) RecheckAllMedia(
	ctx context.Context, req gen.RecheckAllMediaRequestObject,
) (gen.RecheckAllMediaResponseObject, error) {
	confirm := req.Body != nil && req.Body.Confirm

	res, err := s.queue.RecheckAll(ctx, confirm)
	if err != nil {
		return gen.RecheckAllMediadefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.RecheckAllMedia200JSONResponse(recheckResult(res)), nil
}

// RecheckSelectedMedia is the same operation restricted to a selection or a
// filter. An empty body selects nothing rather than everything, so a mis-sent
// request cannot queue the library.
func (s *Server) RecheckSelectedMedia(
	ctx context.Context, req gen.RecheckSelectedMediaRequestObject,
) (gen.RecheckSelectedMediaResponseObject, error) {
	if req.Body == nil {
		return gen.RecheckSelectedMedia200JSONResponse(recheckResult(job.RecheckResult{
			DryRun: true, Irreversible: true,
		})), nil
	}

	request, err := recheckRequest(*req.Body)
	if err != nil {
		return gen.RecheckSelectedMediadefaultJSONResponse(s.fail(ctx, err)), nil
	}

	res, err := s.queue.Recheck(ctx, request)
	if err != nil {
		return gen.RecheckSelectedMediadefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.RecheckSelectedMedia200JSONResponse(recheckResult(res)), nil
}

// recheckRequest turns the body into a selection. ids and filter are mutually
// exclusive: accepting both would make it ambiguous whether the filter narrows
// the ids or adds to them.
func recheckRequest(in gen.RecheckSelectedRequest) (job.Recheck, error) {
	out := job.Recheck{Confirm: in.Confirm}

	hasIDs := in.Ids != nil && len(*in.Ids) > 0
	if hasIDs && in.Filter != nil {
		return job.Recheck{}, badRequest("send either ids or filter, not both")
	}

	if hasIDs {
		if len(*in.Ids) > MaxSelectionSize {
			return job.Recheck{}, badRequest(
				"at most %d ids; use filter for a larger selection", MaxSelectionSize)
		}

		out.IDs = *in.Ids

		return out, nil
	}

	if in.Filter != nil {
		filter := mediaFilter(*in.Filter)
		out.Filter = &filter
	}

	return out, nil
}

func mediaFilter(in gen.MediaFilter) store.MediaFilter {
	out := store.MediaFilter{
		Query:          strings.TrimSpace(deref(in.Q)),
		ArrInstanceID:  in.ArrInstanceId,
		IncludeIgnored: false,
	}

	if in.Status != nil {
		out.Status = []domain.MediaStatus{domain.MediaStatus(*in.Status)}
	}

	if in.PlanKind != nil {
		out.PlanKind = []domain.Kind{domain.Kind(*in.PlanKind)}
	}

	if in.VideoCodec != nil && *in.VideoCodec != "" {
		out.VideoCodec = []string{*in.VideoCodec}
	}

	if in.Provenance != nil {
		out.Provenance = []domain.Provenance{domain.Provenance(*in.Provenance)}
	}

	return out
}

func recheckResult(r job.RecheckResult) gen.RecheckResult {
	out := gen.RecheckResult{
		ByPlanKind:   breakdown(r.ByPlanKind),
		Count:        r.Count,
		DryRun:       r.DryRun,
		Examined:     r.Examined,
		QueuedJobIds: nonNilInt64s(r.QueuedJobIDs),
	}

	if len(r.MediaFileIDs) > 0 {
		out.MediaFileIds = &r.MediaFileIDs
	}

	return out
}

// PreviewSpaceSweep is the dry run of plan.md 11. It queues nothing.
func (s *Server) PreviewSpaceSweep(
	ctx context.Context, _ gen.PreviewSpaceSweepRequestObject,
) (gen.PreviewSpaceSweepResponseObject, error) {
	res, err := s.queue.SpaceSweepPreview(ctx)
	if err != nil {
		return gen.PreviewSpaceSweepdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.PreviewSpaceSweep200JSONResponse(sweepPreview(res)), nil
}

// RunSpaceSweep queues the sweep. confirm must be true; false is rejected with
// 400, because every file the sweep touches is replaced and there is no undo.
func (s *Server) RunSpaceSweep(
	ctx context.Context, req gen.RunSpaceSweepRequestObject,
) (gen.RunSpaceSweepResponseObject, error) {
	if req.Body == nil || !req.Body.Confirm {
		return gen.RunSpaceSweepdefaultJSONResponse(s.fail(ctx, badRequest(
			"the space reclaim sweep replaces every file it touches and cannot be undone; "+
				"send confirm true to run it"))), nil
	}

	var ids []int64
	if req.Body.MediaFileIds != nil {
		ids = *req.Body.MediaFileIds
	}

	if len(ids) > MaxSelectionSize {
		return gen.RunSpaceSweepdefaultJSONResponse(s.fail(ctx, badRequest(
			"at most %d media_file_ids", MaxSelectionSize))), nil
	}

	res, err := s.queue.SpaceSweepRun(ctx, ids, true)
	if err != nil {
		return gen.RunSpaceSweepdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.RunSpaceSweep200JSONResponse{
		ByPlanKind:           breakdown(res.ByPlanKind),
		Count:                res.Count,
		ProjectedSavingBytes: res.ProjectedSavingBytes,
		QueuedJobIds:         nonNilInt64s(res.QueuedJobIDs),
	}, nil
}

func sweepPreview(r job.SpaceSweepPreview) gen.SpaceSweepPreview {
	candidates := make([]gen.SpaceSweepCandidate, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		candidates = append(candidates, gen.SpaceSweepCandidate{
			CurrentBytes:            c.CurrentBytes,
			CurrentVideoBitrateKbps: intPtr(c.CurrentVideoBitrateKbps),
			Filename:                c.Filename,
			MediaFileId:             c.MediaFileID,
			Path:                    c.Path,
			ProjectedBytes:          c.ProjectedBytes,
			ProjectedSavingBytes:    c.ProjectedSavingBytes,
			ProjectedSavingPct:      c.ProjectedSavingPct,
			TargetVideoBitrateKbps:  intPtr(c.TargetVideoBitrateKbps),
			VideoCodec:              strPtr(c.VideoCodec),
		})
	}

	return gen.SpaceSweepPreview{
		ByPlanKind:           breakdown(r.ByPlanKind),
		Candidates:           candidates,
		Count:                r.Count,
		CurrentBytes:         r.CurrentBytes,
		Examined:             r.Examined,
		Irreversible:         true,
		ProjectedBytes:       r.ProjectedBytes,
		ProjectedSavingBytes: r.ProjectedSavingBytes,
		ProjectedSavingPct:   r.ProjectedSavingPct,
	}
}

func breakdown(b job.PlanKindBreakdown) gen.PlanKindBreakdown {
	return gen.PlanKindBreakdown{
		AudioOnly: b.AudioOnly,
		Full:      b.Full,
		Remux:     b.Remux,
		Skip:      b.Skip,
	}
}

func nonNilInt64s(in []int64) []int64 {
	if in == nil {
		return []int64{}
	}

	return in
}
