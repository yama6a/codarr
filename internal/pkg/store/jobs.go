package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

const jobColumns = `id, media_file_id, kind, origin, priority, state, attempt, transform_json,
	staging_path, used_temp_dir, ffmpeg_argv, progress_pct, progress_speed, estimated_seconds,
	actual_seconds, final_out_time_us, encoder_used, decode_path, fell_back, fallback_reason, source_size,
	output_size, output_fingerprint, output_full_hash, blocked_by, failure_code,
	failure_message, stderr_tail, queued_at, started_at, finished_at`

// EnqueueJob is idempotent. idx_jobs_one_active_per_file is a partial unique
// index over the active states, so a webhook and a manual trigger racing on the
// same file produce one job; the loser is a no-op reporting created=false, not
// an error (plan.md 17.1).
func (s *store) EnqueueJob(ctx context.Context, j domain.Job) (domain.Job, bool, error) {
	transform, err := marshalJSON(j.Transform)
	if err != nil {
		return domain.Job{}, false, err
	}

	var created bool

	err = s.write(ctx, func(tx *sql.Tx) error {
		const insert = `
			INSERT INTO jobs (media_file_id, kind, origin, priority, state, attempt,
				transform_json, queued_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT DO NOTHING
			RETURNING id`

		err := tx.QueryRowContext(ctx, insert, j.MediaFileID, string(j.Kind), string(j.Origin),
			j.Priority, string(domain.JobQueued), j.Attempt, transform, formatTime(j.QueuedAt),
		).Scan(&j.ID)
		if notFound(err) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("insert job: %w", err)
		}

		created = true

		const media = `UPDATE media_files SET status = ?, updated_at = ? WHERE id = ?`
		if _, err := tx.ExecContext(ctx, media, string(domain.MediaQueued),
			formatTime(j.QueuedAt), j.MediaFileID); err != nil {
			return fmt.Errorf("mark media queued: %w", err)
		}

		j.State = domain.JobQueued

		return nil
	})
	if err != nil {
		return domain.Job{}, false, err
	}

	if !created {
		s.logger.Info("enqueue skipped, file already has an active job",
			slog.Int64("media_file_id", j.MediaFileID), slog.String("origin", string(j.Origin)))

		return domain.Job{}, false, nil
	}

	return j, true, nil
}

func (s *store) GetJob(ctx context.Context, id int64) (domain.Job, error) {
	const query = `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`

	j, err := scanJob(s.db.read.QueryRowContext(ctx, query, id))
	if notFound(err) {
		return domain.Job{}, ErrNotFound
	}

	if err != nil {
		return domain.Job{}, fmt.Errorf("scan job: %w", err)
	}

	return j, nil
}

func (s *store) ListJobs(ctx context.Context, f JobFilter) ([]domain.Job, int, error) {
	var c conds

	c.in("state", strs(f.State))

	if f.MediaFileID != nil {
		c.add("media_file_id = ?", *f.MediaFileID)
	}

	where, args := c.where(), c.args

	var total int
	if err := s.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`+where, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	//nolint:gosec // the only interpolation is placeholder lists built from constants
	query := `SELECT ` + jobColumns + ` FROM jobs` + where +
		` ORDER BY priority ASC, queued_at ASC, id ASC LIMIT ? OFFSET ?`

	rows, err := s.db.read.QueryContext(ctx, query, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("select jobs: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []domain.Job{}

	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan job: %w", err)
		}

		out = append(out, j)
	}

	if err := closeRows(rows); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

func (s *store) ActiveJobForMedia(ctx context.Context, mediaFileID int64) (domain.Job, bool, error) {
	states := domain.ActiveJobStates()

	args := make([]any, 0, len(states)+1)
	args = append(args, mediaFileID)

	for _, st := range states {
		args = append(args, string(st))
	}

	query := `SELECT ` + jobColumns + ` FROM jobs WHERE media_file_id = ? AND state IN (` +
		placeholders(len(states)) + `) ORDER BY id DESC LIMIT 1`

	j, err := scanJob(s.db.read.QueryRowContext(ctx, query, args...))
	if notFound(err) {
		return domain.Job{}, false, nil
	}

	if err != nil {
		return domain.Job{}, false, fmt.Errorf("scan active job: %w", err)
	}

	return j, true, nil
}

// ClaimNextJob takes the lowest priority then oldest queued job and moves it to
// running inside one transaction, so two callers can never claim the same row.
func (s *store) ClaimNextJob(ctx context.Context) (domain.Job, bool, error) {
	var (
		job     domain.Job
		claimed bool
	)

	err := s.write(ctx, func(tx *sql.Tx) error {
		const pick = `
			SELECT id FROM jobs WHERE state = ?
			ORDER BY priority ASC, queued_at ASC, id ASC LIMIT 1`

		var id int64

		err := tx.QueryRowContext(ctx, pick, string(domain.JobQueued)).Scan(&id)
		if notFound(err) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("pick next job: %w", err)
		}

		now := time.Now().UTC()

		const claim = `UPDATE jobs SET state = ?, started_at = ? WHERE id = ? AND state = ?`

		res, err := tx.ExecContext(ctx, claim, string(domain.JobRunning), formatTime(now),
			id, string(domain.JobQueued))
		if err != nil {
			return fmt.Errorf("claim job: %w", err)
		}

		if err := requireOne(res); err != nil {
			return err
		}

		job, err = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
		if err != nil {
			return fmt.Errorf("reload claimed job: %w", err)
		}

		const media = `UPDATE media_files SET status = ?, updated_at = ? WHERE id = ?`
		if _, err := tx.ExecContext(ctx, media, string(domain.MediaProcessing),
			formatTime(now), job.MediaFileID); err != nil {
			return fmt.Errorf("mark media processing: %w", err)
		}

		claimed = true

		return nil
	})
	if err != nil {
		return domain.Job{}, false, err
	}

	return job, claimed, nil
}

func (s *store) SetJobState(ctx context.Context, id int64, state domain.JobState) error {
	return s.execOne(ctx, `UPDATE jobs SET state = ? WHERE id = ?`, string(state), id)
}

func (s *store) SetJobBlockedBy(ctx context.Context, id int64, blockedBy string) error {
	return s.execOne(ctx, `UPDATE jobs SET blocked_by = ? WHERE id = ?`, nullString(blockedBy), id)
}

func (s *store) UpdateJobExecution(ctx context.Context, u ExecutionUpdate) error {
	argv, err := marshalStrings(u.FfmpegArgv)
	if err != nil {
		return err
	}

	const query = `
		UPDATE jobs SET
			staging_path = ?, used_temp_dir = ?, ffmpeg_argv = ?, encoder_used = ?,
			decode_path = ?, fell_back = ?, fallback_reason = ?, source_size = ?,
			estimated_seconds = ?, final_out_time_us = ?
		WHERE id = ?`

	return s.execOne(ctx, query, nullString(u.StagingPath), u.UsedTempDir, argv,
		nullString(string(u.EncoderUsed)), nullString(string(u.DecodePath)), u.FellBack,
		nullString(u.FallbackReason), nullInt64(u.SourceSize),
		nullInt64(int64(u.EstimatedSeconds)), nullInt64(u.FinalOutTimeUS), u.JobID)
}

func (s *store) UpdateJobProgress(ctx context.Context, id int64, pct, speed float64, estimatedSeconds int) error {
	const query = `
		UPDATE jobs SET progress_pct = ?, progress_speed = ?, estimated_seconds = ?
		WHERE id = ?`

	return s.execOne(ctx, query, pct, speed, nullInt64(int64(estimatedSeconds)), id)
}

func (s *store) UpdateJobTransform(ctx context.Context, id int64, t domain.TransformRecord) error {
	transform, err := marshalJSON(t)
	if err != nil {
		return err
	}

	return s.execOne(ctx, `UPDATE jobs SET transform_json = ? WHERE id = ?`, transform, id)
}

// FailJob writes both halves of plan.md 19.1: a failed job without a code and a
// message is a bug, so both are required here.
func (s *store) FailJob(ctx context.Context, id int64, code domain.FailureCode, message, stderrTail string) error {
	if code == "" || message == "" {
		return ErrInvalidFailure
	}

	return s.write(ctx, func(tx *sql.Tx) error {
		return failJobTx(ctx, tx, id, code, message, stderrTail)
	})
}

func (s *store) CancelJob(ctx context.Context, id int64) error {
	const query = `
		UPDATE jobs SET state = ?, finished_at = ? WHERE id = ? AND state NOT IN (?, ?, ?)`

	return s.execOne(ctx, query, string(domain.JobCancelled), formatTime(time.Now()), id,
		string(domain.JobDone), string(domain.JobCancelled), string(domain.JobFailed))
}

// RestartJob re-queues a cancelled or failed job ahead of everything queued and
// resets the attempt counter, since plan.md 19.2 makes a manual retry a fresh
// start rather than a continuation of the interruption streak.
func (s *store) RestartJob(ctx context.Context, id int64) (domain.Job, error) {
	var out domain.Job

	err := s.write(ctx, func(tx *sql.Tx) error {
		front, err := frontPriority(ctx, tx, id)
		if err != nil {
			return err
		}

		const query = `
			UPDATE jobs SET
				state = ?, priority = ?, attempt = 0, progress_pct = NULL,
				progress_speed = NULL, started_at = NULL, finished_at = NULL,
				failure_code = NULL, failure_message = NULL, stderr_tail = NULL,
				blocked_by = NULL, staging_path = NULL, queued_at = ?
			WHERE id = ? AND state IN (?, ?)`

		res, err := tx.ExecContext(ctx, query, string(domain.JobQueued), front,
			formatTime(time.Now()), id, string(domain.JobCancelled), string(domain.JobFailed))
		if err != nil {
			return fmt.Errorf("restart job: %w", err)
		}

		if err := requireOne(res); err != nil {
			return err
		}

		out, err = scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
		if err != nil {
			return fmt.Errorf("reload restarted job: %w", err)
		}

		const media = `UPDATE media_files SET status = ?, updated_at = ? WHERE id = ?`
		if _, err := tx.ExecContext(ctx, media, string(domain.MediaQueued),
			formatTime(time.Now()), out.MediaFileID); err != nil {
			return fmt.Errorf("mark media queued: %w", err)
		}

		return nil
	})
	if err != nil {
		return domain.Job{}, err
	}

	return out, nil
}

// SweepInterruptedJobs is the startup sweep of plan.md 19.2. There is one
// worker process, so anything still in an in-flight state was interrupted.
//
// running and verifying are re-queued at the front with attempt+1, or failed
// once attempt has reached domain.MaxAutoAttempts. promoting and
// awaiting_stream_end are reported as SweepNeedsCheck and left exactly as
// found: deciding those needs the destination file and the staging file, which
// is the job package's business, not the store's. The caller finishes them with
// RecordPromotion, RequeueInterruptedJob or FailJob.
func (s *store) SweepInterruptedJobs(ctx context.Context) ([]SweepResult, error) {
	var out []SweepResult

	err := s.write(ctx, func(tx *sql.Tx) error {
		found, err := selectInterrupted(ctx, tx)
		if err != nil {
			return err
		}

		out = make([]SweepResult, 0, len(found))

		for _, j := range found {
			res, err := sweepOne(ctx, tx, j)
			if err != nil {
				return err
			}

			out = append(out, res)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// RequeueInterruptedJob applies the same cap and front-of-queue rule to a
// single job, for a caller that has finished the consistency check a
// SweepNeedsCheck result asked for.
func (s *store) RequeueInterruptedJob(ctx context.Context, id int64) (SweepResult, error) {
	var out SweepResult

	err := s.write(ctx, func(tx *sql.Tx) error {
		const query = `SELECT ` + interruptedColumns + ` FROM jobs WHERE id = ?`

		j, err := scanInterrupted(tx.QueryRowContext(ctx, query, id))
		if notFound(err) {
			return ErrNotFound
		}

		if err != nil {
			return err
		}

		out, err = requeueOrFail(ctx, tx, j)

		return err
	})
	if err != nil {
		return SweepResult{}, err
	}

	return out, nil
}

func (s *store) CountJobsByState(ctx context.Context) (map[domain.JobState]int, error) {
	counts, err := s.countBy(ctx, `SELECT state, COUNT(*) FROM jobs GROUP BY state`)
	if err != nil {
		return nil, err
	}

	out := make(map[domain.JobState]int, len(counts))
	for k, v := range counts {
		out[domain.JobState(k)] = v
	}

	return out, nil
}

func (s *store) Stats(ctx context.Context) (Stats, error) {
	const query = `
		SELECT COUNT(*), COALESCE(SUM(source_size), 0), COALESCE(SUM(output_size), 0),
			COALESCE(SUM(actual_seconds), 0)
		FROM jobs WHERE state = ?`

	var out Stats

	err := s.db.read.QueryRowContext(ctx, query, string(domain.JobDone)).
		Scan(&out.FilesDone, &out.BytesIn, &out.BytesOut, &out.EncodeSeconds)
	if err != nil {
		return Stats{}, fmt.Errorf("select job stats: %w", err)
	}

	out.BytesSaved = out.BytesIn - out.BytesOut

	return out, nil
}

const interruptedColumns = `id, media_file_id, state, attempt, priority, staging_path`

func selectInterrupted(ctx context.Context, tx *sql.Tx) ([]interruptedJob, error) {
	states := domain.InFlightJobStates()

	args := make([]any, 0, len(states))
	for _, st := range states {
		args = append(args, string(st))
	}

	//nolint:gosec // the only interpolation is a placeholder list
	query := `SELECT ` + interruptedColumns + ` FROM jobs WHERE state IN (` +
		placeholders(len(states)) + `) ORDER BY id`

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select interrupted jobs: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var found []interruptedJob

	for rows.Next() {
		j, err := scanInterrupted(rows)
		if err != nil {
			return nil, err
		}

		found = append(found, j)
	}

	if err := closeRows(rows); err != nil {
		return nil, err
	}

	return found, nil
}

// sweepOne leaves promoting and awaiting_stream_end exactly as found: both need
// the destination file or the staging file to decide, which is the job
// package's business (plan.md 19.2).
func sweepOne(ctx context.Context, tx *sql.Tx, j interruptedJob) (SweepResult, error) {
	if j.state == domain.JobPromoting || j.state == domain.JobAwaitingStreamEnd {
		return SweepResult{
			JobID:       j.id,
			MediaFileID: j.mediaFileID,
			FoundState:  j.state,
			Action:      SweepNeedsCheck,
			Attempt:     j.attempt,
			StagingPath: j.stagingPath,
		}, nil
	}

	return requeueOrFail(ctx, tx, j)
}

type interruptedJob struct {
	id          int64
	mediaFileID int64
	state       domain.JobState
	attempt     int
	priority    int
	stagingPath string
}

func scanInterrupted(row rowScanner) (interruptedJob, error) {
	var (
		j       interruptedJob
		state   string
		staging sql.NullString
	)

	if err := row.Scan(&j.id, &j.mediaFileID, &state, &j.attempt, &j.priority, &staging); err != nil {
		return interruptedJob{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	j.state = domain.JobState(state)
	j.stagingPath = staging.String

	return j, nil
}

func requeueOrFail(ctx context.Context, tx *sql.Tx, j interruptedJob) (SweepResult, error) {
	out := SweepResult{
		JobID:       j.id,
		MediaFileID: j.mediaFileID,
		FoundState:  j.state,
		Attempt:     j.attempt,
		StagingPath: j.stagingPath,
	}

	if j.attempt >= domain.MaxAutoAttempts {
		message := fmt.Sprintf(
			"interrupted %d times; the automatic restart cap of %d was reached, retry manually",
			j.attempt+1, domain.MaxAutoAttempts)

		if err := failJobTx(ctx, tx, j.id, domain.FailInterrupted, message, ""); err != nil {
			return SweepResult{}, err
		}

		out.Action = SweepFailed

		return out, nil
	}

	front, err := frontPriority(ctx, tx, j.id)
	if err != nil {
		return SweepResult{}, err
	}

	const query = `
		UPDATE jobs SET
			state = ?, priority = ?, attempt = attempt + 1, progress_pct = NULL,
			progress_speed = NULL, started_at = NULL, staging_path = NULL, blocked_by = NULL
		WHERE id = ?`

	res, err := tx.ExecContext(ctx, query, string(domain.JobQueued), front, j.id)
	if err != nil {
		return SweepResult{}, fmt.Errorf("requeue interrupted job: %w", err)
	}

	if err := requireOne(res); err != nil {
		return SweepResult{}, err
	}

	const media = `UPDATE media_files SET status = ?, updated_at = ? WHERE id = ?`
	if _, err := tx.ExecContext(ctx, media, string(domain.MediaQueued),
		formatTime(time.Now()), j.mediaFileID); err != nil {
		return SweepResult{}, fmt.Errorf("mark media queued: %w", err)
	}

	out.Action = SweepRequeued
	out.Attempt = j.attempt + 1

	return out, nil
}

// frontPriority is plan.md 19: min(queued priorities) - 1, so a re-queued job
// runs next. With nothing queued it steps ahead of the job's own priority, which
// keeps repeated interruptions monotonic instead of pinning them at 99.
func frontPriority(ctx context.Context, tx *sql.Tx, jobID int64) (int, error) {
	const query = `
		SELECT MIN(COALESCE(
			(SELECT MIN(priority) FROM jobs WHERE state = ?), j.priority), j.priority)
		FROM jobs j WHERE j.id = ?`

	var current int

	err := tx.QueryRowContext(ctx, query, string(domain.JobQueued), jobID).Scan(&current)
	if notFound(err) {
		return 0, ErrNotFound
	}

	if err != nil {
		return 0, fmt.Errorf("compute front priority: %w", err)
	}

	return current - 1, nil
}

func failJobTx(ctx context.Context, tx *sql.Tx, id int64, code domain.FailureCode, message, stderrTail string) error {
	const query = `
		UPDATE jobs SET
			state = ?, failure_code = ?, failure_message = ?, stderr_tail = ?, finished_at = ?
		WHERE id = ?`

	res, err := tx.ExecContext(ctx, query, string(domain.JobFailed), string(code), message,
		nullString(stderrTail), formatTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}

	if err := requireOne(res); err != nil {
		return err
	}

	const media = `
		UPDATE media_files SET status = ?, last_error = ?, updated_at = ?
		WHERE id = (SELECT media_file_id FROM jobs WHERE id = ?)`

	if _, err := tx.ExecContext(ctx, media, string(domain.MediaFailed), message,
		formatTime(time.Now()), id); err != nil {
		return fmt.Errorf("mark media failed: %w", err)
	}

	return nil
}

//nolint:funlen // 30 columns; the length is the schema's
func scanJob(row rowScanner) (domain.Job, error) {
	var (
		j              domain.Job
		kind           string
		origin         string
		state          string
		transform      sql.NullString
		stagingPath    sql.NullString
		ffmpegArgv     sql.NullString
		progressPct    sql.NullFloat64
		progressSpeed  sql.NullFloat64
		estimated      sql.NullInt64
		actual         sql.NullInt64
		finalOutTime   sql.NullInt64
		encoderUsed    sql.NullString
		decodePath     sql.NullString
		fallbackReason sql.NullString
		sourceSize     sql.NullInt64
		outputSize     sql.NullInt64
		outFingerprint sql.NullString
		outFullHash    sql.NullString
		blockedBy      sql.NullString
		failureCode    sql.NullString
		failureMessage sql.NullString
		stderrTail     sql.NullString
		queuedAt       sql.NullString
		startedAt      sql.NullString
		finishedAt     sql.NullString
	)

	err := row.Scan(&j.ID, &j.MediaFileID, &kind, &origin, &j.Priority, &state, &j.Attempt,
		&transform, &stagingPath, &j.UsedTempDir, &ffmpegArgv, &progressPct, &progressSpeed,
		&estimated, &actual, &finalOutTime, &encoderUsed, &decodePath, &j.FellBack, &fallbackReason,
		&sourceSize, &outputSize, &outFingerprint, &outFullHash, &blockedBy, &failureCode,
		&failureMessage, &stderrTail, &queuedAt, &startedAt, &finishedAt)
	if err != nil {
		return domain.Job{}, err //nolint:wrapcheck // sql.ErrNoRows must stay comparable for the caller
	}

	j.Kind = domain.Kind(kind)
	j.Origin = domain.JobOrigin(origin)
	j.State = domain.JobState(state)
	j.StagingPath = stagingPath.String
	j.ProgressPct = progressPct.Float64
	j.ProgressSpeed = progressSpeed.Float64
	j.EstimatedSeconds = int(estimated.Int64)
	j.ActualSeconds = int(actual.Int64)
	j.FinalOutTimeUS = finalOutTime.Int64
	j.EncoderUsed = domain.Encoder(encoderUsed.String)
	j.DecodePath = domain.DecodePath(decodePath.String)
	j.FallbackReason = fallbackReason.String
	j.SourceSize = sourceSize.Int64
	j.OutputSize = outputSize.Int64
	j.OutputFingerprint = outFingerprint.String
	j.OutputFullHash = outFullHash.String
	j.BlockedBy = blockedBy.String
	j.FailureCode = domain.FailureCode(failureCode.String)
	j.FailureMessage = failureMessage.String
	j.StderrTail = stderrTail.String

	if err := unmarshalJSON(transform, &j.Transform); err != nil {
		return domain.Job{}, err
	}

	if err := unmarshalJSON(ffmpegArgv, &j.FfmpegArgv); err != nil {
		return domain.Job{}, err
	}

	if j.QueuedAt, err = scanTime(queuedAt); err != nil {
		return domain.Job{}, err
	}

	if j.StartedAt, err = scanTimePtr(startedAt); err != nil {
		return domain.Job{}, err
	}

	if j.FinishedAt, err = scanTimePtr(finishedAt); err != nil {
		return domain.Job{}, err
	}

	return j, nil
}
