package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/ffprobe"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/promote"
)

// errUnknownSweepAction guards against the store growing a fourth outcome that
// nothing here handles.
var errUnknownSweepAction = errors.New("job: unknown interrupted-job sweep action")

// verdict is what an interrupted promotion turned out to have done. Promotion
// is a single rename(), so exactly one of the first two is true (19.2); the
// third exists because "neither" means the file on disk is not what either
// answer predicts, and guessing there would be the one guess that destroys
// something.
type verdict int

const (
	verdictUnknown verdict = iota
	verdictSourceIntact
	verdictPromoted
)

// Recover is the startup sweep of plan.md 19.2. There is one worker process, so
// every job left in a non-terminal in-flight state was interrupted. The store
// has already re-queued or failed the ones it can decide alone; the two that
// need the filesystem are decided here, and the orphan sweep of 15.2 runs last
// so it only removes what no job still claims.
func (s *Service) Recover(ctx context.Context) error {
	results, err := s.store.SweepInterruptedJobs(ctx)
	if err != nil {
		return fmt.Errorf("sweeping interrupted jobs: %w", err)
	}

	claimed := make([]string, 0, len(results))

	for _, r := range results {
		s.log.InfoContext(ctx, "interrupted job found",
			slog.Int64("job_id", r.JobID),
			slog.String("found_state", string(r.FoundState)),
			slog.String("action", string(r.Action)),
			slog.Int("attempt", r.Attempt))

		s.observeSweep(r.Action)

		keep, err := s.recoverOne(ctx, r)
		if err != nil {
			s.mx.error(errorRecovery)
			s.log.ErrorContext(ctx, "deciding what an interrupted job did failed",
				slog.Int64("job_id", r.JobID), slog.Any("error", err))

			continue
		}

		if keep != "" {
			claimed = append(claimed, keep)
		}
	}

	s.sweepOrphans(ctx, claimed)
	s.notify()
	s.syncQueueGauges(ctx)

	return nil
}

// observeSweep records what the store already did with an interrupted job.
// jobs_total is not touched here: the sweep result carries no kind or origin,
// and plan.md 24 names jobs_requeued_total and jobs_failed_total for exactly
// these two outcomes.
func (s *Service) observeSweep(action store.SweepAction) {
	switch action {
	case store.SweepRequeued:
		s.mx.jobRequeued()
	case store.SweepFailed:
		s.mx.jobFailed(domain.FailInterrupted)
	case store.SweepNeedsCheck:
	}
}

// recoverOne returns the staging path the orphan sweep must leave alone, if any.
func (s *Service) recoverOne(ctx context.Context, r store.SweepResult) (string, error) {
	switch r.Action {
	case store.SweepRequeued, store.SweepFailed:
		// running and verifying: the store already applied the attempt cap and
		// the front-of-queue rule, and whatever staging file exists is a
		// half-written encode.
		s.removeStaging(ctx, r.StagingPath)

		return "", nil
	case store.SweepNeedsCheck:
		if r.FoundState == domain.JobAwaitingStreamEnd {
			return s.recoverAwaiting(ctx, r)
		}

		return "", s.recoverPromoting(ctx, r)
	default:
		return "", fmt.Errorf("%w: %q", errUnknownSweepAction, r.Action)
	}
}

// recoverPromoting is the consistency check of plan.md 19.2. It is the only
// state where the library file itself may be mid-change, and the CODARR tag is
// checked rather than a timestamp inferred from precisely because a timestamp
// cannot tell the two outcomes apart.
func (s *Service) recoverPromoting(ctx context.Context, r store.SweepResult) error {
	media, err := s.store.GetMediaFile(ctx, r.MediaFileID)
	if err != nil {
		return fmt.Errorf("loading media file %d: %w", r.MediaFileID, err)
	}

	answer, probe, detail := s.promotionVerdict(ctx, media)

	switch answer {
	case verdictSourceIntact:
		s.removeStaging(ctx, r.StagingPath)

		return s.requeue(ctx, r)
	case verdictPromoted:
		return s.markPromoted(ctx, r, media, probe)
	case verdictUnknown:
		return s.failUndecidable(ctx, r, media, detail)
	}

	return nil
}

// promotionVerdict answers which side of the rename the process died on.
//
// The fingerprint comes first and the tag second on purpose: mkvmerge carries
// global tags through a third-party rewrite, so a source that was promoted once
// before and has since been modified still reports the tag. Only the
// fingerprint recorded at analysis proves the file on disk is still the source.
func (s *Service) promotionVerdict(ctx context.Context, media domain.MediaFile) (verdict, *ffprobe.Result, string) {
	if media.Fingerprint != "" {
		current, err := s.fp.Sparse(media.Path)
		if err != nil {
			return verdictUnknown, nil, fmt.Sprintf("%s could not be fingerprinted: %v", media.Path, err)
		}

		if current == media.Fingerprint {
			return verdictSourceIntact, nil, ""
		}
	}

	probe, err := s.prober.Probe(ctx, media.Path)
	if err != nil {
		return verdictUnknown, nil, fmt.Sprintf("%s could not be probed: %v", media.Path, err)
	}

	if !taggedWithCurrentPolicy(probe) {
		return verdictUnknown, probe,
			fmt.Sprintf("%s is neither the source analysis recorded nor an output of the current policy", media.Path)
	}

	return verdictPromoted, probe, ""
}

// taggedWithCurrentPolicy is the half of plan.md 12's conjunction that is
// available mid-promotion: the output fingerprint was never recorded, because
// recording it is exactly what the crash interrupted.
func taggedWithCurrentPolicy(probe *ffprobe.Result) bool {
	if _, ok := probe.Format.Tag(decide.TagPresent); !ok {
		return false
	}

	policy, ok := probe.Format.Tag(decide.TagPolicy)

	return ok && policy == decide.PolicyHash()
}

// markPromoted finishes a job whose rename landed but which never got to record
// anything. plan.md 19.2 asks specifically for the Plex and *arr notifications
// that never fired.
func (s *Service) markPromoted(
	ctx context.Context,
	r store.SweepResult,
	media domain.MediaFile,
	probe *ffprobe.Result,
) error {
	j, err := s.store.GetJob(ctx, r.JobID)
	if err != nil {
		return fmt.Errorf("loading job %d: %w", r.JobID, err)
	}

	info, err := s.fs.Stat(media.Path)
	if err != nil {
		return fmt.Errorf("stat of the promoted file %s: %w", media.Path, err)
	}

	sum, err := s.fp.Sparse(media.Path)
	if err != nil {
		return fmt.Errorf("fingerprinting the promoted file %s: %w", media.Path, err)
	}

	actual := s.elapsed(j)
	identity := domain.OutputIdentity{
		Fingerprint: sum,
		SizeBytes:   info.Size,
		MTime:       info.MTime.Unix(),
		PolicyHash:  decide.PolicyHash(),
		RecordedAt:  s.clk.Now(),
	}

	transform := decide.MergeMeasured(j.Transform, probe, actual)
	transform.OutputIdentity = &identity

	err = s.store.RecordPromotion(ctx, store.PromotionUpdate{
		JobID:             j.ID,
		MediaFileID:       media.ID,
		OutputFingerprint: identity.Fingerprint,
		OutputSize:        identity.SizeBytes,
		OutputMTime:       identity.MTime,
		PolicyHash:        identity.PolicyHash,
		Transform:         transform,
		ActualSeconds:     actual,
		PromotedAt:        s.clk.Now(),
	})
	if err != nil {
		return fmt.Errorf("recording the recovered promotion of job %d: %w", j.ID, err)
	}

	s.observe(ctx, domain.JobDone, j.Kind, j.Origin)
	s.mx.transcodeDuration(j.Kind, j.EncoderUsed, float64(actual))

	s.log.InfoContext(ctx, "an interrupted promotion had already completed, finishing it",
		slog.Int64("job_id", j.ID), slog.String("path", media.Path))

	if err := s.notifier.NotifyPromoted(ctx, media.Path); err != nil {
		s.mx.error(errorNotify)
		s.log.WarnContext(ctx, "the deferred post-promotion notification failed",
			slog.Int64("job_id", j.ID), slog.String("path", media.Path), slog.Any("error", err))
	}

	return nil
}

// failUndecidable is the case plan.md 19.2 says cannot happen. Re-queueing
// would re-encode from a probe that no longer describes the file on disk, so
// the job fails with a message naming what was found instead.
func (s *Service) failUndecidable(ctx context.Context, r store.SweepResult, media domain.MediaFile, detail string) error {
	message := "the job was interrupted while promoting and the outcome could not be established: " + detail +
		"; the staging file has been left in place for inspection"

	if err := s.store.FailJob(ctx, r.JobID, domain.FailPromote, message, ""); err != nil {
		return fmt.Errorf("failing undecidable job %d: %w", r.JobID, err)
	}

	s.mx.jobFailed(domain.FailPromote)

	s.log.ErrorContext(ctx, "an interrupted promotion could not be decided",
		slog.Int64("job_id", r.JobID), slog.String("path", media.Path), slog.String("detail", detail))

	return nil
}

// recoverAwaiting is plan.md 19.2's resumable case. The expensive work is done
// and the staging file is a verified output already sitting on the destination
// filesystem, so it is worth far more than a re-encode.
func (s *Service) recoverAwaiting(ctx context.Context, r store.SweepResult) (string, error) {
	j, err := s.store.GetJob(ctx, r.JobID)
	if err != nil {
		return "", fmt.Errorf("loading job %d: %w", r.JobID, err)
	}

	if s.stagingVerifies(ctx, j) {
		s.addPending(j.ID)
		s.log.InfoContext(ctx, "resuming an interrupted promotion, its output still verifies",
			slog.Int64("job_id", j.ID), slog.String("staging_path", j.StagingPath))

		return j.StagingPath, nil
	}

	// The output is gone or no longer verifies. The rename may still have
	// landed: the promoter reports a block but not an unblock, so a job can be
	// in awaiting_stream_end at the moment of the replace. The consistency
	// check settles it rather than re-queueing over a finished promotion.
	return "", s.recoverPromoting(ctx, r)
}

func (s *Service) stagingVerifies(ctx context.Context, j domain.Job) bool {
	if j.StagingPath == "" {
		return false
	}

	if _, err := s.fs.Stat(j.StagingPath); err != nil {
		return false
	}

	t, err := s.taskFromRow(ctx, j)
	if err != nil {
		s.log.WarnContext(ctx, "an interrupted promotion could not be rebuilt from its row",
			slog.Int64("job_id", j.ID), slog.Any("error", err))

		return false
	}

	warnings, err := s.promoter.Verify(ctx, promote.Request{
		JobID:      j.ID,
		SourcePath: t.media.Path,
		Staging:    t.staging,
		Plan:       t.plan,
		Source:     t.source,

		// Without this a resumed job on a legacy container is re-verified with
		// no fallback and fails on a duration its own source header misreported
		// (15.3), which is the one case 19.2 calls worth resuming.
		FinalOutTimeSeconds: t.finalOut.Seconds(),
	})
	if err != nil {
		s.log.WarnContext(ctx, "the staged output of an interrupted promotion no longer verifies",
			slog.Int64("job_id", j.ID), slog.Any("error", err))

		return false
	}

	for _, w := range warnings {
		s.log.WarnContext(ctx, "verification warning on a resumed promotion",
			slog.Int64("job_id", j.ID), slog.String("warning", w))
	}

	return true
}

func (s *Service) requeue(ctx context.Context, r store.SweepResult) error {
	res, err := s.store.RequeueInterruptedJob(ctx, r.JobID)
	if err != nil {
		return fmt.Errorf("re-queueing interrupted job %d: %w", r.JobID, err)
	}

	s.observeSweep(res.Action)

	s.log.InfoContext(ctx, "interrupted job resolved",
		slog.Int64("job_id", r.JobID),
		slog.String("action", string(res.Action)),
		slog.Int("attempt", res.Attempt))

	return nil
}

func (s *Service) sweepOrphans(ctx context.Context, claimed []string) {
	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		s.mx.error(errorOrphanSweep)
		s.log.WarnContext(ctx, "the roots could not be read, skipping the orphan sweep", slog.Any("error", err))

		return
	}

	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.Path)
	}

	removed, err := s.promoter.Sweep(ctx, paths, claimed)
	if err != nil {
		s.mx.error(errorOrphanSweep)
		s.log.WarnContext(ctx, "the orphan sweep did not complete cleanly", slog.Any("error", err))
	}

	if len(removed) > 0 {
		s.log.InfoContext(ctx, "orphan sweep finished", slog.Int("removed", len(removed)))
	}
}

// resume finishes a promotion that a restart interrupted. It runs on the worker
// goroutine, ahead of claiming new work, so the one-transcode-at-a-time rule
// still holds.
func (s *Service) resume(parent context.Context, jobID int64) error {
	j, err := s.store.GetJob(parent, jobID)
	if err != nil {
		return fmt.Errorf("loading resumable job %d: %w", jobID, err)
	}

	if j.State != domain.JobAwaitingStreamEnd {
		return nil
	}

	return s.withRunning(parent, j, s.resumePipeline)
}

func (s *Service) resumePipeline(ctx context.Context, j domain.Job) error {
	t, err := s.taskFromRow(ctx, j)
	if err != nil {
		return err
	}

	return s.finalise(ctx, t)
}

// taskFromRow rebuilds enough of a job to promote its finished output. The plan
// comes from the media row rather than a fresh one, because what has to be
// verified is the file that was actually produced.
func (s *Service) taskFromRow(ctx context.Context, j domain.Job) (*task, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, wrapf(domain.FailInternal, err, "the settings could not be read")
	}

	media, err := s.store.GetMediaFile(ctx, j.MediaFileID)
	if err != nil {
		return nil, wrapf(domain.FailInternal, err, "media file %d could not be read", j.MediaFileID)
	}

	probe, err := storedProbe(media)
	if err != nil {
		return nil, err
	}

	if media.Plan == nil {
		return nil, failf(domain.FailInternal, "%s has no stored plan, so its output cannot be verified", media.Path)
	}

	plan, ok := planFor(j.Origin, *media.Plan)
	if !ok {
		return nil, failf(domain.FailInternal, "%s cannot be re-planned for a %s job", media.Path, j.Origin)
	}

	t := &task{
		job:       j,
		media:     media,
		settings:  settings,
		probe:     probe,
		plan:      plan,
		transform: j.Transform,
		duration:  durationOf(probe),
		finalOut:  time.Duration(j.FinalOutTimeUS) * time.Microsecond,
	}

	t.source = promote.SourceState{
		SizeBytes:       media.SizeBytes,
		MTime:           media.MTime,
		Fingerprint:     media.Fingerprint,
		DurationSeconds: t.duration.Seconds(),
		Video:           t.transform.Video.Before,
		LegacyContainer: decide.IsLegacyContainer(plan.SourceContainer),
	}

	if t.staging, err = s.stagingFor(j, media.Path); err != nil {
		return nil, err
	}

	s.setStaging(t.staging.Path)

	return t, nil
}

// stagingFor rebuilds where the output is and where the rename takes it from.
// CrossDevice is not persisted, so it is re-measured; on the primary path the
// staging file is a sibling of the destination and the answer is trivially no.
func (s *Service) stagingFor(j domain.Job, destPath string) (promote.Staging, error) {
	if j.StagingPath == "" {
		return promote.Staging{}, failf(domain.FailPromote,
			"job %d has no staging path recorded, so its output cannot be found", j.ID)
	}

	staging := promote.Staging{Path: j.StagingPath, FinalPath: j.StagingPath, UsedTempDir: j.UsedTempDir}
	if !j.UsedTempDir {
		return staging, nil
	}

	destDir := filepath.Dir(destPath)
	staging.FinalPath = filepath.Join(destDir, filepath.Base(j.StagingPath))

	stagingDir, err := s.fs.Stat(filepath.Dir(j.StagingPath))
	if err != nil {
		return promote.Staging{}, wrapf(domain.FailPromote, err,
			"the staging directory of job %d could not be stat'd", j.ID)
	}

	dest, err := s.fs.Stat(destDir)
	if err != nil {
		return promote.Staging{}, wrapf(domain.FailPromote, err, "the destination directory %s could not be stat'd", destDir)
	}

	staging.CrossDevice = stagingDir.Device != dest.Device

	return staging, nil
}
