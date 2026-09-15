package job

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
	"go.uber.org/zap"
)

// Run consumes the queue until ctx is cancelled, returning nil on shutdown and
// leaving whatever was in flight for the sweep of 19.2 (plan.md 19).
func (s *Service) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		ran, err := s.RunOnce(ctx)
		if err != nil {
			s.mx.error(errorWorker)
			s.log.Error("the worker could not take the next job", zap.Error(err))
		}

		if ran {
			continue
		}

		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-s.clk.After(s.idlePoll):
		}
	}
}

// RunOnce takes at most one piece of work, preferring a promotion resumed from a
// crash (19.2), whose expensive half is already done.
func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	if id, ok := s.nextPending(); ok {
		return true, s.resume(ctx, id)
	}

	paused, err := s.Paused(ctx)
	if err != nil {
		return false, err
	}

	if paused {
		return false, nil
	}

	j, ok, err := s.store.ClaimNextJob(ctx)
	if err != nil {
		return false, fmt.Errorf("claiming the next job: %w", err)
	}

	if !ok {
		return false, nil
	}

	return true, s.execute(ctx, j)
}

// Paused reports whether the queue is accepting new work. A running job is
// unaffected (19).
func (s *Service) Paused(ctx context.Context) (bool, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return false, fmt.Errorf("reading the queue settings: %w", err)
	}

	return settings.QueuePaused, nil
}

// Pause stops new jobs starting. Whatever is running continues to completion.
func (s *Service) Pause(ctx context.Context) error { return s.setPaused(ctx, true) }

// Resume starts consuming the queue again.
func (s *Service) Resume(ctx context.Context) error {
	if err := s.setPaused(ctx, false); err != nil {
		return err
	}

	s.notify()

	return nil
}

func (s *Service) setPaused(ctx context.Context, paused bool) error {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("reading the queue settings: %w", err)
	}

	if settings.QueuePaused == paused {
		return nil
	}

	settings.QueuePaused = paused
	settings.UpdatedAt = s.clk.Now()

	if err := s.store.UpdateSettings(ctx, settings); err != nil {
		return fmt.Errorf("storing the queue settings: %w", err)
	}

	s.log.Info("queue pause changed", zap.Bool("paused", paused))

	return nil
}

// Cancel stops a job, blocking until a running one has actually stopped so the
// caller's next read of the job row sees the final state (19).
func (s *Service) Cancel(ctx context.Context, jobID int64) error {
	if done, ok := s.requestCancel(jobID); ok {
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("waiting for job %d to stop: %w", jobID, ctx.Err())
		}
	}

	j, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("loading job %d: %w", jobID, err)
	}

	if err := s.store.CancelJob(ctx, jobID); err != nil {
		return fmt.Errorf("cancelling job %d: %w", jobID, err)
	}

	s.releaseMedia(ctx, j.MediaFileID)
	s.removeStaging(j.StagingPath)
	s.observe(ctx, domain.JobCancelled, j.Kind, j.Origin)

	return nil
}

// requestCancel signals the in-flight job if jobID names it, returning the
// channel that closes once the worker has finished the transition.
func (s *Service) requestCancel(jobID int64) (<-chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current == nil || s.current.id != jobID {
		return nil, false
	}

	s.current.requested = true
	s.current.cancel()

	return s.current.done, true
}

// Restart re-queues a cancelled or failed job ahead of everything already
// queued and resets the automatic-interruption counter (19, 19.2).
func (s *Service) Restart(ctx context.Context, jobID int64) (domain.Job, error) {
	j, err := s.store.RestartJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, fmt.Errorf("restarting job %d: %w", jobID, err)
	}

	s.notify()
	s.observe(ctx, domain.JobQueued, j.Kind, j.Origin)

	return j, nil
}

// Runs on a context detached from the job's own, because that context is exactly
// what was just cancelled.
func (s *Service) finishCancelled(ctx context.Context, j domain.Job, stagingPath string) error {
	ctx = context.WithoutCancel(ctx)

	s.removeStaging(stagingPath)

	if err := s.store.CancelJob(ctx, j.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("cancelling job %d: %w", j.ID, err)
	}

	s.releaseMedia(ctx, j.MediaFileID)
	s.observe(ctx, domain.JobCancelled, j.Kind, j.Origin)
	s.log.Info("job cancelled", zap.Int64("job_id", j.ID))

	return nil
}

// The staging file survives a verification failure and only that: 15.3 keeps it
// for inspection, while a half-written encode is gigabytes of nothing (19.1).
func (s *Service) finishFailed(ctx context.Context, j domain.Job, f *Error, stagingPath string) error {
	ctx = context.WithoutCancel(ctx)

	if f.Code != domain.FailVerification {
		s.removeStaging(stagingPath)
	}

	if err := s.store.FailJob(ctx, j.ID, f.Code, f.Error(), f.StderrTail); err != nil {
		return fmt.Errorf("failing job %d: %w", j.ID, err)
	}

	s.mx.jobFailed(f.Code)
	s.observe(ctx, domain.JobFailed, j.Kind, j.Origin)

	s.log.Error("job failed",
		zap.Int64("job_id", j.ID),
		zap.String("failure_code", string(f.Code)),
		zap.String("failure_message", f.Error()))

	return nil
}

// releaseMedia takes a file back out of processing, to the state analysis left it
// in, since nothing was written.
func (s *Service) releaseMedia(ctx context.Context, mediaFileID int64) {
	if err := s.store.SetMediaStatus(ctx, mediaFileID, domain.MediaAnalyzed, ""); err != nil {
		s.mx.error(errorState)
		s.log.Warn("resetting the media status failed", zap.Int64("media_file_id", mediaFileID), zap.Error(err))
	}
}

func (s *Service) removeStaging(path string) {
	if path == "" {
		return
	}

	if err := s.fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.mx.error(errorStaging)
		s.log.Warn("removing a staging file failed", zap.String("path", path), zap.Error(err))

		return
	}

	s.log.Info("staging file removed", zap.String("path", path))
}
