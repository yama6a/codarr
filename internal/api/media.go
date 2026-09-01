package api

import (
	"context"
	"fmt"
	"strings"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// MaxSelectionSize bounds an explicit id list. Beyond it the UI is expected to
// send a filter instead, which is exactly why RecheckSelectedRequest takes one.
const MaxSelectionSize = 5000

// ListMedia is the library table of plan.md 18.2: filtered, sorted and
// paginated server-side.
func (s *Server) ListMedia(ctx context.Context, req gen.ListMediaRequestObject) (gen.ListMediaResponseObject, error) {
	limit, offset, pageNo, pageSize := page(req.Params.Page, req.Params.PageSize)

	filter := store.MediaFilter{
		Query:          strings.TrimSpace(deref(req.Params.Q)),
		ArrInstanceID:  req.Params.ArrInstanceId,
		IncludeIgnored: true,
		Sort:           sortColumn(req.Params.Sort),
		Descending:     descending(req.Params.Sort),
		Limit:          limit,
		Offset:         offset,
	}

	if req.Params.Status != nil {
		filter.Status = []domain.MediaStatus{domain.MediaStatus(*req.Params.Status)}
	}

	if req.Params.PlanKind != nil {
		filter.PlanKind = []domain.Kind{domain.Kind(*req.Params.PlanKind)}
	}

	if req.Params.VideoCodec != nil && *req.Params.VideoCodec != "" {
		filter.VideoCodec = []string{*req.Params.VideoCodec}
	}

	if req.Params.Provenance != nil {
		filter.Provenance = []domain.Provenance{domain.Provenance(*req.Params.Provenance)}
	}

	rows, total, err := s.store.ListMediaFiles(ctx, filter)
	if err != nil {
		return gen.ListMediadefaultJSONResponse(s.fail(ctx, err)), nil
	}

	names, err := s.instanceNames(ctx)
	if err != nil {
		return gen.ListMediadefaultJSONResponse(s.fail(ctx, err)), nil
	}

	items := make([]gen.MediaListItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, mediaListItem(m, instanceName(names, m.ArrInstanceID)))
	}

	return gen.ListMedia200JSONResponse{Items: items, Page: pageNo, PageSize: pageSize, Total: total}, nil
}

// GetMediaFile is one file with its plan and probe output, which is what the
// detail modal renders (18.3).
func (s *Server) GetMediaFile(
	ctx context.Context, req gen.GetMediaFileRequestObject,
) (gen.GetMediaFileResponseObject, error) {
	out, err := s.mediaView(ctx, req.Id)
	if err != nil {
		return gen.GetMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetMediaFile200JSONResponse(out), nil
}

// AnalyzeMediaFile re-probes and re-plans one file against the current policy.
func (s *Server) AnalyzeMediaFile(
	ctx context.Context, req gen.AnalyzeMediaFileRequestObject,
) (gen.AnalyzeMediaFileResponseObject, error) {
	media, err := s.store.GetMediaFile(ctx, req.Id)
	if err != nil {
		return gen.AnalyzeMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if _, err := s.underRoots(ctx, media.Path); err != nil {
		return gen.AnalyzeMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if _, err := s.analyzer.Analyze(ctx, media.Path, domain.OriginManual); err != nil {
		return gen.AnalyzeMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.mediaView(ctx, req.Id)
	if err != nil {
		return gen.AnalyzeMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.AnalyzeMediaFile200JSONResponse(out), nil
}

// QueueMediaFile enqueues one file. The queue decides whether there is work to
// do; a file that plans to skip is a no-op with a reason, not an error.
func (s *Server) QueueMediaFile(
	ctx context.Context, req gen.QueueMediaFileRequestObject,
) (gen.QueueMediaFileResponseObject, error) {
	res, err := s.queue.Enqueue(ctx, req.Id, domain.OriginManual)
	if err != nil {
		return gen.QueueMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.QueueMediaFile200JSONResponse{
		Enqueued:    res.Enqueued,
		JobId:       res.JobID,
		MediaFileId: res.MediaFileID,
		PlanKind:    planKindPtr(res.PlanKind),
		Reason:      res.Reason,
	}, nil
}

// IgnoreMediaFile adds the file to the ignore list.
func (s *Server) IgnoreMediaFile(
	ctx context.Context, req gen.IgnoreMediaFileRequestObject,
) (gen.IgnoreMediaFileResponseObject, error) {
	out, err := s.setIgnored(ctx, req.Id, true)
	if err != nil {
		return gen.IgnoreMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.IgnoreMediaFile200JSONResponse(out), nil
}

// UnignoreMediaFile removes the file from the ignore list.
func (s *Server) UnignoreMediaFile(
	ctx context.Context, req gen.UnignoreMediaFileRequestObject,
) (gen.UnignoreMediaFileResponseObject, error) {
	out, err := s.setIgnored(ctx, req.Id, false)
	if err != nil {
		return gen.UnignoreMediaFiledefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.UnignoreMediaFile200JSONResponse(out), nil
}

func (s *Server) setIgnored(ctx context.Context, id int64, ignored bool) (gen.MediaDetail, error) {
	if err := s.store.SetMediaIgnored(ctx, id, ignored); err != nil {
		return gen.MediaDetail{}, fmt.Errorf("set ignored on media file %d: %w", id, err)
	}

	return s.mediaView(ctx, id)
}

func (s *Server) mediaView(ctx context.Context, id int64) (gen.MediaDetail, error) {
	media, err := s.store.GetMediaFile(ctx, id)
	if err != nil {
		return gen.MediaDetail{}, fmt.Errorf("get media file %d: %w", id, err)
	}

	names, err := s.instanceNames(ctx)
	if err != nil {
		return gen.MediaDetail{}, err
	}

	latest, err := s.latestJobID(ctx, id)
	if err != nil {
		return gen.MediaDetail{}, err
	}

	return mediaDetail(media, instanceName(names, media.ArrInstanceID), latest), nil
}

func (s *Server) latestJobID(ctx context.Context, mediaFileID int64) (*int64, error) {
	jobs, _, err := s.store.ListJobs(ctx, store.JobFilter{MediaFileID: &mediaFileID, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("list jobs for media file %d: %w", mediaFileID, err)
	}

	if len(jobs) == 0 {
		return nil, nil //nolint:nilnil // no job yet is not an error
	}

	return &jobs[0].ID, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// sortColumn maps the spec's sort keys onto the store's whitelist. Anything
// unrecognised falls back to path rather than reaching the query.
func sortColumn(sort *gen.MediaSort) store.MediaSort {
	if sort == nil {
		return store.SortPath
	}

	switch strings.TrimPrefix(string(*sort), "-") {
	case "plan_kind":
		return store.SortPlanKind
	case "size_bytes":
		return store.SortSize
	case "status":
		return store.SortStatus
	case "updated_at":
		return store.SortUpdatedAt
	case "video_bitrate":
		return store.SortBitrate
	case "provenance":
		return store.SortProvenance
	default:
		return store.SortPath
	}
}

func descending(sort *gen.MediaSort) bool {
	return sort != nil && strings.HasPrefix(string(*sort), "-")
}
