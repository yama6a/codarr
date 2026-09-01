package job_test

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/yama6a/codarr/internal/job"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// fakeStore is an in-memory stand-in for the parts of the store the queue uses.
// It is hand-written rather than generated because half these tests are about
// what the queue does across several writes, and a mock that only records calls
// cannot answer that.
type fakeStore struct {
	mu sync.Mutex

	jobs       map[int64]*domain.Job
	media      map[int64]*domain.MediaFile
	throughput map[string]domain.ThroughputStat
	roots      []domain.Root
	settings   domain.Settings

	nextJobID int64
	now       func() time.Time

	calls      []string
	progress   []progressWrite
	promotions []store.PromotionUpdate
	executions []store.ExecutionUpdate
	failures   []failedJob
}

type progressWrite struct {
	JobID     int64
	Pct       float64
	Speed     float64
	FPS       float64
	Estimated int
}

type failedJob struct {
	JobID   int64
	Code    domain.FailureCode
	Message string
	Stderr  string
}

var _ job.Store = (*fakeStore)(nil)

func newFakeStore(now func() time.Time) *fakeStore {
	return &fakeStore{
		jobs:       map[int64]*domain.Job{},
		media:      map[int64]*domain.MediaFile{},
		throughput: map[string]domain.ThroughputStat{},
		nextJobID:  1,
		now:        now,
	}
}

func (f *fakeStore) record(op string) { f.calls = append(f.calls, op) }

func (f *fakeStore) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func (f *fakeStore) job(id int64) (domain.Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return domain.Job{}, false
	}

	return *j, true
}

func (f *fakeStore) putJob(j domain.Job) domain.Job {
	f.mu.Lock()
	defer f.mu.Unlock()

	if j.ID == 0 {
		j.ID = f.nextJobID
		f.nextJobID++
	}

	if j.QueuedAt.IsZero() {
		j.QueuedAt = f.now()
	}

	stored := j
	f.jobs[j.ID] = &stored

	return stored
}

func (f *fakeStore) putMedia(m domain.MediaFile) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored := m
	f.media[m.ID] = &stored
}

func (f *fakeStore) EnqueueJob(_ context.Context, j domain.Job) (domain.Job, bool, error) {
	f.mu.Lock()

	for _, existing := range f.jobs {
		if existing.MediaFileID == j.MediaFileID && slices.Contains(domain.ActiveJobStates(), existing.State) {
			f.record("EnqueueJob:duplicate")
			f.mu.Unlock()

			return domain.Job{}, false, nil
		}
	}

	f.record("EnqueueJob")
	f.mu.Unlock()

	j.State = domain.JobQueued

	created := f.putJob(j)
	f.setMediaStatus(created.MediaFileID, domain.MediaQueued)

	return created, true, nil
}

func (f *fakeStore) GetJob(_ context.Context, id int64) (domain.Job, error) {
	j, ok := f.job(id)
	if !ok {
		return domain.Job{}, store.ErrNotFound
	}

	return j, nil
}

func (f *fakeStore) ActiveJobForMedia(_ context.Context, mediaFileID int64) (domain.Job, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, j := range f.jobs {
		if j.MediaFileID == mediaFileID && slices.Contains(domain.ActiveJobStates(), j.State) {
			return *j, true, nil
		}
	}

	return domain.Job{}, false, nil
}

func (f *fakeStore) ClaimNextJob(_ context.Context) (domain.Job, bool, error) {
	f.mu.Lock()

	var pick *domain.Job

	for _, j := range f.jobs {
		if j.State != domain.JobQueued {
			continue
		}

		if pick == nil || j.Priority < pick.Priority || (j.Priority == pick.Priority && j.ID < pick.ID) {
			pick = j
		}
	}

	if pick == nil {
		f.mu.Unlock()

		return domain.Job{}, false, nil
	}

	now := f.now()
	pick.State = domain.JobRunning
	pick.StartedAt = &now
	f.record("ClaimNextJob")

	claimed := *pick
	f.mu.Unlock()

	f.setMediaStatus(claimed.MediaFileID, domain.MediaProcessing)

	return claimed, true, nil
}

func (f *fakeStore) SetJobState(_ context.Context, id int64, state domain.JobState) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	j.State = state
	f.record("SetJobState:" + string(state))

	return nil
}

func (f *fakeStore) SetJobBlockedBy(_ context.Context, id int64, blockedBy string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	j.BlockedBy = blockedBy

	return nil
}

func (f *fakeStore) UpdateJobExecution(_ context.Context, u store.ExecutionUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[u.JobID]
	if !ok {
		return store.ErrNotFound
	}

	j.StagingPath = u.StagingPath
	j.UsedTempDir = u.UsedTempDir
	j.FfmpegArgv = u.FfmpegArgv
	j.EncoderUsed = u.EncoderUsed
	j.DecodePath = u.DecodePath
	j.FellBack = u.FellBack
	j.FallbackReason = u.FallbackReason
	j.SourceSize = u.SourceSize
	j.EstimatedSeconds = u.EstimatedSeconds
	j.FinalOutTimeUS = u.FinalOutTimeUS

	f.executions = append(f.executions, u)
	f.record("UpdateJobExecution")

	return nil
}

func (f *fakeStore) UpdateJobProgress(_ context.Context, id int64, pct, speed, fps float64, estimatedSeconds int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	j.ProgressPct = pct
	j.ProgressSpeed = speed
	j.ProgressFPS = fps
	f.progress = append(f.progress, progressWrite{JobID: id, Pct: pct, Speed: speed, FPS: fps, Estimated: estimatedSeconds})

	return nil
}

func (f *fakeStore) CountJobsByState(context.Context) (map[domain.JobState]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.record("CountJobsByState")

	counts := map[domain.JobState]int{}
	for _, j := range f.jobs {
		counts[j.State]++
	}

	return counts, nil
}

func (f *fakeStore) UpdateJobTransform(_ context.Context, id int64, t domain.TransformRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	j.Transform = t
	f.record("UpdateJobTransform")

	return nil
}

// FailJob refuses a failure without both halves, exactly as the real store
// does, so a test that produces a bare failure fails loudly (19.1).
func (f *fakeStore) FailJob(_ context.Context, id int64, code domain.FailureCode, message, stderrTail string) error {
	if code == "" || message == "" {
		return store.ErrInvalidFailure
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	now := f.now()
	j.State = domain.JobFailed
	j.FailureCode = code
	j.FailureMessage = message
	j.StderrTail = stderrTail
	j.FinishedAt = &now

	f.failures = append(f.failures, failedJob{JobID: id, Code: code, Message: message, Stderr: stderrTail})
	f.record("FailJob")

	if m, ok := f.media[j.MediaFileID]; ok {
		m.Status = domain.MediaFailed
		m.LastError = message
	}

	return nil
}

func (f *fakeStore) CancelJob(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.ErrNotFound
	}

	if j.State == domain.JobDone || j.State == domain.JobCancelled || j.State == domain.JobFailed {
		return store.ErrNotFound
	}

	now := f.now()
	j.State = domain.JobCancelled
	j.FinishedAt = &now
	f.record("CancelJob")

	return nil
}

func (f *fakeStore) RestartJob(_ context.Context, id int64) (domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return domain.Job{}, store.ErrNotFound
	}

	j.State = domain.JobQueued
	j.Priority = f.frontPriority(j.Priority)
	j.Attempt = 0
	j.FailureCode = ""
	j.FailureMessage = ""
	j.StagingPath = ""
	f.record("RestartJob")

	return *j, nil
}

// frontPriority is the store's min(queued) - 1 rule (19).
func (f *fakeStore) frontPriority(own int) int {
	lowest := own

	for _, j := range f.jobs {
		if j.State == domain.JobQueued && j.Priority < lowest {
			lowest = j.Priority
		}
	}

	return lowest - 1
}

func (f *fakeStore) RequeueInterruptedJob(_ context.Context, id int64) (store.SweepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return store.SweepResult{}, store.ErrNotFound
	}

	return f.requeueOrFail(j), nil
}

func (f *fakeStore) SweepInterruptedJobs(_ context.Context) ([]store.SweepResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ids := make([]int64, 0, len(f.jobs))

	for id, j := range f.jobs {
		if slices.Contains(domain.InFlightJobStates(), j.State) {
			ids = append(ids, id)
		}
	}

	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })

	out := make([]store.SweepResult, 0, len(ids))

	for _, id := range ids {
		j := f.jobs[id]

		// plan.md 19.2: promoting and awaiting_stream_end need the filesystem,
		// so the store never decides them.
		if j.State == domain.JobPromoting || j.State == domain.JobAwaitingStreamEnd {
			out = append(out, store.SweepResult{
				JobID:       j.ID,
				MediaFileID: j.MediaFileID,
				FoundState:  j.State,
				Action:      store.SweepNeedsCheck,
				Attempt:     j.Attempt,
				StagingPath: j.StagingPath,
			})

			continue
		}

		out = append(out, f.requeueOrFail(j))
	}

	return out, nil
}

func (f *fakeStore) requeueOrFail(j *domain.Job) store.SweepResult {
	out := store.SweepResult{
		JobID:       j.ID,
		MediaFileID: j.MediaFileID,
		FoundState:  j.State,
		Attempt:     j.Attempt,
		StagingPath: j.StagingPath,
	}

	if j.Attempt >= domain.MaxAutoAttempts {
		now := f.now()
		j.State = domain.JobFailed
		j.FailureCode = domain.FailInterrupted
		j.FailureMessage = "interrupted repeatedly; the automatic restart cap was reached"
		j.FinishedAt = &now
		out.Action = store.SweepFailed

		return out
	}

	j.State = domain.JobQueued
	j.Attempt++
	j.Priority = f.frontPriority(j.Priority)
	j.StagingPath = ""
	out.Action = store.SweepRequeued
	out.Attempt = j.Attempt

	return out
}

func (f *fakeStore) GetMediaFile(_ context.Context, id int64) (domain.MediaFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, ok := f.media[id]
	if !ok {
		return domain.MediaFile{}, store.ErrNotFound
	}

	return *m, nil
}

func (f *fakeStore) ListMediaFiles(_ context.Context, filter store.MediaFilter) ([]domain.MediaFile, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ids := make([]int64, 0, len(f.media))
	for id := range f.media {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })

	matched := make([]domain.MediaFile, 0, len(ids))

	for _, id := range ids {
		if m := f.media[id]; matchesFilter(*m, filter) {
			matched = append(matched, *m)
		}
	}

	return page(matched, filter), len(matched), nil
}

func matchesFilter(m domain.MediaFile, filter store.MediaFilter) bool {
	if len(filter.Status) > 0 && !slices.Contains(filter.Status, m.Status) {
		return false
	}

	if len(filter.VideoCodec) > 0 && !slices.Contains(filter.VideoCodec, m.VideoCodec) {
		return false
	}

	return filter.IncludeIgnored || !m.Ignored
}

func page(in []domain.MediaFile, filter store.MediaFilter) []domain.MediaFile {
	if filter.Offset >= len(in) {
		return nil
	}

	out := in[filter.Offset:]
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}

	return slices.Clone(out)
}

func (f *fakeStore) SetMediaStatus(_ context.Context, id int64, status domain.MediaStatus, lastError string) error {
	f.setMediaStatus(id, status)

	f.mu.Lock()
	defer f.mu.Unlock()

	if m, ok := f.media[id]; ok {
		m.LastError = lastError
	}

	return nil
}

func (f *fakeStore) setMediaStatus(id int64, status domain.MediaStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if m, ok := f.media[id]; ok {
		m.Status = status
	}
}

func (f *fakeStore) RecordPromotion(_ context.Context, u store.PromotionUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[u.JobID]
	if !ok {
		return store.ErrNotFound
	}

	j.State = domain.JobDone
	j.OutputFingerprint = u.OutputFingerprint
	j.OutputSize = u.OutputSize
	j.ActualSeconds = u.ActualSeconds
	j.Transform = u.Transform
	j.FinishedAt = &u.PromotedAt

	if m, ok := f.media[u.MediaFileID]; ok {
		m.Status = domain.MediaDone
		m.CodarrOutputFingerprint = u.OutputFingerprint
		m.CodarrOutputSize = u.OutputSize
		m.CodarrPolicyHash = u.PolicyHash
		m.CodarrTagged = true
		m.Provenance = domain.ProvenanceCodarrOutput
	}

	f.promotions = append(f.promotions, u)
	f.record("RecordPromotion")

	return nil
}

func (f *fakeStore) ListRoots(_ context.Context) ([]domain.Root, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.roots), nil
}

func (f *fakeStore) GetSettings(_ context.Context) (domain.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.settings, nil
}

func (f *fakeStore) UpdateSettings(_ context.Context, s domain.Settings) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.settings = s
	f.record("UpdateSettings")

	return nil
}

func throughputKey(kind domain.Kind, encoder, resolution string) string {
	return string(kind) + "|" + encoder + "|" + resolution
}

func (f *fakeStore) GetThroughputStat(
	_ context.Context, kind domain.Kind, encoder, resolution string,
) (domain.ThroughputStat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	st, ok := f.throughput[throughputKey(kind, encoder, resolution)]
	if !ok {
		return domain.ThroughputStat{}, store.ErrNotFound
	}

	return st, nil
}

func (f *fakeStore) UpsertThroughputStat(_ context.Context, s domain.ThroughputStat) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.throughput[throughputKey(s.Kind, s.Encoder, s.Resolution)] = s
	f.record("UpsertThroughputStat")

	return nil
}
