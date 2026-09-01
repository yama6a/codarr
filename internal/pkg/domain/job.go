package domain

import "time"

// JobState is the queue state machine:
//
//	queued -> running -> verifying -> awaiting_stream_end -> promoting -> done
//	                 \-> cancelled
//	                 \-> failed
type JobState string

const (
	JobQueued            JobState = "queued"
	JobRunning           JobState = "running"
	JobVerifying         JobState = "verifying"
	JobAwaitingStreamEnd JobState = "awaiting_stream_end"
	JobPromoting         JobState = "promoting"
	JobDone              JobState = "done"
	JobCancelled         JobState = "cancelled"
	JobFailed            JobState = "failed"
)

// ActiveJobStates are the states the partial unique index treats as "this file
// already has a job", which is what makes enqueue idempotent.
func ActiveJobStates() []JobState {
	return []JobState{JobQueued, JobRunning, JobVerifying, JobAwaitingStreamEnd, JobPromoting}
}

// InFlightJobStates are the non-terminal states a crash can leave behind; with one
// worker process, anything found in one at startup was interrupted.
func InFlightJobStates() []JobState {
	return []JobState{JobRunning, JobVerifying, JobAwaitingStreamEnd, JobPromoting}
}

// JobOrigin records what asked for the job.
type JobOrigin string

const (
	OriginIngest     JobOrigin = "ingest"
	OriginManual     JobOrigin = "manual"
	OriginRecheck    JobOrigin = "recheck"
	OriginSpaceSweep JobOrigin = "space_sweep"
)

// FailureCode is the machine-readable half of a failure. A failed job without
// one is a bug.
type FailureCode string

const (
	FailInterrupted  FailureCode = "interrupted"
	FailPreflight    FailureCode = "preflight_failed"
	FailProbe        FailureCode = "probe_failed"
	FailFfmpeg       FailureCode = "ffmpeg_failed"
	FailVerification FailureCode = "verification_failed"
	FailPromote      FailureCode = "promote_failed"
	FailInternal     FailureCode = "internal_error"
)

// MaxAutoAttempts caps automatic re-queueing of interrupted jobs. Without it, a
// process that dies on one particular file loops forever and burns the array.
const MaxAutoAttempts = 3

// Priority defaults. Lower runs first, so quick wins clear ahead of encodes.
const (
	PriorityQuick  = 90
	PriorityNormal = 100
	PriorityFull   = 110
)

// Job is one unit of work against one media file.
type Job struct {
	ID          int64
	MediaFileID int64
	Kind        Kind
	Origin      JobOrigin
	Priority    int
	State       JobState
	Attempt     int

	Transform TransformRecord

	StagingPath      string
	UsedTempDir      bool
	FfmpegArgv       []string
	ProgressPct      float64
	ProgressSpeed    float64
	ProgressFPS      float64
	EstimatedSeconds int
	ActualSeconds    int

	// ffmpeg's own last out_time (14.3), persisted because 19.2 resumes
	// awaiting_stream_end across a restart and 15.3 needs it for the fallback.
	FinalOutTimeUS int64

	EncoderUsed    Encoder
	DecodePath     DecodePath
	FellBack       bool
	FallbackReason string

	SourceSize        int64
	OutputSize        int64
	OutputFingerprint string
	OutputFullHash    string

	BlockedBy      string
	FailureCode    FailureCode
	FailureMessage string
	StderrTail     string

	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}
