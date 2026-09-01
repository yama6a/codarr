package api

import (
	"context"
	"fmt"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// DashboardListSize bounds each list the dashboard shows. The queue is ordered,
// so the first page is the part anyone looks at.
const DashboardListSize = 25

// ListJobs is the job history, newest first.
func (s *Server) ListJobs(ctx context.Context, req gen.ListJobsRequestObject) (gen.ListJobsResponseObject, error) {
	limit, offset, pageNo, pageSize := page(req.Params.Page, req.Params.PageSize)

	filter := store.JobFilter{MediaFileID: req.Params.MediaFileId, Limit: limit, Offset: offset}
	if req.Params.State != nil {
		filter.State = []domain.JobState{domain.JobState(*req.Params.State)}
	}

	jobs, total, err := s.store.ListJobs(ctx, filter)
	if err != nil {
		return gen.ListJobsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	items, err := s.jobSummaries(ctx, jobs)
	if err != nil {
		return gen.ListJobsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.ListJobs200JSONResponse{Items: items, Page: pageNo, PageSize: pageSize, Total: total}, nil
}

// GetJob is one job including its transform record, which is what the detail
// modal renders for queued and completed items alike (plan.md 18.3).
func (s *Server) GetJob(ctx context.Context, req gen.GetJobRequestObject) (gen.GetJobResponseObject, error) {
	out, err := s.jobView(ctx, req.Id)
	if err != nil {
		return gen.GetJobdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetJob200JSONResponse(out), nil
}

// CancelJob cancels a queued or running job. The staging file is cleaned by the
// queue; the job stays visible at the top of the list.
func (s *Server) CancelJob(ctx context.Context, req gen.CancelJobRequestObject) (gen.CancelJobResponseObject, error) {
	if err := s.queue.Cancel(ctx, req.Id); err != nil {
		return gen.CancelJobdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.jobView(ctx, req.Id)
	if err != nil {
		return gen.CancelJobdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if s.metrics != nil {
		s.metrics.JobObserved(domain.JobCancelled, domain.Kind(out.Kind), domain.JobOrigin(out.Origin))
	}

	return gen.CancelJob200JSONResponse(out), nil
}

// RestartJob re-queues a cancelled or failed job ahead of everything already
// queued, and resets the interruption counter (plan.md 19.2).
func (s *Server) RestartJob(
	ctx context.Context, req gen.RestartJobRequestObject,
) (gen.RestartJobResponseObject, error) {
	restarted, err := s.queue.Restart(ctx, req.Id)
	if err != nil {
		return gen.RestartJobdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if s.metrics != nil {
		s.metrics.JobObserved(domain.JobQueued, restarted.Kind, restarted.Origin)
	}

	out, err := s.jobView(ctx, req.Id)
	if err != nil {
		return gen.RestartJobdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.RestartJob200JSONResponse(out), nil
}

// GetQueue is the queue as the worker sees it.
func (s *Server) GetQueue(ctx context.Context, _ gen.GetQueueRequestObject) (gen.GetQueueResponseObject, error) {
	out, err := s.queueState(ctx)
	if err != nil {
		return gen.GetQueuedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetQueue200JSONResponse(out), nil
}

// PauseQueue stops new jobs starting. A running job continues (plan.md 19).
func (s *Server) PauseQueue(ctx context.Context, _ gen.PauseQueueRequestObject) (gen.PauseQueueResponseObject, error) {
	if err := s.queue.Pause(ctx); err != nil {
		return gen.PauseQueuedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.queueState(ctx)
	if err != nil {
		return gen.PauseQueuedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.PauseQueue200JSONResponse(out), nil
}

// ResumeQueue starts consuming the queue again.
func (s *Server) ResumeQueue(
	ctx context.Context, _ gen.ResumeQueueRequestObject,
) (gen.ResumeQueueResponseObject, error) {
	if err := s.queue.Resume(ctx); err != nil {
		return gen.ResumeQueuedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.queueState(ctx)
	if err != nil {
		return gen.ResumeQueuedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.ResumeQueue200JSONResponse(out), nil
}

func (s *Server) queueState(ctx context.Context) (gen.QueueState, error) {
	paused, err := s.queue.Paused(ctx)
	if err != nil {
		return gen.QueueState{}, fmt.Errorf("read the queue pause flag: %w", err)
	}

	counts, err := s.store.CountJobsByState(ctx)
	if err != nil {
		return gen.QueueState{}, fmt.Errorf("count jobs by state: %w", err)
	}

	names := newMediaCache(s.store)

	queued, err := s.listState(ctx, names, DashboardListSize, domain.JobQueued)
	if err != nil {
		return gen.QueueState{}, err
	}

	awaiting, err := s.awaitingStreamEnd(ctx, names)
	if err != nil {
		return gen.QueueState{}, err
	}

	running, err := s.currentJob(ctx, names)
	if err != nil {
		return gen.QueueState{}, err
	}

	return gen.QueueState{
		AwaitingStreamEnd: awaiting,
		Depth:             counts[domain.JobQueued],
		Paused:            paused,
		Queued:            queued,
		Running:           running,
	}, nil
}

// currentJob is the one job in flight. There is exactly one worker, so at most
// one job is ever in a running state (plan.md 19).
func (s *Server) currentJob(ctx context.Context, cache *mediaCache) (*gen.JobSummary, error) {
	for _, state := range []domain.JobState{domain.JobRunning, domain.JobVerifying, domain.JobPromoting} {
		found, err := s.listState(ctx, cache, 1, state)
		if err != nil {
			return nil, err
		}

		if len(found) > 0 {
			return &found[0], nil
		}
	}

	return nil, nil //nolint:nilnil // an idle queue has no current job, which is not an error
}

func (s *Server) listState(
	ctx context.Context, cache *mediaCache, limit int, states ...domain.JobState,
) ([]gen.JobSummary, error) {
	jobs, _, err := s.store.ListJobs(ctx, store.JobFilter{State: states, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}

	return s.summariesWith(ctx, cache, jobs)
}

func (s *Server) awaitingStreamEnd(ctx context.Context, cache *mediaCache) ([]gen.AwaitingStreamEnd, error) {
	jobs, _, err := s.store.ListJobs(ctx, store.JobFilter{
		State: []domain.JobState{domain.JobAwaitingStreamEnd},
		Limit: DashboardListSize,
	})
	if err != nil {
		return nil, fmt.Errorf("list deferred jobs: %w", err)
	}

	out := make([]gen.AwaitingStreamEnd, 0, len(jobs))

	for _, j := range jobs {
		media, err := cache.get(ctx, j.MediaFileID)
		if err != nil {
			return nil, err
		}

		since := j.QueuedAt
		if j.StartedAt != nil {
			since = *j.StartedAt
		}

		out = append(out, gen.AwaitingStreamEnd{
			Filename:       filename(media.Path),
			JobId:          j.ID,
			MediaFileId:    j.MediaFileID,
			Path:           media.Path,
			SessionUser:    strPtr(j.BlockedBy),
			WaitingSeconds: int(s.clk.Now().Sub(since).Seconds()),
			WaitingSince:   since,
		})
	}

	return out, nil
}

func (s *Server) jobSummaries(ctx context.Context, jobs []domain.Job) ([]gen.JobSummary, error) {
	return s.summariesWith(ctx, newMediaCache(s.store), jobs)
}

func (s *Server) summariesWith(
	ctx context.Context, cache *mediaCache, jobs []domain.Job,
) ([]gen.JobSummary, error) {
	out := make([]gen.JobSummary, 0, len(jobs))

	for _, j := range jobs {
		media, err := cache.get(ctx, j.MediaFileID)
		if err != nil {
			return nil, err
		}

		out = append(out, jobSummary(j, media.Path))
	}

	return out, nil
}

func (s *Server) jobView(ctx context.Context, id int64) (gen.Job, error) {
	j, err := s.store.GetJob(ctx, id)
	if err != nil {
		return gen.Job{}, fmt.Errorf("get job %d: %w", id, err)
	}

	media, err := s.store.GetMediaFile(ctx, j.MediaFileID)
	if err != nil {
		return gen.Job{}, fmt.Errorf("get media file %d: %w", j.MediaFileID, err)
	}

	names, err := s.instanceNames(ctx)
	if err != nil {
		return gen.Job{}, err
	}

	return jobDetail(j, media, instanceName(names, media.ArrInstanceID)), nil
}

// mediaCache stops a list of jobs turning into one media query per row when
// several jobs point at the same file.
type mediaCache struct {
	store MediaStore
	rows  map[int64]domain.MediaFile
}

func newMediaCache(st MediaStore) *mediaCache {
	return &mediaCache{store: st, rows: map[int64]domain.MediaFile{}}
}

func (c *mediaCache) get(ctx context.Context, id int64) (domain.MediaFile, error) {
	if m, ok := c.rows[id]; ok {
		return m, nil
	}

	m, err := c.store.GetMediaFile(ctx, id)
	if err != nil {
		return domain.MediaFile{}, fmt.Errorf("get media file %d: %w", id, err)
	}

	c.rows[id] = m

	return m, nil
}

func jobSummary(j domain.Job, path string) gen.JobSummary {
	return gen.JobSummary{
		ActualSeconds:    intPtr(j.ActualSeconds),
		Attempt:          j.Attempt,
		BlockedBy:        strPtr(j.BlockedBy),
		DecodePath:       decodePathPtr(j.DecodePath),
		EncoderUsed:      encoderPtr(j.EncoderUsed),
		EstimatedSeconds: intPtr(j.EstimatedSeconds),
		FailureCode:      failureCodePtr(j.FailureCode),
		FailureMessage:   strPtr(j.FailureMessage),
		FallbackReason:   strPtr(j.FallbackReason),
		FellBack:         j.FellBack,
		FinishedAt:       j.FinishedAt,
		Id:               j.ID,
		Kind:             gen.PlanKind(j.Kind),
		MediaFileId:      j.MediaFileID,
		MediaFilename:    filename(path),
		MediaPath:        path,
		Origin:           gen.JobOrigin(j.Origin),
		OutputSize:       int64Ptr(j.OutputSize),
		Priority:         j.Priority,
		ProgressPct:      floatPtr(j.ProgressPct),
		ProgressSpeed:    floatPtr(j.ProgressSpeed),
		QueuedAt:         j.QueuedAt,
		SourceSize:       int64Ptr(j.SourceSize),
		StartedAt:        j.StartedAt,
		State:            gen.JobState(j.State),
	}
}

func jobDetail(j domain.Job, media domain.MediaFile, instance string) gen.Job {
	out := gen.Job{
		ActualSeconds:     intPtr(j.ActualSeconds),
		ArrInstanceId:     media.ArrInstanceID,
		ArrInstanceName:   strPtr(instance),
		Attempt:           j.Attempt,
		BlockedBy:         strPtr(j.BlockedBy),
		DecodePath:        decodePathPtr(j.DecodePath),
		EncoderUsed:       encoderPtr(j.EncoderUsed),
		EstimatedSeconds:  intPtr(j.EstimatedSeconds),
		FailureCode:       failureCodePtr(j.FailureCode),
		FailureMessage:    strPtr(j.FailureMessage),
		FallbackReason:    strPtr(j.FallbackReason),
		FellBack:          j.FellBack,
		FinishedAt:        j.FinishedAt,
		Id:                j.ID,
		Kind:              gen.PlanKind(j.Kind),
		MediaFileId:       j.MediaFileID,
		MediaFilename:     filename(media.Path),
		MediaPath:         media.Path,
		Origin:            gen.JobOrigin(j.Origin),
		OutputFingerprint: strPtr(j.OutputFingerprint),
		OutputFullHash:    strPtr(j.OutputFullHash),
		OutputSize:        int64Ptr(j.OutputSize),
		Priority:          j.Priority,
		ProbeResult:       strPtr(media.ProbeJSON),
		ProgressPct:       floatPtr(j.ProgressPct),
		ProgressSpeed:     floatPtr(j.ProgressSpeed),
		QueuedAt:          j.QueuedAt,
		SourceSize:        int64Ptr(j.SourceSize),
		StagingPath:       strPtr(j.StagingPath),
		StartedAt:         j.StartedAt,
		State:             gen.JobState(j.State),
		StderrTail:        strPtr(j.StderrTail),
		Transform:         transformRecord(j.Transform),
		UsedTempDir:       j.UsedTempDir,
	}

	if len(j.FfmpegArgv) > 0 {
		out.FfmpegArgv = &j.FfmpegArgv
	}

	return out
}

func decodePathPtr(d domain.DecodePath) *gen.DecodePath {
	if d == "" {
		return nil
	}

	return ptrOf(gen.DecodePath(d))
}

func encoderPtr(e domain.Encoder) *gen.Encoder {
	if e == "" {
		return nil
	}

	return ptrOf(gen.Encoder(e))
}

func failureCodePtr(c domain.FailureCode) *gen.FailureCode {
	if c == "" {
		return nil
	}

	return ptrOf(gen.FailureCode(c))
}
